package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/devicekey"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/gorilla/websocket"
)

func startAgentServer(t *testing.T, hub *Hub) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", NewHandler(hub).HandleAgent)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/agent"
}

func dialAgent(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	raw, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if err := raw.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: "agent"}); err != nil {
		t.Fatal(err)
	}
	if got := readType(t, raw, protocol.TypeAuthResult); !got.Success {
		t.Fatalf("auth failed: %s", got.Error)
	}
	return raw
}

func readType(t *testing.T, raw *websocket.Conn, want string) protocol.Message {
	t.Helper()
	_ = raw.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg protocol.Message
		if err := raw.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s: %v", want, err)
		}
		if msg.Type == want {
			return msg
		}
	}
}

func register(t *testing.T, raw *websocket.Conn, deviceID, pubB64, code string) protocol.Message {
	t.Helper()
	if err := raw.WriteJSON(protocol.Message{
		Type:        protocol.TypeRegister,
		DeviceID:    deviceID,
		Hostname:    "host",
		OS:          "linux",
		Version:     "test",
		PublicKey:   pubB64,
		PairingCode: code,
	}); err != nil {
		t.Fatal(err)
	}
	return readType(t, raw, protocol.TypeRegisterResult)
}

func TestIdentityEnforcementFlow(t *testing.T) {
	hub := NewHub("agent", "controller")
	hub.SetRequireDeviceKeys(true)
	url := startAgentServer(t, hub)

	priv, pub, err := devicekey.LoadOrCreate(filepath.Join(t.TempDir(), "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := devicekey.PublicBase64(pub)

	// 1) New device without a pairing code is rejected.
	raw := dialAgent(t, url)
	if got := register(t, raw, "pc-1", pubB64, ""); got.Success {
		t.Fatal("registration without pairing code should fail")
	}

	// 2) Issue a code, register, prove possession, and check binding.
	code, _ := hub.IssuePairCode()
	raw = dialAgent(t, url)
	if err := raw.WriteJSON(protocol.Message{
		Type: protocol.TypeRegister, DeviceID: "pc-1", Hostname: "host",
		OS: "linux", Version: "test", PublicKey: pubB64, PairingCode: code,
	}); err != nil {
		t.Fatal(err)
	}
	challenge := readType(t, raw, protocol.TypeRegisterChallenge)
	sig := devicekey.SignMessage(priv, devicekey.RegisterMessage("pc-1", challenge.Nonce))
	if err := raw.WriteJSON(protocol.Message{
		Type: protocol.TypeRegisterProof, DeviceID: "pc-1",
		PublicKey: pubB64, Nonce: challenge.Nonce, Signature: sig,
	}); err != nil {
		t.Fatal(err)
	}
	if got := readType(t, raw, protocol.TypeRegisterResult); !got.Success {
		t.Fatalf("registration failed: %s", got.Error)
	}
	if key, ok := hub.DeviceKey("pc-1"); !ok || key != pubB64 {
		t.Fatalf("key not bound: %q %v", key, ok)
	}

	// 3) Same key reconnects without a code: challenge + proof only.
	raw = dialAgent(t, url)
	if err := raw.WriteJSON(protocol.Message{
		Type: protocol.TypeRegister, DeviceID: "pc-1", Hostname: "host",
		OS: "linux", Version: "test", PublicKey: pubB64,
	}); err != nil {
		t.Fatal(err)
	}
	challenge = readType(t, raw, protocol.TypeRegisterChallenge)
	sig = devicekey.SignMessage(priv, devicekey.RegisterMessage("pc-1", challenge.Nonce))
	if err := raw.WriteJSON(protocol.Message{
		Type: protocol.TypeRegisterProof, DeviceID: "pc-1",
		PublicKey: pubB64, Nonce: challenge.Nonce, Signature: sig,
	}); err != nil {
		t.Fatal(err)
	}
	if got := readType(t, raw, protocol.TypeRegisterResult); !got.Success {
		t.Fatalf("known device re-register failed: %s", got.Error)
	}

	// 4) A different key claiming the same device ID is rejected.
	_, otherPub, err := devicekey.LoadOrCreate(filepath.Join(t.TempDir(), "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	raw = dialAgent(t, url)
	if got := register(t, raw, "pc-1", devicekey.PublicBase64(otherPub), ""); got.Success {
		t.Fatal("different key must not take over a bound device ID")
	}
}

func TestEmployeeInitiatedEnrollment(t *testing.T) {
	hub := NewHub("agent", "controller")
	hub.SetRequireDeviceKeys(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", NewHandler(hub).HandleAgent)
	mux.HandleFunc("/ws/controller", NewHandler(hub).HandleController)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	_, pub, err := devicekey.LoadOrCreate(filepath.Join(t.TempDir(), "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := devicekey.PublicBase64(pub)

	agentConn, _, err := websocket.DefaultDialer.Dial(wsBase+"/ws/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer agentConn.Close()
	if err := agentConn.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: "agent"}); err != nil {
		t.Fatal(err)
	}
	readType(t, agentConn, protocol.TypeAuthResult)
	if err := agentConn.WriteJSON(protocol.Message{
		Type: protocol.TypeRegister, DeviceID: "pc-emp", Hostname: "emp",
		OS: "windows", Version: "test", PublicKey: pubB64, RequestEnrollment: true,
	}); err != nil {
		t.Fatal(err)
	}
	enroll := readType(t, agentConn, protocol.TypeEnrollCode)
	if enroll.Code == "" {
		t.Fatal("empty enrollment code")
	}

	ctrl, _, err := websocket.DefaultDialer.Dial(wsBase+"/ws/controller", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	if err := ctrl.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: "controller"}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctrl, protocol.TypeAuthResult)
	if err := ctrl.WriteJSON(protocol.Message{Type: protocol.TypeEnrollApprove, Code: enroll.Code}); err != nil {
		t.Fatal(err)
	}
	if got := readType(t, ctrl, protocol.TypeEnrollResult); !got.Success {
		t.Fatalf("approve failed: %s", got.Error)
	}
	if got := readType(t, agentConn, protocol.TypeEnrollApproved); got.DeviceID != "pc-emp" {
		t.Fatalf("unexpected approval: %+v", got)
	}
	if key, ok := hub.DeviceKey("pc-emp"); !ok || key != pubB64 {
		t.Fatalf("key not bound after enrollment: %q %v", key, ok)
	}
	devices := hub.GetDeviceList()
	if len(devices) != 1 || devices[0].ID != "pc-emp" {
		t.Fatalf("device not registered: %+v", devices)
	}
}
