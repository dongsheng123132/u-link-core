package ulink

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Identity is this machine's local CLI identity.  v0.1 uses it as the
// pairing scaffold; relay-side enforcement is the next milestone.
type Identity struct {
	DeviceID    string    `json:"device_id"`
	PublicKey   string    `json:"public_key"` // base64 raw Ed25519
	Fingerprint string    `json:"fingerprint"`
	ShortCode   string    `json:"short_code"`
	CreatedAt   time.Time `json:"created_at"`
}

// TrustedDevice is one approved peer key.
type TrustedDevice struct {
	Name        string    `json:"name,omitempty"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	AddedAt     time.Time `json:"added_at"`
}

// EnsureIdentity loads or creates this machine's Ed25519 identity.
func EnsureIdentity(dir string) (*Identity, error) {
	if id, err := LoadIdentity(dir); err == nil && id != nil {
		return id, nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "device.key"), keyPEM, 0o600); err != nil {
		return nil, err
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub)
	fp := Fingerprint(pub)
	id := &Identity{
		DeviceID:    "ulink-" + strings.ToLower(fp[:6]),
		PublicKey:   pubB64,
		Fingerprint: fp,
		ShortCode:   ShortCode(fp),
		CreatedAt:   time.Now().UTC(),
	}
	if err := saveJSON(filepath.Join(dir, "identity.json"), id, 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

// LoadIdentity returns nil, nil when no identity exists yet.
func LoadIdentity(dir string) (*Identity, error) {
	data, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, err
	}
	if id.PublicKey == "" || id.Fingerprint == "" {
		return nil, fmt.Errorf("identity.json is incomplete; delete it and rerun `ulink pair`")
	}
	return &id, nil
}

// Fingerprint returns the full hex SHA-256 of a raw Ed25519 public key.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// ShortCode is the 6-character pairing code derived from a fingerprint.
func ShortCode(fp string) string {
	fp = strings.ToUpper(strings.TrimSpace(fp))
	if len(fp) < 6 {
		return fp
	}
	return fp[:6]
}

// LoadTrusted reads the local trust list.
func LoadTrusted(dir string) ([]TrustedDevice, error) {
	var out []TrustedDevice
	data, err := os.ReadFile(filepath.Join(dir, "trusted.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TrustKey appends a peer public key (base64 raw Ed25519) to the trust list.
func TrustKey(dir, name, pubB64 string) (*TrustedDevice, error) {
	pubB64 = strings.TrimSpace(pubB64)
	raw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be base64 raw Ed25519 (32 bytes)")
	}
	fp := Fingerprint(ed25519.PublicKey(raw))
	trusted, err := LoadTrusted(dir)
	if err != nil {
		return nil, err
	}
	for _, t := range trusted {
		if t.Fingerprint == fp {
			return &t, nil
		}
	}
	entry := TrustedDevice{
		Name:        strings.TrimSpace(name),
		PublicKey:   pubB64,
		Fingerprint: fp,
		AddedAt:     time.Now().UTC(),
	}
	trusted = append(trusted, entry)
	if err := saveJSON(filepath.Join(dir, "trusted.json"), trusted, 0o600); err != nil {
		return nil, err
	}
	return &entry, nil
}

func saveJSON(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, mode)
}
