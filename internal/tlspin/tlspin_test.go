package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"testing"
)

func TestNormalize(t *testing.T) {
	if Normalize("AA:BB:CC") != "aabbcc" {
		t.Fatal("normalize")
	}
	if Normalize(" aa bb ") != "aabb" {
		t.Fatal("normalize spaces")
	}
}

func TestConfigDefaultsToSystemVerification(t *testing.T) {
	cfg := Config("")
	if cfg.InsecureSkipVerify {
		t.Fatal("empty pin must use Go's normal certificate verification")
	}
	if cfg.VerifyPeerCertificate != nil {
		t.Fatal("empty pin must not install a custom verifier")
	}
}

func TestConfigWithPinUsesExplicitLeafVerifier(t *testing.T) {
	certDER := []byte("test certificate bytes")
	sum := sha256.Sum256(certDER)
	pin := hex.EncodeToString(sum[:])
	cfg := Config(pin)
	if !cfg.InsecureSkipVerify {
		t.Fatal("pin mode must skip CA verification before checking the pin")
	}
	if cfg.VerifyConnection == nil {
		t.Fatal("pin mode must verify hostname and certificate fingerprint")
	}

	state := tls.ConnectionState{
		ServerName:       "relay.example.com",
		PeerCertificates: []*x509.Certificate{{DNSNames: []string{"relay.example.com"}, Raw: certDER}},
	}
	if err := cfg.VerifyConnection(state); err != nil {
		t.Fatalf("correct hostname and pin rejected: %v", err)
	}

	state.ServerName = "attacker.example.net"
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("certificate for another hostname was accepted")
	}

	state.ServerName = "relay.example.com"
	state.PeerCertificates[0].Raw = []byte("different certificate")
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("certificate with a different fingerprint was accepted")
	}
}
