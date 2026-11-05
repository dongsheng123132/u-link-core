// Package tlspin provides a TLS config that pins a self-signed cert by
// SHA-256 fingerprint.  Use for self-hosted relays instead of blind
// InsecureSkipVerify.
package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
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

// Config returns a TLS config.  Empty pin keeps the legacy behavior
// (insecure skip verify); a non-empty pin verifies the leaf cert hash.
func Config(pin string) *tls.Config {
	if strings.TrimSpace(pin) == "" {
		return &tls.Config{InsecureSkipVerify: true}
	}
	want := Normalize(pin)
	return &tls.Config{
		InsecureSkipVerify: true, // we verify manually below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("tls pin: no peer certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if want != "" && got != want {
				return fmt.Errorf("tls pin mismatch: got %s want %s", got, want)
			}
			return nil
		},
	}
}
