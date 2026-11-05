package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/gorilla/websocket"
)

func TestForwardFailureRemovesFailedDevice(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer raw.Close()
		<-r.Context().Done()
	}))
	defer srv.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Closing the local WebSocket makes WriteJSON fail deterministically;
	// a peer-close alone may still accept one frame into the TCP send buffer.
	_ = raw.Close()

	hub := NewHub("agent", "controller")
	conn := wsconn.New(raw)
	hub.RegisterDevice("pc-test", "host", "windows", "test", conn)
	if err := hub.ForwardToDevice("pc-test", protocol.Message{Type: protocol.TypeExec}); err == nil {
		t.Fatal("expected forward to closed device to fail")
	}
	if devices := hub.GetDeviceList(); len(devices) != 0 {
		t.Fatalf("failed device remained online: %#v", devices)
	}
}

func TestRemovingStaleGenerationKeepsReplacement(t *testing.T) {
	hub := NewHub("agent", "controller")
	oldConn := new(wsconn.Conn)
	newConn := new(wsconn.Conn)
	hub.RegisterDevice("pc-test", "old", "windows", "test", oldConn)
	hub.RegisterDevice("pc-test", "new", "windows", "test", newConn)
	hub.RemoveDevice(oldConn)

	hub.mu.RLock()
	got := hub.devices["pc-test"]
	hub.mu.RUnlock()
	if got == nil || got.Conn != newConn || got.Hostname != "new" {
		t.Fatalf("stale removal deleted replacement: %#v", got)
	}
}

func TestBindDeviceKeyRejectsMismatch(t *testing.T) {
	hub := NewHub("agent", "controller")
	if err := hub.BindDeviceKey("pc-1", "key-a"); err != nil {
		t.Fatal(err)
	}
	if err := hub.BindDeviceKey("pc-1", "key-a"); err != nil {
		t.Fatalf("idempotent bind failed: %v", err)
	}
	if err := hub.BindDeviceKey("pc-1", "key-b"); err == nil {
		t.Fatal("expected mismatch error")
	}
	if got, ok := hub.DeviceKey("pc-1"); !ok || got != "key-a" {
		t.Fatalf("key changed: %q %v", got, ok)
	}
}

func TestPairCodeOneTime(t *testing.T) {
	hub := NewHub("agent", "controller")
	code, expires := hub.IssuePairCode()
	if len(code) != 6 || expires.Before(time.Now()) {
		t.Fatalf("bad code: %q %v", code, expires)
	}
	if !hub.ConsumePairCode(code) {
		t.Fatal("first consume should succeed")
	}
	if hub.ConsumePairCode(code) {
		t.Fatal("second consume should fail")
	}
	if hub.ConsumePairCode("000000") && code != "000000" {
		t.Fatal("unknown code accepted")
	}
}

func TestRevokeDeviceRemovesKey(t *testing.T) {
	hub := NewHub("agent", "controller")
	if err := hub.BindDeviceKey("pc-1", "key-a"); err != nil {
		t.Fatal(err)
	}
	if !hub.RevokeDevice("pc-1") {
		t.Fatal("revoke should report a change")
	}
	if _, ok := hub.DeviceKey("pc-1"); ok {
		t.Fatal("key still present after revoke")
	}
	if hub.RevokeDevice("pc-1") {
		t.Fatal("second revoke should be a no-op")
	}
}

func TestPairAttemptRateLimit(t *testing.T) {
	hub := NewHub("agent", "controller")
	for i := 0; i < 10; i++ {
		if !hub.AllowPairAttempt("203.0.113.7") {
			t.Fatalf("attempt %d should be allowed", i)
		}
		hub.NotePairFailure("203.0.113.7")
	}
	if hub.AllowPairAttempt("203.0.113.7") {
		t.Fatal("11th attempt should be blocked")
	}
	if !hub.AllowPairAttempt("203.0.113.8") {
		t.Fatal("different IP should not be blocked")
	}
	hub.ResetPairAttempts("203.0.113.7")
	if !hub.AllowPairAttempt("203.0.113.7") {
		t.Fatal("reset should allow again")
	}
}
