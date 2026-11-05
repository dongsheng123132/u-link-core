package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/gorilla/websocket"
)

func TestHeartbeatWriteFailureInterruptsResponseWait(t *testing.T) {
	upgrader := websocket.Upgrader{}
	liveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("live upgrade: %v", err)
			return
		}
		defer raw.Close()
		<-r.Context().Done()
	}))
	defer liveServer.Close()
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

	dial := func(url string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	live := dial(liveServer.URL)
	stale := dial(staleServer.URL)
	defer stale.Close()
	<-staleClosed

	c := NewClient("ws://unused", "token")
	c.rawConn = live
	c.conn = wsconn.New(stale)
	c.stopHB = make(chan struct{})
	waitDone := make(chan error, 1)
	go func() { _, err := c.waitForResponse("request", "result"); waitDone <- err }()
	go c.heartbeat(c.stopHB, time.Millisecond)
	select {
	case err := <-waitDone:
		if err == nil {
			t.Fatal("wait unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat write failure did not interrupt response wait")
	}
}
