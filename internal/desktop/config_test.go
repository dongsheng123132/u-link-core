package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadConfigCreatesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DeviceID == "" || cfg.LocalToken == "" {
		t.Fatalf("defaults not generated: %+v", cfg)
	}
	if cfg.Relay != "" || cfg.AgentToken != "" {
		t.Fatalf("a fresh install must not ship a relay or token: %+v", cfg)
	}
	if !cfg.AllowControl || !cfg.ShowAllDevices {
		t.Fatalf("allow_control/show_all_devices should default to true: %+v", cfg)
	}
	if len(cfg.LocalToken) != 48 {
		t.Fatalf("local token should be 24 random bytes hex, got %d chars", len(cfg.LocalToken))
	}
	if !cfg.BypassProxy {
		t.Fatal("bypass_proxy should default to true")
	}

	// The file must be created with 0600 (the controller token lives there).
	// Windows has no POSIX permission bits, so the assertion only applies
	// to Unix; the file is still created with the same mode request there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config permissions = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestLoadConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ControllerToken = "ctl-secret"
	cfg.AllowControl = false
	cfg.DeviceID = "mac-test"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	again, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ControllerToken != "ctl-secret" || again.AllowControl || again.DeviceID != "mac-test" {
		t.Fatalf("round trip mismatch: %+v", again)
	}
	if again.LocalToken != cfg.LocalToken {
		t.Fatal("local token changed across reload")
	}
}

func TestDefaultDeviceIDStable(t *testing.T) {
	first := defaultDeviceID()
	second := defaultDeviceID()
	if first != second {
		t.Fatalf("device id should be stable: %q vs %q", first, second)
	}
	if len(first) < 6 {
		t.Fatalf("device id too short: %q", first)
	}
}

func TestConfigDirOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cfg")
	got, err := ConfigDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty config dir")
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("config dir not created: %v", err)
	}
}

func TestEndpoints(t *testing.T) {
	if got := agentEndpoint("wss://relay.example.com:8900"); got != "wss://relay.example.com:8900/ws/agent" {
		t.Fatalf("agentEndpoint = %q", got)
	}
	if got := agentEndpoint("ws://127.0.0.1:18902/"); got != "ws://127.0.0.1:18902/ws/agent" {
		t.Fatalf("agentEndpoint trailing slash = %q", got)
	}
	if got := agentEndpoint("ws://127.0.0.1:18902/ws/agent"); got != "ws://127.0.0.1:18902/ws/agent" {
		t.Fatalf("agentEndpoint should be idempotent, got %q", got)
	}
	if got := controllerEndpoint("wss://example.com:8900/"); got != "wss://example.com:8900/ws/controller" {
		t.Fatalf("controllerEndpoint = %q", got)
	}
	if got := controllerEndpoint("ws://127.0.0.1:18902/ws/controller"); got != "ws://127.0.0.1:18902/ws/controller" {
		t.Fatalf("controllerEndpoint should be idempotent, got %q", got)
	}
}
