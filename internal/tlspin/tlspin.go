// Package tlspin provides a TLS config that pins a self-signed cert by
// SHA-256 fingerprint.  Use for self-hosted relays instead of blind
// InsecureSkipVerify.
package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Normalize removes separators/case so "AA:BB" and "aabb" compare equal.
func Normalize(pin string) string {
	pin = strings.ToLower(strings.TrimSpace(pin))
	pin = strings.ReplaceAll(pin, ":", "")
	pin = strings.ReplaceAll(pin, " ", "")
	return pin
}

// Config returns a TLS config for a relay URL. With no pin, Go's normal
// certificate and hostname verification applies. A non-empty pin opts into
// self-signed certificates by verifying the leaf fingerprint AND the
// hostname of the relay the caller actually dialed.
//
// The host is taken from rawURL rather than from tls.ConnectionState.ServerName:
// SNI cannot carry IP literals, so ServerName is empty when a self-hosted relay
// is addressed by IP (the common case), which would otherwise make pin mode
// unusable for exactly the deployments it exists for.
func Config(rawURL, pin string) *tls.Config {
	if strings.TrimSpace(pin) == "" {
		return &tls.Config{}
	}
	want := Normalize(pin)
	host := HostFromURL(rawURL)
	return &tls.Config{
		InsecureSkipVerify: true, // pin mode accepts self-signed certs, then verifies identity below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("tls pin: no peer certificate")
			}
			leaf := cs.PeerCertificates[0]
			if host == "" {
				return fmt.Errorf("tls pin: cannot determine relay host for hostname verification")
			}
			if err := leaf.VerifyHostname(host); err != nil {
				return fmt.Errorf("tls pin hostname mismatch for %q: %w", host, err)
			}
			sum := sha256.Sum256(leaf.Raw)
			got := hex.EncodeToString(sum[:])
			if want != "" && got != want {
				return fmt.Errorf("tls pin mismatch: got %s want %s", got, want)
			}
			return nil
		},
	}
}

// HostFromURL extracts the bare hostname (no port) from a ws:// or wss:// URL.
func HostFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}
