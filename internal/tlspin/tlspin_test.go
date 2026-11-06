package tlspin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
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

func TestHostFromURL(t *testing.T) {
	tests := map[string]string{
		"wss://127.0.0.1:18900/ws/agent":    "127.0.0.1",
		"wss://relay.example.com:8900":      "relay.example.com",
		"ws://localhost:8900/ws/controller": "localhost",
		"wss://[::1]:8900/ws/agent":         "::1",
		"not a url":                         "",
	}
	for in, want := range tests {
		if got := HostFromURL(in); got != want {
			t.Errorf("HostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigDefaultsToSystemVerification(t *testing.T) {
	cfg := Config("wss://relay.example.com:8900", "")
	if cfg.InsecureSkipVerify {
		t.Fatal("empty pin must use Go's normal certificate verification")
	}
	if cfg.VerifyConnection != nil {
		t.Fatal("empty pin must not install a custom verifier")
	}
}

func TestConfigWithPinVerifiesHostnameAndFingerprint(t *testing.T) {
	certDER := []byte("test certificate bytes")
	sum := sha256.Sum256(certDER)
	pin := hex.EncodeToString(sum[:])
	cfg := Config("wss://relay.example.com:8900", pin)
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

	// The hostname comes from the dialed URL, not from SNI: a certificate
	// issued for a different host must be rejected even if the pin matches.
	state.PeerCertificates[0].DNSNames = []string{"attacker.example.net"}
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("certificate for another hostname was accepted")
	}

	state.PeerCertificates[0].DNSNames = []string{"relay.example.com"}
	state.PeerCertificates[0].Raw = []byte("different certificate")
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("certificate with a different fingerprint was accepted")
	}
}

// IP-addressed relays never send SNI, so ConnectionState.ServerName is empty.
// Pin mode must still verify the IP SAN from the dialed URL.
func TestConfigWithPinWorksForIPLiteralRelay(t *testing.T) {
	certDER := []byte("ip certificate bytes")
	sum := sha256.Sum256(certDER)
	pin := hex.EncodeToString(sum[:])
	cfg := Config("wss://127.0.0.1:18900/ws/agent", pin)

	state := tls.ConnectionState{
		ServerName:       "",
		PeerCertificates: []*x509.Certificate{{IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, Raw: certDER}},
	}
	if err := cfg.VerifyConnection(state); err != nil {
		t.Fatalf("IP relay with matching pin rejected: %v", err)
	}

	state.PeerCertificates[0].IPAddresses = []net.IP{net.ParseIP("10.0.0.7")}
	if err := cfg.VerifyConnection(state); err == nil {
		t.Fatal("certificate for another IP was accepted")
	}
}
