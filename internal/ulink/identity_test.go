package ulink

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestEnsureIdentityIsStable(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureIdentity(dir)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if first.DeviceID == "" || first.Fingerprint == "" || first.ShortCode == "" {
		t.Fatalf("incomplete identity: %+v", first)
	}
	if len(first.PublicKey) == 0 {
		t.Fatal("public key empty")
	}

	second, err := EnsureIdentity(dir)
	if err != nil {
		t.Fatalf("ensure second: %v", err)
	}
	if first.DeviceID != second.DeviceID || first.Fingerprint != second.Fingerprint {
		t.Fatalf("identity changed: %+v vs %+v", first, second)
	}
}

func TestTrustKeyDeduplicates(t *testing.T) {
	dir := t.TempDir()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(pub)

	first, err := TrustKey(dir, "peer-a", key)
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	second, err := TrustKey(dir, "peer-a-again", key)
	if err != nil {
		t.Fatalf("trust again: %v", err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fingerprint mismatch: %s vs %s", first.Fingerprint, second.Fingerprint)
	}
	trusted, err := LoadTrusted(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(trusted) != 1 {
		t.Fatalf("want 1 trusted entry, got %d", len(trusted))
	}
}

func TestTrustKeyRejectsBadKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := TrustKey(dir, "", "not-base64"); err == nil {
		t.Fatal("expected error for invalid key")
	}
	if _, err := TrustKey(dir, "", base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error for short key")
	}
}
