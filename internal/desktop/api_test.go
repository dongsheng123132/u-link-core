package desktop

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "test-local-token-123"

func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg := DefaultConfig()
	// Public builds ship unconfigured, so the fixture supplies its own
	// relay and tokens rather than relying on built-in defaults.
	cfg.Relay = "wss://relay.example.com:8900"
	cfg.AgentToken = "agent-secret"
	cfg.LocalToken = testToken
	cfg.DeviceID = "mac-test"
	cfg.ControllerToken = "controller-secret"
	ring := NewRing(20)
	return &App{
		cfg:         *cfg,
		ring:        ring,
		startedAt:   time.Now(),
		execCancels: make(map[string]func()),
		quitCh:      make(chan struct{}),
		agentState:  "online",
		trayActive:  true,
	}
}

// startTestServer binds the real routes on an ephemeral loopback port so
// the Host/Origin checks see exactly what a browser would send.
func startTestServer(t *testing.T, a *App) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	a.port = port
	srv := &http.Server{Handler: a.routes()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func doRequest(t *testing.T, method, url string, body string, headers map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAPIRequiresToken(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	code, _ := doRequest(t, http.MethodGet, base+"/api/status", "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", code)
	}
	code, _ = doRequest(t, http.MethodGet, base+"/api/status", "", map[string]string{"X-ULink-Token": "wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", code)
	}
	code, body := doRequest(t, http.MethodGet, base+"/api/status", "", map[string]string{"X-ULink-Token": testToken})
	if code != http.StatusOK {
		t.Fatalf("valid token: status = %d body=%s", code, body)
	}
}

func TestAPIRejectsForeignOriginAndHost(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	code, _ := doRequest(t, http.MethodGet, base+"/api/status", "", map[string]string{
		"X-ULink-Token": testToken,
		"Origin":        "http://evil.example",
	})
	if code != http.StatusForbidden {
		t.Fatalf("foreign origin: status = %d, want 403", code)
	}

	// DNS rebinding: valid token but Host header points at an attacker name.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/status", nil)
	req.Host = "evil.example"
	req.Header.Set("X-ULink-Token", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: status = %d, want 403", resp.StatusCode)
	}
}

func TestStatusShape(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	_, body := doRequest(t, http.MethodGet, base+"/api/status", "", map[string]string{"X-ULink-Token": testToken})
	var status Status
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status: %v body=%s", err, body)
	}
	if status.Agent.DeviceID != "mac-test" {
		t.Fatalf("device id = %q", status.Agent.DeviceID)
	}
	if status.Agent.State != "online" {
		t.Fatalf("state = %q, want online", status.Agent.State)
	}
	if !status.Agent.AllowControl {
		t.Fatal("allow_control should be true")
	}
	if status.Port == 0 || status.Tray != true {
		t.Fatalf("unexpected port/tray: %d/%v", status.Port, status.Tray)
	}
}

func TestSettingsNeverEchoTokens(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	_, body := doRequest(t, http.MethodGet, base+"/api/settings", "", map[string]string{"X-ULink-Token": testToken})
	text := string(body)
	if strings.Contains(text, "controller-secret") || strings.Contains(text, testToken) {
		t.Fatalf("settings leaked a token: %s", text)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["controller_token_set"] != true || payload["agent_token_set"] != true {
		t.Fatalf("token-set flags missing: %s", text)
	}
	if _, ok := payload["controller_token"]; ok {
		t.Fatal("controller_token key must not exist")
	}
}

func TestSettingsAcceptsTLSPin(t *testing.T) {
	a := newTestApp(t)
	a.cfgPath = filepath.Join(t.TempDir(), "config.json")
	base := startTestServer(t, a)
	code, body := doRequest(t, http.MethodPost, base+"/api/settings",
		`{"tls_pin":"AA:BB:CC"}`,
		map[string]string{"X-ULink-Token": testToken, "Content-Type": "application/json"})
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
	if a.snapshotConfig().TLSPin != "AA:BB:CC" {
		t.Fatalf("tls_pin not stored: %q", a.snapshotConfig().TLSPin)
	}
	_, got := doRequest(t, http.MethodGet, base+"/api/settings", "", map[string]string{"X-ULink-Token": testToken})
	if !strings.Contains(string(got), "AA:BB:CC") {
		t.Fatalf("tls_pin not returned to the dashboard: %s", got)
	}
}

func TestExecWithoutControllerTokenIsRejected(t *testing.T) {
	a := newTestApp(t)
	a.cfg.ControllerToken = ""
	base := startTestServer(t, a)
	code, body := doRequest(t, http.MethodPost, base+"/api/exec",
		`{"device":"pc-1","command":"hostname"}`,
		map[string]string{"X-ULink-Token": testToken, "Content-Type": "application/json"})
	if code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d body=%s, want 412", code, body)
	}
	if !strings.Contains(string(body), "控制端") {
		t.Fatalf("error should explain missing controller token: %s", body)
	}
}

func TestPingNeedsNoToken(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	code, body := doRequest(t, http.MethodGet, base+"/api/ping", "", nil)
	if code != http.StatusOK {
		t.Fatalf("ping status = %d", code)
	}
	if !strings.Contains(string(body), "u-link") {
		t.Fatalf("ping body = %s", body)
	}
}

func TestIndexServesDashboard(t *testing.T) {
	base := startTestServer(t, newTestApp(t))
	code, body := doRequest(t, http.MethodGet, base+"/", "", nil)
	if code != http.StatusOK {
		t.Fatalf("index status = %d", code)
	}
	if !strings.Contains(string(body), "U-Link") {
		t.Fatalf("dashboard payload looks wrong (%d bytes)", len(body))
	}
	if ct := http.DetectContentType(body); !strings.Contains(ct, "text/html") {
		t.Fatalf("content type sniff = %s", ct)
	}
}
