// Package wsconn wraps a *websocket.Conn so that concurrent writers don't
// corrupt frames.
//
// gorilla/websocket explicitly states: "Connections support one concurrent
// reader and one concurrent writer. Applications are responsible for
// ensuring that no more than one goroutine calls the write methods
// (WriteMessage, WriteJSON, NextWriter) concurrently".
//
// In remote-agent the agent client has at minimum two writers (heartbeat
// goroutine + result responder), and the server has even more (every
// controller's incoming exec triggers a write to the target device's
// conn from the controller's read goroutine). Without serialization this
// produces interleaved frames or panics on the wire.
package wsconn

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var writeWait = 30 * time.Second

// Conn is a thread-safe wrapper around *websocket.Conn for writes.
// Reads stay on the underlying connection — gorilla allows one concurrent
// reader and one concurrent writer, so single-goroutine read is fine and
// doesn't need locking here.
type Conn struct {
	*websocket.Conn
	writeMu sync.Mutex
}

// New wraps a raw *websocket.Conn.
func New(c *websocket.Conn) *Conn {
	return &Conn{Conn: c}
}

// WriteJSON writes a JSON-encoded message under the write lock.
func (c *Conn) WriteJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.Conn.WriteJSON(v)
}

// WriteMessage writes a low-level frame under the write lock.
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.Conn.WriteMessage(messageType, data)
}
