// Package devicekey manages the Ed25519 key that proves a device owns its
// device ID.  The private key never leaves the machine; the relay only
// stores the public key and checks a signature over a per-connection nonce.
package devicekey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
)

// LoadOrCreate loads an Ed25519 private key from path, creating a new one
// with 0600 permissions (and 0700 parent) when it does not exist.
func LoadOrCreate(path string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if path == "" {
		return nil, nil, errors.New("device key path is empty")
	}
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, nil, errors.New("device key file is not valid PEM")
		}
		priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, err
		}
		ed, ok := priv.(ed25519.PrivateKey)
		if !ok {
			return nil, nil, errors.New("device key is not Ed25519")
		}
		return ed, ed.Public().(ed25519.PublicKey), nil
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// PublicBase64 returns the raw Ed25519 public key as base64.
func PublicBase64(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePublic decodes a raw base64 Ed25519 public key.
func DecodePublic(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be 32 bytes")
	}
	return ed25519.PublicKey(raw), nil
}

// Fingerprint returns the full hex SHA-256 of the public key.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// SignMessage signs a UTF-8 message and returns base64 raw signature.
func SignMessage(priv ed25519.PrivateKey, message string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(message)))
}

// VerifyMessage verifies a base64 raw signature over message.
func VerifyMessage(pubB64, message, sigB64 string) bool {
	pub, err := DecodePublic(pubB64)
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, []byte(message), sig)
}

// RegisterMessage is the exact string a device signs after the relay sends
// its nonce.  Keep this stable: it is a wire contract.
func RegisterMessage(deviceID, nonce string) string {
	return "ulink-register\n" + deviceID + "\n" + nonce
}
