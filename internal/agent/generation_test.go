package agent

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

func TestAsyncExecReplyStaysOnDispatchConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	newEndpoint := func() (*wsconn.Conn, <-chan protocol.Message, func()) {
		t.Helper()
		messages := make(chan protocol.Message, 4)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer raw.Close()
			for {
				var msg protocol.Message
				if err := raw.ReadJSON(&msg); err != nil {
					return
				}
				messages <- msg
			}
		}))
		raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		return wsconn.New(raw), messages, func() { _ = raw.Close(); srv.Close() }
	}
	oldConn, oldMessages, closeOld := newEndpoint()
	defer closeOld()
	_, newMessages, closeNew := newEndpoint()
	defer closeNew()

	c := NewClient("ws://unused", "token", "pc-test")
	c.handleMessage(oldConn, protocol.Message{Type: protocol.TypeExec, RequestID: "old-request", Command: "echo bound"})
	select {
	case msg := <-oldMessages:
		if msg.Type != protocol.TypeResult || msg.RequestID != "old-request" {
			t.Fatalf("unexpected old-connection reply: %#v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for old-connection result")
	}
	select {
	case msg := <-newMessages:
		t.Fatalf("old request leaked to new connection: %#v", msg)
	case <-time.After(150 * time.Millisecond):
	}
}
