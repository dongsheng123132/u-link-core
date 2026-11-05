// Package ulink holds the CLI-first personal device fabric: config,
// identity, audit, remote execution, and the MCP surface.
package ulink

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the on-disk CLI configuration.  It deliberately does not need
// an account: a relay URL and a controller token are enough to talk to a
// self-hosted relay.  Device identity (pairing) lives next to it.
type Config struct {
	Relay           string `json:"relay"`
	ControllerToken string `json:"controller_token"`
	DeviceID        string `json:"device_id,omitempty"`
	AuditPath       string `json:"audit_path,omitempty"`
	TLSPin          string `json:"tls_pin,omitempty"`

	// ConfigDir is resolved at load time and never persisted.
	ConfigDir string `json:"-"`
}

// HomeDir resolves the U-Link CLI state directory.
// ULINK_HOME overrides it; default is ~/.ulink.
func HomeDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ULINK_HOME")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ulink"), nil
}

// Load reads the CLI config, falling back to the existing U-Link desktop
// config for relay/controller token so the CLI can be tried without a
// second setup step.
func Load() (*Config, error) {
	dir, err := HomeDir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// LoadFrom reads the CLI config from an explicit directory.
func LoadFrom(dir string) (*Config, error) {
	cfg := &Config{ConfigDir: dir}

	if data, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", filepath.Join(dir, "config.json"), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// Fallback: reuse the desktop U-Link config when the CLI is empty.
	if cfg.Relay == "" || cfg.ControllerToken == "" {
		if legacy, ok := readLegacyConfig(); ok {
			if cfg.Relay == "" {
				cfg.Relay = legacy.Relay
			}
			if cfg.ControllerToken == "" {
				cfg.ControllerToken = legacy.ControllerToken
			}
		}
	}

	if v := os.Getenv("ULINK_RELAY"); v != "" {
		cfg.Relay = v
	} else if v := os.Getenv("REMOTE_AGENT_SERVER"); v != "" && cfg.Relay == "" {
		cfg.Relay = v
	}
	if v := os.Getenv("ULINK_TOKEN"); v != "" {
		cfg.ControllerToken = v
	} else if v := os.Getenv("REMOTE_AGENT_TOKEN"); v != "" && cfg.ControllerToken == "" {
		cfg.ControllerToken = v
	}
	if v := os.Getenv("ULINK_TLS_PIN"); v != "" {
		cfg.TLSPin = v
	}
	cfg.Relay = strings.TrimSpace(cfg.Relay)
	cfg.ControllerToken = strings.TrimSpace(cfg.ControllerToken)

	if cfg.AuditPath == "" {
		cfg.AuditPath = filepath.Join(dir, "audit.jsonl")
	}
	return cfg, nil
}

// EnsureConnectedConfig validates the minimum needed to dial the relay.
func (c *Config) EnsureConnectedConfig() error {
	if c.Relay == "" {
		return errors.New("relay is not configured: pass --relay, set ULINK_RELAY, or create ~/.ulink/config.json")
	}
	if c.ControllerToken == "" {
		return errors.New("controller token is not configured: pass --token, set ULINK_TOKEN, or reuse the U-Link desktop config")
	}
	return nil
}

type legacyConfig struct {
	Relay           string `json:"relay"`
	ControllerToken string `json:"controller_token"`
}

func readLegacyConfig() (legacyConfig, bool) {
	var path string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return legacyConfig{}, false
		}
		path = filepath.Join(home, "Library", "Application Support", "U-Link", "config.json")
	case "windows":
		base, err := os.UserConfigDir()
		if err != nil {
			return legacyConfig{}, false
		}
		path = filepath.Join(base, "U-Link", "config.json")
	default:
		base, err := os.UserConfigDir()
		if err != nil {
			return legacyConfig{}, false
		}
		path = filepath.Join(base, "u-link", "config.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return legacyConfig{}, false
	}
	var lc legacyConfig
	if json.Unmarshal(data, &lc) != nil {
		return legacyConfig{}, false
	}
	return lc, lc.Relay != "" || lc.ControllerToken != ""
}
