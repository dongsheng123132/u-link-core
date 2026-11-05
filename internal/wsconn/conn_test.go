package wsconn

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestConcurrentWritesDontInterleave proves that two goroutines hammering
// WriteJSON on the same Conn produce well-formed frames at the receiver,
// not interleaved garbage. Without the writeMu in Conn, gorilla writes
// race and the receiver sees malformed JSON or a closed connection.
//
// The test runs without -race because we don't have CGO on Windows;
// instead it exercises a real TCP loopback ws connection so any
// interleaving would surface as ReadMessage errors or unparseable JSON.
func TestConcurrentWritesDontInterleave(t *testing.T) {
	const writers = 8
	const perWriter = 50

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		safe := New(raw)

		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				// Pad the payload so a torn write would produce visibly
				// corrupt JSON rather than a coincidentally-valid frame.
				payload := map[string]any{
					"writer": id,
					"pad":    strings.Repeat("x", 1024),
				}
				for n := 0; n < perWriter; n++ {
					if err := safe.WriteJSON(payload); err != nil {
						t.Errorf("writer %d: WriteJSON: %v", id, err)
						return
					}
				}
			}(i)
		}
		wg.Wait()
		raw.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	want := writers * perWriter
	got := 0
	for got < want {
		var msg map[string]any
		if err := client.ReadJSON(&msg); err != nil {
			t.Fatalf("ReadJSON after %d msgs: %v", got, err)
		}
		// If frames are interleaved, ReadJSON would either error or
		// return an object missing one of our expected keys.
		if _, ok := msg["writer"]; !ok {
			t.Fatalf("msg %d missing 'writer' key: %#v", got, msg)
		}
		if _, ok := msg["pad"]; !ok {
			t.Fatalf("msg %d missing 'pad' key: %#v", got, msg)
		}
		got++
	}
}

func TestWriteUnblocksOnStalledConn(t *testing.T) {
	orig := writeWait
	writeWait = 300 * time.Millisecond
	defer func() { writeWait = orig }()

	upgraded := make(chan struct{})
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		close(upgraded)
		<-r.Context().Done()
		_ = raw
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	rawClient, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer rawClient.Close()
	<-upgraded
	client := New(rawClient)
	payload := []byte(strings.Repeat("x", 64*1024))
	done := make(chan error, 1)
	go func() {
		var lastErr error
		for i := 0; i < 10000; i++ {
			if err := client.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				lastErr = err
				break
			}
		}
		done <- lastErr
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected stalled write to return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteMessage hung on a stalled connection")
	}
}
