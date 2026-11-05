package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/tlspin"
	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	heartbeatInterval = 25 * time.Second // shorter than typical NAT idle (30-60s)
	peerReadTimeout   = 90 * time.Second
)

// Client is the CLI controller client.
type Client struct {
	serverURL string
	token     string
	tlsPin    string
	rawConn   *websocket.Conn
	conn      *wsconn.Conn
	stopHB    chan struct{}
	closeOnce sync.Once

	// bypassProxy dials the relay directly (see the matching field on
	// the agent client: desktop installs inherit Clash-style proxy envs
	// that break wss to the domestic relay).
	bypassProxy bool
}

// SetBypassProxy makes the client ignore HTTP_PROXY/HTTPS_PROXY.
// SetTLSPin pins the relay leaf certificate (SHA-256 hex; empty = legacy).
func (c *Client) SetTLSPin(pin string) {
	c.tlsPin = pin
}

func (c *Client) SetBypassProxy(v bool) { c.bypassProxy = v }

// NewClient creates a new CLI client.
func NewClient(serverURL, token string) *Client {
	return &Client{
		serverURL: serverURL,
		token:     token,
	}
}

// Connect establishes a WebSocket connection and authenticates.
func (c *Client) Connect() error {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	if c.bypassProxy {
		dialer.Proxy = nil
	}
	if strings.HasPrefix(c.serverURL, "wss://") {
		dialer.TLSClientConfig = tlspin.Config(c.tlsPin)
	}
	rawConn, _, err := dialer.Dial(c.serverURL, nil)
	if err != nil {
		return fmt.Errorf("connect failed: %w", err)
	}
	rawConn.SetReadLimit(protocol.MaxReadMessageSize)
	_ = rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	c.rawConn = rawConn
	c.conn = wsconn.New(rawConn)

	// Authenticate
	if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypeAuth, Token: c.token}); err != nil {
		rawConn.Close()
		return fmt.Errorf("auth send failed: %w", err)
	}

	var resp protocol.Message
	if err := rawConn.ReadJSON(&resp); err != nil {
		rawConn.Close()
		return fmt.Errorf("auth read failed: %w", err)
	}
	if !resp.Success {
		rawConn.Close()
		return fmt.Errorf("auth failed: %s", resp.Error)
	}

	// Start a heartbeat goroutine so consumer-grade NATs (which often
	// drop idle ws after 30-60s) keep the connection alive even when
	// the controller is just sitting in waitForResponse waiting for an
	// agent-side command to finish. Server treats heartbeats as no-ops
	// for controllers — we don't actually need pongs back, the bytes
	// on the wire are what matters.
	c.stopHB = make(chan struct{})
	go c.heartbeat(c.stopHB, heartbeatInterval)

	return nil
}

// Close closes the connection and stops the heartbeat goroutine.
// Safe to call multiple times — Close from defers in CLI commands and
// from error paths in Connect both end up here.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.stopHB != nil {
			close(c.stopHB)
		}
		if c.rawConn != nil {
			c.rawConn.Close()
		}
	})
}

func (c *Client) heartbeat(stop <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			// Closing the raw connection wakes an in-flight ReadMessage
			// immediately. Without this, a one-way dead connection can make
			// a command wait until its five-minute read deadline.
			if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypeHeartbeat}); err != nil {
				c.Close()
				return
			}
		}
	}
}

// ListDevices returns the list of online devices.
func (c *Client) ListDevices() ([]protocol.DeviceInfo, error) {
	if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypeListDevices}); err != nil {
		return nil, err
	}

	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		var resp protocol.Message
		if err := c.rawConn.ReadJSON(&resp); err != nil {
			return nil, err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		// Skip pongs that the heartbeat goroutine triggered — they're
		// noise from the perspective of this caller.
		if resp.Type == protocol.TypePong {
			continue
		}
		return resp.Devices, nil
	}
}

// Exec executes a command on a remote device and returns the result.
// Equivalent to ExecOpts with default options (cmd shell, no stream).
func (c *Client) Exec(deviceID, command string) (*protocol.Message, error) {
	return c.ExecOpts(deviceID, command, ExecOptions{})
}

// ExecOptions configures a single exec call.  Shell selects the runner
// on Windows ("powershell" to dodge cmd.exe interception by 360 /
// 腾讯电脑管家); empty falls back to v1 default (cmd /S /C).  See
// internal/agent/executor_windows.go for the supported values.
type ExecOptions struct {
	Shell string
}

// ExecOpts is the configurable form of Exec.  Buffered (non-streaming)
// — for streaming use ExecStream.
func (c *Client) ExecOpts(deviceID, command string, opts ExecOptions) (*protocol.Message, error) {
	reqID := uuid.New().String()[:12]

	msg := protocol.Message{
		Type:      protocol.TypeExec,
		DeviceID:  deviceID,
		Command:   command,
		RequestID: reqID,
		Shell:     opts.Shell,
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		return nil, err
	}

	return c.waitForResponse(reqID, protocol.TypeResult)
}

// StreamEvent is one frame of streaming exec output.  Final=true means
// the command has exited; ExitCode is set on the final event only.
// Stdout/Stderr carry whichever pipe the chunk arrived on; on the final
// event they may also include any trailing buffered output.
type StreamEvent struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Final    bool
	Err      error
}

// ExecStream starts a streaming command on the remote device.  Chunks
// are delivered on the returned channel as they arrive; the channel
// closes after the final event (Final=true) is delivered.  cancel()
// kills the remote process via TypeExecCancel.
func (c *Client) ExecStream(deviceID, command string, opts ExecOptions) (
	reqID string,
	events <-chan StreamEvent,
	cancel func() error,
	err error,
) {
	reqID = uuid.New().String()[:12]

	msg := protocol.Message{
		Type:      protocol.TypeExec,
		DeviceID:  deviceID,
		Command:   command,
		RequestID: reqID,
		Shell:     opts.Shell,
		Streaming: true,
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		return "", nil, nil, err
	}

	out := make(chan StreamEvent, 32)
	go c.pumpExecStream(reqID, out)

	cancel = func() error {
		return c.conn.WriteJSON(protocol.Message{
			Type:      protocol.TypeExecCancel,
			DeviceID:  deviceID,
			RequestID: reqID,
		})
	}

	return reqID, out, cancel, nil
}

// pumpExecStream reads from the wire until the final TypeResult arrives,
// emitting one StreamEvent per chunk, then closing the channel.
func (c *Client) pumpExecStream(reqID string, out chan<- StreamEvent) {
	defer close(out)
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		_, data, err := c.rawConn.ReadMessage()
		if err != nil {
			out <- StreamEvent{Err: err, Final: true}
			return
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))

		var resp protocol.Message
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.Type == protocol.TypePong {
			continue
		}
		if resp.RequestID != reqID {
			continue
		}

		switch resp.Type {
		case protocol.TypeExecChunk:
			out <- StreamEvent{Stdout: resp.Stdout, Stderr: resp.Stderr}
		case protocol.TypeResult:
			out <- StreamEvent{
				Stdout:   resp.Stdout,
				Stderr:   resp.Stderr,
				ExitCode: resp.ExitCode,
				Final:    true,
			}
			return
		}
	}
}

// CancelExec sends a TypeExecCancel for a previously-issued ExecStream.
// Best-effort — the matching TypeResult delivered through the stream
// channel is the source of truth for "did it actually stop".
func (c *Client) CancelExec(deviceID, requestID string) error {
	return c.conn.WriteJSON(protocol.Message{
		Type:      protocol.TypeExecCancel,
		DeviceID:  deviceID,
		RequestID: requestID,
	})
}

// PushFile pushes a local file to a remote device.
func (c *Client) PushFile(deviceID, localPath, remotePath string) (*protocol.Message, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("read local file: %w", err)
	}

	if int64(len(data)) > protocol.MaxPushFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d)", len(data), protocol.MaxPushFileSize)
	}

	reqID := uuid.New().String()[:12]

	msg := protocol.Message{
		Type:      protocol.TypeFilePush,
		DeviceID:  deviceID,
		RequestID: reqID,
		FileName:  filepath.Base(localPath),
		FilePath:  remotePath,
		FileData:  base64.StdEncoding.EncodeToString(data),
		FileSize:  int64(len(data)),
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		return nil, err
	}

	return c.waitForResponse(reqID, protocol.TypeFilePushAck)
}

// PullProgress is invoked once per chunk written to the local file
// during a streaming pull.  totalBytes is the file's full size as
// reported by the agent's first chunk (0 until the first chunk
// arrives).  written is the cumulative byte count written so far.
type PullProgress func(written, totalBytes int64)

// PullFile downloads a remote file to localPath, streaming chunks from
// the agent and writing them in order.  Returns the total bytes written.
// Equivalent to PullFileWithProgress with a no-op progress callback.
func (c *Client) PullFile(deviceID, remotePath, localPath string) (int64, error) {
	return c.PullFileWithProgress(deviceID, remotePath, localPath, nil)
}

// PullFileWithProgress is the configurable form of PullFile, calling
// onProgress once per chunk written.  Useful for CLI progress bars or
// programmatic monitoring of long downloads.
func (c *Client) PullFileWithProgress(deviceID, remotePath, localPath string, onProgress PullProgress) (int64, error) {
	reqID := uuid.New().String()[:12]

	msg := protocol.Message{
		Type:      protocol.TypeFilePull,
		DeviceID:  deviceID,
		FilePath:  remotePath,
		RequestID: reqID,
	}
	if err := c.conn.WriteJSON(msg); err != nil {
		return 0, err
	}

	// Buffer chunks by Seq so out-of-order delivery (shouldn't happen
	// in practice over a single ws conn, but cheap insurance) doesn't
	// corrupt the file.
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return 0, fmt.Errorf("mkdir local: %w", err)
	}
	f, err := os.Create(localPath)
	if err != nil {
		return 0, fmt.Errorf("create local: %w", err)
	}
	defer f.Close()

	pending := map[int][]byte{}
	nextSeq := 0
	var written int64
	var totalBytes int64
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))

	for {
		_, data, err := c.rawConn.ReadMessage()
		if err != nil {
			return written, err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		var resp protocol.Message
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.Type == protocol.TypePong {
			continue
		}
		if resp.Type != protocol.TypeFilePullChunk || resp.RequestID != reqID {
			continue
		}

		if resp.Error != "" {
			return written, fmt.Errorf("agent: %s", resp.Error)
		}

		// Agent stamps the file's full size on every chunk, but the
		// first chunk is what we cache for the progress callback.
		if totalBytes == 0 && resp.FileSize > 0 {
			totalBytes = resp.FileSize
		}

		decoded, err := base64.StdEncoding.DecodeString(resp.FileData)
		if err != nil {
			return written, fmt.Errorf("decode chunk %d: %w", resp.Seq, err)
		}
		pending[resp.Seq] = decoded

		// Drain in-order chunks.
		drained := false
		for {
			chunk, ok := pending[nextSeq]
			if !ok {
				break
			}
			delete(pending, nextSeq)
			n, werr := f.Write(chunk)
			if werr != nil {
				return written, fmt.Errorf("write local: %w", werr)
			}
			written += int64(n)
			drained = true
			if written > protocol.MaxPullFileSize {
				return written, fmt.Errorf("pull exceeded MaxPullFileSize (%d)", protocol.MaxPullFileSize)
			}
			nextSeq++
		}
		if drained && onProgress != nil {
			onProgress(written, totalBytes)
		}

		if resp.EOF {
			if len(pending) > 0 {
				return written, fmt.Errorf("pull ended with %d out-of-order chunks still buffered", len(pending))
			}
			return written, nil
		}
	}
}

// IssuePairCode asks the relay for a one-time enrollment code.
func (c *Client) IssuePairCode() (string, time.Time, error) {
	if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypePairIssue}); err != nil {
		return "", time.Time{}, err
	}
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		var resp protocol.Message
		if err := c.rawConn.ReadJSON(&resp); err != nil {
			return "", time.Time{}, err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		switch resp.Type {
		case protocol.TypePairCode:
			return resp.Code, time.Unix(resp.ExpiresAtUnix, 0), nil
		case protocol.TypePong:
			continue
		default:
			return "", time.Time{}, fmt.Errorf("unexpected reply: %s (%s)", resp.Type, resp.Error)
		}
	}
}

// ApproveEnrollment approves an employee-initiated enrollment code.
func (c *Client) ApproveEnrollment(code string) (string, string, error) {
	if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypeEnrollApprove, Code: code}); err != nil {
		return "", "", err
	}
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		var resp protocol.Message
		if err := c.rawConn.ReadJSON(&resp); err != nil {
			return "", "", err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		switch resp.Type {
		case protocol.TypeEnrollResult:
			if !resp.Success {
				return "", "", fmt.Errorf("enrollment approval failed: %s", resp.Error)
			}
			return resp.DeviceID, resp.KeyFingerprint, nil
		case protocol.TypePong:
			continue
		default:
			return "", "", fmt.Errorf("unexpected reply: %s (%s)", resp.Type, resp.Error)
		}
	}
}

// RevokeDevice removes a device's bound key and disconnects it.
func (c *Client) RevokeDevice(deviceID string) error {
	if err := c.conn.WriteJSON(protocol.Message{Type: protocol.TypeRevokeDevice, DeviceID: deviceID}); err != nil {
		return err
	}
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		var resp protocol.Message
		if err := c.rawConn.ReadJSON(&resp); err != nil {
			return err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		switch resp.Type {
		case protocol.TypeRegisterResult:
			if !resp.Success {
				return fmt.Errorf("revoke failed: %s", resp.Error)
			}
			return nil
		case protocol.TypePong:
			continue
		default:
			return fmt.Errorf("unexpected reply: %s (%s)", resp.Type, resp.Error)
		}
	}
}

func (c *Client) waitForResponse(reqID, expectType string) (*protocol.Message, error) {
	_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	for {
		_, data, err := c.rawConn.ReadMessage()
		if err != nil {
			return nil, err
		}
		_ = c.rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))

		var resp protocol.Message
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}

		// Heartbeat pong from the server — ignore and keep waiting.
		if resp.Type == protocol.TypePong {
			continue
		}
		if resp.Type == expectType && resp.RequestID == reqID {
			return &resp, nil
		}
	}
}
