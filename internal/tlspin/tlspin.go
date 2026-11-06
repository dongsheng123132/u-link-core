// Package tlspin provides a TLS config that pins a self-signed cert by
// SHA-256 fingerprint.  Use for self-hosted relays instead of blind
// InsecureSkipVerify.
package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"strings"
)

// Normalize removes separators/case so "AA:BB" and "aabb" compare equal.
func Normalize(pin string) string {
	pin = strings.ToLower(strings.TrimSpace(pin))
	pin = strings.ReplaceAll(pin, ":", "")
	pin = strings.ReplaceAll(pin, " ", "")
	return pin
}

// Config returns a TLS config. With no pin, Go's normal certificate and
// hostname verification applies. A non-empty pin opts into self-signed
// certificates by verifying the leaf certificate's SHA-256 fingerprint.
func Config(pin string) *tls.Config {
	if strings.TrimSpace(pin) == "" {
		return &tls.Config{}
	}
	want := Normalize(pin)
	return &tls.Config{
		InsecureSkipVerify: true, // pin mode accepts self-signed certs, then verifies identity below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("tls pin: no peer certificate")
			}
			leaf := cs.PeerCertificates[0]
			if cs.ServerName == "" {
				return fmt.Errorf("tls pin: no server name available for hostname verification")
			}
			if err := leaf.VerifyHostname(cs.ServerName); err != nil {
				return fmt.Errorf("tls pin hostname mismatch: %w", err)
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
