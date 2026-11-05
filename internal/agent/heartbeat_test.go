package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/gorilla/websocket"
)

func TestStaleHeartbeatDoesNotTouchReconnectedSocket(t *testing.T) {
	upgrader := websocket.Upgrader{}
	staleClosed := make(chan struct{})
	staleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("stale upgrade: %v", err)
			return
		}
		_ = raw.Close()
		close(staleClosed)
	}))
	defer staleServer.Close()

	liveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("live upgrade: %v", err)
			return
		}
		defer raw.Close()
		for {
			if _, _, err := raw.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer liveServer.Close()

	dial := func(url string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	stale := wsconn.New(dial(staleServer.URL))
	defer stale.Close()
	<-staleClosed
	live := wsconn.New(dial(liveServer.URL))
	defer live.Close()

	c := NewClient("ws://unused", "token", "pc-test")
	done := make(chan struct{})
	go func() { c.heartbeat(stale, make(chan struct{}), time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale heartbeat did not stop after its connection closed")
	}
	if err := live.WriteMessage(websocket.TextMessage, []byte("still-connected")); err != nil {
		t.Fatalf("stale heartbeat closed the reconnected socket: %v", err)
	}
}

func TestHeartbeatStopsWithoutWaitingForNextTick(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	c := NewClient("ws://unused", "token", "pc-test")
	go func() {
		defer close(done)
		c.heartbeat(nil, stop, time.Hour)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("heartbeat did not stop promptly")
	}
}
