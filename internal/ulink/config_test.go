package ulink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"relay":"wss://relay.example:8900","controller_token":"tok","device_id":"dev1"}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay != "wss://relay.example:8900" || cfg.ControllerToken != "tok" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.ConfigDir != dir {
		t.Fatalf("config dir: %s", cfg.ConfigDir)
	}
	if cfg.AuditPath == "" {
		t.Fatal("audit path should default")
	}
}

func TestLoadFromEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ULINK_RELAY", "wss://env.example:8900")
	t.Setenv("ULINK_TOKEN", "envtok")
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay != "wss://env.example:8900" || cfg.ControllerToken != "envtok" {
		t.Fatalf("env override failed: %+v", cfg)
	}
}

func TestEnsureConnectedConfig(t *testing.T) {
	if err := (&Config{}).EnsureConnectedConfig(); err == nil {
		t.Fatal("empty config should fail")
	}
	if err := (&Config{Relay: "wss://x", ControllerToken: "t"}).EnsureConnectedConfig(); err != nil {
		t.Fatalf("valid config failed: %v", err)
	}
}
