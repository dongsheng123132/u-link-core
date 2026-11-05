package devicekey

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.key")
	priv1, pub1, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	priv2, pub2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !priv1.Equal(priv2) || !pub1.Equal(pub2) {
		t.Fatal("key changed between loads")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// Windows has no POSIX permission bits, so this check is Unix-only.
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("key file too open: %v", info.Mode().Perm())
		}
	}
}

func TestSignVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "device.key")
	priv, pub, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := PublicBase64(pub)
	msg := RegisterMessage("pc-1", "nonce")
	sig := SignMessage(priv, msg)
	if !VerifyMessage(pubB64, msg, sig) {
		t.Fatal("valid signature rejected")
	}
	if VerifyMessage(pubB64, msg+"x", sig) {
		t.Fatal("tampered message accepted")
	}
}
