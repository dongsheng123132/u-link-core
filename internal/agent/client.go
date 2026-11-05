package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/devicekey"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/redact"
	"github.com/dongsheng123132/remote-agent/internal/tlspin"
	"github.com/dongsheng123132/remote-agent/internal/version"
	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/semaphore"
)

const (
	// Reconnect policy: after a healthy session reconnect fast, but back
	// off exponentially (with jitter) while the relay stays unreachable.
	// A fixed 5s retry hammered the relay during outages and made the
	// "online again within 30s" acceptance criterion flaky.
	initialReconnectDelay   = 2 * time.Second
	maxReconnectDelay       = 30 * time.Second
	healthySessionThreshold = 60 * time.Second

	heartbeatInterval = 30 * time.Second
	peerReadTimeout   = 90 * time.Second

	// maxConcurrentExec bounds how many exec messages we'll process in
	// parallel.  Picked at 8 because (a) most customer machines are
	// 4-8 core consumer hardware, (b) consumer security tools sometimes
	// rate-limit child process creation themselves and a higher number
	// just queues at the OS level, (c) Claude Code's typical concurrent
	// debug fan-out is 3-5 commands.
	maxConcurrentExec     = 8
	maxConcurrentFilePull = 4
)

// errControlPaused is returned to a controller when this machine's
// "allow remote control" switch is off.
const errControlPaused = "远程控制已暂停：本机 U-Link 的「允许被控制」开关已关闭"

// Client is the agent WebSocket client that connects to the relay server.
type Client struct {
	serverURL string
	token     string
	deviceID  string

	// v0.2 identity: the key file and the one-time pairing code used on
	// first registration.  Both are optional for legacy relays.
	deviceKeyPath string
	pairingCode   string
	tlsPin        string

	// fileRoots confines push/pull to explicitly allowed directories.
	// Empty means legacy behavior (only reject ".."); production should
	// always set it (cmd/agent defaults to home + temp).
	fileRoots []string

	// enrollCallback is invoked when the relay returns an employee-facing
	// enrollment code (reverse flow: employee shows code, IT approves).
	enrollCallback func(code string, expires time.Time)

	// bypassProxy dials the relay directly.  Desktop installs on Chinese
	// laptops often inherit HTTP(S)_PROXY from the login shell (Clash),
	// which makes the wss handshake to the domestic relay time out.  The
	// standalone agent keeps the historical ProxyFromEnvironment
	// behavior; only embedders opt in.
	bypassProxy bool

	execSem *semaphore.Weighted
	fileSem *semaphore.Weighted

	// activeExec tracks running streaming execs by RequestID so a
	// TypeExecCancel from the controller can find and kill them.
	// Buffered execs (legacy non-streaming path) intentionally aren't
	// tracked — they're synchronous and short-lived.
	activeMu   sync.Mutex
	activeExec map[string]context.CancelFunc

	// audit logs every operator action to a customer-visible file so
	// they can audit support sessions after the fact.  Best-effort:
	// failures during open/write are swallowed — the agent must never
	// crash because the audit file is unwritable.
	audit *AuditLog

	// Connection state + optional observer, used by the desktop tray.
	stateMu  sync.RWMutex
	state    ConnState
	stateErr string
	onState  func(ConnState, string)

	// controlPolicy lets an embedder (U-Link desktop) pause inbound
	// remote control without tearing down the connection. nil = allow.
	policyMu      sync.RWMutex
	controlPolicy func() bool

	// auditHook mirrors audit entries into an embedder's log ring.
	auditHookMu sync.RWMutex
	auditHook   func(string)
}

// ConnState is the agent's view of its own relay connection.  It is not
// a statement about any other device: "online" means this agent is
// registered with the relay right now.
type ConnState string

const (
	ConnStateConnecting ConnState = "connecting"
	ConnStateOnline     ConnState = "online"
	ConnStateOffline    ConnState = "offline"
	ConnStateStopped    ConnState = "stopped"
)

// SetBypassProxy makes the client ignore HTTP_PROXY/HTTPS_PROXY.
func (c *Client) SetBypassProxy(v bool) {
	c.stateMu.Lock()
	c.bypassProxy = v
	c.stateMu.Unlock()
}

// SetDeviceKeyPath overrides where the Ed25519 device key is stored.
// Empty means the default (~/.uclaw/device.key or %LOCALAPPDATA%\uclaw\device.key).
func (c *Client) SetDeviceKeyPath(path string) {
	c.stateMu.Lock()
	c.deviceKeyPath = path
	c.stateMu.Unlock()
}

// SetPairingCode sets the one-time code used to enroll a new device.
func (c *Client) SetPairingCode(code string) {
	c.stateMu.Lock()
	c.pairingCode = code
	c.stateMu.Unlock()
}

// SetTLSPin pins the relay leaf certificate (SHA-256 hex; empty = legacy).
func (c *Client) SetTLSPin(pin string) {
	c.stateMu.Lock()
	c.tlsPin = pin
	c.stateMu.Unlock()
}

// SetEnrollCallback registers a callback for employee-facing enrollment codes.
func (c *Client) SetEnrollCallback(fn func(code string, expires time.Time)) {
	c.stateMu.Lock()
	c.enrollCallback = fn
	c.stateMu.Unlock()
}

// SetFileRoots restricts push/pull to the given directories.  Empty
// restores legacy behavior; production callers should set this.
func (c *Client) SetFileRoots(roots []string) {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		if r = strings.TrimSpace(r); r != "" {
			if abs, err := filepath.Abs(r); err == nil {
				clean = append(clean, abs)
			}
		}
	}
	c.stateMu.Lock()
	c.fileRoots = clean
	c.stateMu.Unlock()
}

func defaultFileRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{home, os.TempDir()}
}

// resolveFilePath cleans a remote path and confines it to fileRoots.
func (c *Client) resolveFilePath(path string) (string, error) {
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	c.stateMu.RLock()
	roots := append([]string(nil), c.fileRoots...)
	c.stateMu.RUnlock()
	if len(roots) == 0 {
		return abs, nil
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed file roots", path)
}

func (c *Client) keyPath() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.deviceKeyPath != "" {
		return c.deviceKeyPath
	}
	return defaultDeviceKeyPath()
}

func (c *Client) pairing() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.pairingCode
}

func defaultDeviceKeyPath() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "uclaw", "device.key")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".uclaw", "device.key")
}

// SetStateCallback registers an observer for connection transitions.
// The callback may fire from connection goroutines; it must not block.
func (c *Client) SetStateCallback(fn func(ConnState, string)) {
	c.stateMu.Lock()
	c.onState = fn
	c.stateMu.Unlock()
}

// State returns the current connection state plus the last error, if any.
func (c *Client) State() (ConnState, string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state, c.stateErr
}

func (c *Client) setState(s ConnState, errMsg string) {
	c.stateMu.Lock()
	prev := c.state
	c.state = s
	c.stateErr = errMsg
	cb := c.onState
	c.stateMu.Unlock()
	if cb != nil && (prev != s || errMsg != "") {
		cb(s, errMsg)
	}
}

// SetControlPolicy installs a dynamic gate for inbound exec / file
// operations.  The function is evaluated per request, so a tray toggle
// takes effect immediately without reconnecting.
func (c *Client) SetControlPolicy(fn func() bool) {
	c.policyMu.Lock()
	c.controlPolicy = fn
	c.policyMu.Unlock()
}

func (c *Client) controlAllowed() bool {
	c.policyMu.RLock()
	fn := c.controlPolicy
	c.policyMu.RUnlock()
	if fn == nil {
		return true
	}
	return fn()
}

// SetAuditHook mirrors every audit entry into an embedder-owned log.
func (c *Client) SetAuditHook(fn func(string)) {
	c.auditHookMu.Lock()
	c.auditHook = fn
	c.auditHookMu.Unlock()
}

// recordAudit writes to the on-disk audit log and notifies the hook.
func (c *Client) recordAudit(line string) {
	line = redact.String(line)
	if c.audit != nil {
		c.audit.Append(line)
	}
	c.auditHookMu.RLock()
	hook := c.auditHook
	c.auditHookMu.RUnlock()
	if hook != nil {
		hook(line)
	}
}

// NewClient creates a new agent client.  If deviceID is empty, the
// agent first tries to load a previously-persisted ID from
// ~/.uclaw/device-id (per-user, survives reboots & re-runs of the
// agent.ps1 installer), falling back to a fresh machine-flavored ID
// that gets persisted for next time.  This makes customer support
// tractable: the operator can keep using the same Device ID across
// the customer's reboots and subsequent `irm | iex` invocations
// instead of having to re-ask for a new pc-XXXX every session.
func NewClient(serverURL, token, deviceID string) *Client {
	if deviceID == "" {
		deviceID = resolveDeviceID()
	}
	return &Client{
		serverURL:  serverURL,
		token:      token,
		deviceID:   deviceID,
		execSem:    semaphore.NewWeighted(maxConcurrentExec),
		fileSem:    semaphore.NewWeighted(maxConcurrentFilePull),
		activeExec: make(map[string]context.CancelFunc),
		audit:      NewAuditLog(),
		fileRoots:  defaultFileRoots(),
	}
}

// DeviceID returns the resolved device ID (after loading/generating).
// Callers (cmd/agent banner) need this so they can show the customer
// the exact ID before any registration message hits the server, and
// also so they can copy it to the clipboard.
func (c *Client) DeviceID() string {
	return c.deviceID
}

// AuditPath returns the audit log path so callers (e.g. the agent
// CLI's banner) can show it to the customer.
func (c *Client) AuditPath() string {
	if c.audit == nil {
		return ""
	}
	return c.audit.Path()
}

// AuditCount returns the number of operator actions recorded so far.
func (c *Client) AuditCount() int64 {
	if c.audit == nil {
		return 0
	}
	return c.audit.Count()
}

// Close releases agent resources (currently just the audit log).
// Idempotent; can be called from multiple shutdown paths.
func (c *Client) Close() {
	if c.audit != nil {
		c.audit.Close()
	}
}

// deviceIDFile returns the per-user persistence path for device IDs.
// Picked %LOCALAPPDATA%\uclaw on Windows (matches the agent.ps1
// installer's $AGENT_DIR convention) and ~/.uclaw elsewhere.
func deviceIDFile() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "uclaw", "device-id")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".uclaw", "device-id")
}

// resolveDeviceID returns a stable device ID for this machine, creating
// + persisting one on first run.  Format: pc-{4-hex-suffix} where the
// suffix is derived from a SHA-1 of the hostname so it stays stable
// across runs but differs between machines that share the persistence
// directory (rare, but defensive).
func resolveDeviceID() string {
	path := deviceIDFile()
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" && len(id) <= 64 {
				return id
			}
		}
	}

	hostname, _ := os.Hostname()
	id := generateStableDeviceID(hostname)

	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(id), 0o600)
	}
	return id
}

// generateStableDeviceID returns a deterministic short ID from the
// hostname.  Falls back to a random UUID prefix if hostname is empty
// (containers, restricted environments).
func generateStableDeviceID(hostname string) string {
	if hostname == "" {
		return "pc-" + uuid.New().String()[:4]
	}
	sum := sha1.Sum([]byte(hostname))
	return fmt.Sprintf("pc-%x", sum[:2])
}

// Run connects to the server and handles messages with auto-reconnect.
// Kept for the standalone agent binary; the desktop app uses RunContext
// so a config change or "restart connection" can stop the old loop.
func (c *Client) Run() {
	c.RunContext(context.Background())
}

// RunContext is Run with cancellation.  It owns the reconnect loop and
// the lifetime of each connection attempt.
func (c *Client) RunContext(ctx context.Context) {
	delay := initialReconnectDelay
	for {
		if ctx.Err() != nil {
			c.setState(ConnStateStopped, "")
			return
		}
		start := time.Now()
		err := c.connect(ctx)
		connectedFor := time.Since(start)
		if ctx.Err() != nil {
			c.setState(ConnStateStopped, "")
			return
		}
		if err != nil {
			c.setState(ConnStateOffline, err.Error())
			log.Printf("Connection failed: %v, retrying in %v...", err, delay)
		}
		delay = nextReconnectDelay(delay, connectedFor)
		select {
		case <-ctx.Done():
			c.setState(ConnStateStopped, "")
			return
		case <-time.After(delay):
		}
	}
}

// nextReconnectDelay returns the wait before the next dial attempt.  A
// session that stayed up for a while means the network is healthy, so
// reset to the fast delay; otherwise back off exponentially with +-20%
// jitter so a fleet of agents doesn't hammer a recovering relay in sync.
func nextReconnectDelay(prev, connectedFor time.Duration) time.Duration {
	if connectedFor >= healthySessionThreshold {
		return initialReconnectDelay
	}
	next := prev * 2
	if next < initialReconnectDelay {
		next = initialReconnectDelay
	}
	if next > maxReconnectDelay {
		next = maxReconnectDelay
	}
	jitter := time.Duration(rand.Int63n(int64(next/5)+1)) - next/10
	return next + jitter
}

func (c *Client) connect(ctx context.Context) error {
	log.Printf("Connecting to %s ...", c.serverURL)
	c.setState(ConnStateConnecting, "")

	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	c.stateMu.RLock()
	bypassProxy := c.bypassProxy
	c.stateMu.RUnlock()
	if bypassProxy {
		dialer.Proxy = nil
	} else {
		dialer.Proxy = http.ProxyFromEnvironment
	}
	// Skip TLS verification for self-signed certificates
	if strings.HasPrefix(c.serverURL, "wss://") {
		c.stateMu.RLock()
		pin := c.tlsPin
		c.stateMu.RUnlock()
		dialer.TLSClientConfig = tlspin.Config(pin)
	}
	rawConn, _, err := dialer.DialContext(ctx, c.serverURL, nil)
	if err != nil {
		return err
	}
	rawConn.SetReadLimit(protocol.MaxReadMessageSize)
	_ = rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))
	conn := wsconn.New(rawConn)

	// Idempotent close so the ctx watcher, the read loop and the deferred
	// cleanup can all race to tear the connection down safely.
	var closeOnce sync.Once
	closed := make(chan struct{})
	closeConn := func() {
		closeOnce.Do(func() {
			close(closed)
			_ = rawConn.Close()
		})
	}

	var heartbeatStop chan struct{}
	var heartbeatDone chan struct{}
	defer func() {
		if heartbeatStop != nil {
			close(heartbeatStop)
		}
		// A close wakes a heartbeat that is currently stuck in a write
		// deadline, then waiting for heartbeatDone prevents an old
		// connection generation from surviving into a reconnect.
		closeConn()
		if heartbeatDone != nil {
			<-heartbeatDone
		}
	}()

	// Cancellation has to tear down the socket from outside the read
	// loop; a blocked ReadMessage would otherwise keep the old
	// connection alive after the caller asked for a restart.
	go func() {
		select {
		case <-ctx.Done():
			closeConn()
		case <-closed:
		}
	}()

	log.Println("Connected to relay server")

	// Authenticate
	authMsg := protocol.Message{
		Type:  protocol.TypeAuth,
		Token: c.token,
	}
	if err := c.sendOn(conn, authMsg); err != nil {
		return err
	}
	var authResp protocol.Message
	if err := rawConn.ReadJSON(&authResp); err != nil {
		return fmt.Errorf("auth response: %w", err)
	}
	if authResp.Type == protocol.TypeAuthResult && !authResp.Success {
		return fmt.Errorf("auth failed: %s", authResp.Error)
	}

	// Load (or create) this device's Ed25519 key.  A key is optional for
	// old relays, but required when the relay has REQUIRE_DEVICE_KEYS=1.
	priv, pub, keyErr := devicekey.LoadOrCreate(c.keyPath())
	if keyErr != nil {
		log.Printf("Device key unavailable (%v); registering as legacy device", keyErr)
	}
	publicKey := ""
	if pub != nil {
		publicKey = devicekey.PublicBase64(pub)
	}

	// Register device
	hostname, _ := os.Hostname()
	regMsg := protocol.Message{
		Type:              protocol.TypeRegister,
		DeviceID:          c.deviceID,
		Hostname:          hostname,
		OS:                runtime.GOOS,
		Version:           version.Version,
		PublicKey:         publicKey,
		PairingCode:       c.pairing(),
		RequestEnrollment: c.pairing() == "",
	}
	if err := c.sendOn(conn, regMsg); err != nil {
		return err
	}
	if err := c.awaitRegister(rawConn, conn, priv, publicKey); err != nil {
		return err
	}
	log.Printf("Registered as device: %s (%s)", c.deviceID, hostname)
	c.setState(ConnStateOnline, "")

	// Start a heartbeat tied to this connection generation. Its explicit
	// stop channel makes connect() release it before attempting a reconnect.
	heartbeatStop = make(chan struct{})
	heartbeatDone = make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		c.heartbeat(conn, heartbeatStop, heartbeatInterval)
	}()

	// Read messages — single reader goroutine, handlers may spawn
	// goroutines for blocking work (exec / file pull) so the read loop
	// keeps draining.
	for {
		_, data, err := rawConn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			return err
		}
		_ = rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout))

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Parse error: %v", err)
			continue
		}

		c.handleMessage(conn, msg)
	}
}

// awaitRegister reads the relay's register challenge/result.  With an
// old relay (no v0.2 identity) this times out after 5s and proceeds in
// legacy mode; with a v0.2 relay it proves possession of the device key.
func (c *Client) awaitRegister(rawConn *websocket.Conn, conn *wsconn.Conn, priv ed25519.PrivateKey, publicKey string) error {
	_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = rawConn.SetReadDeadline(time.Now().Add(peerReadTimeout)) }()
	for {
		var msg protocol.Message
		if err := rawConn.ReadJSON(&msg); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				log.Printf("No register ack from relay; assuming legacy registration")
				return nil
			}
			return err
		}
		switch msg.Type {
		case protocol.TypeRegisterChallenge:
			if priv == nil {
				return fmt.Errorf("relay requires a device key but none is available")
			}
			sig := devicekey.SignMessage(priv, devicekey.RegisterMessage(c.deviceID, msg.Nonce))
			if err := c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeRegisterProof,
				DeviceID:  c.deviceID,
				PublicKey: publicKey,
				Nonce:     msg.Nonce,
				Signature: sig,
			}); err != nil {
				return err
			}
			_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		case protocol.TypeEnrollCode:
			if msg.Code == "" {
				return fmt.Errorf("relay returned an empty enrollment code")
			}
			c.stateMu.RLock()
			cb := c.enrollCallback
			c.stateMu.RUnlock()
			if cb != nil {
				cb(msg.Code, time.Unix(msg.ExpiresAtUnix, 0))
			}
			log.Printf("Enrollment code: %s (waiting for IT approval)", msg.Code)
			_ = rawConn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		case protocol.TypeEnrollApproved:
			log.Printf("Enrollment approved for device %s", msg.DeviceID)
			return nil
		case protocol.TypeRegisterResult:
			if !msg.Success {
				return fmt.Errorf("registration rejected: %s", msg.Error)
			}
			return nil
		case protocol.TypePong, protocol.TypeAuthResult:
			continue
		default:
			// Ignore unrelated frames while waiting; the normal read
			// loop will handle them after registration.
		}
	}
}

func (c *Client) handleMessage(conn *wsconn.Conn, msg protocol.Message) {
	switch msg.Type {
	case protocol.TypeExec:
		if !c.controlAllowed() {
			c.recordAudit(fmt.Sprintf("REJECT exec req=%s (control paused)", msg.RequestID))
			_ = c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeResult,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				ExitCode:  -1,
				Stderr:    errControlPaused,
			})
			return
		}
		c.recordAudit(c.summarizeExec(msg))
		// Spawn so the read loop keeps draining; semaphore inside
		// handleExec bounds parallelism.
		go c.handleExec(conn, msg)

	case protocol.TypeExecCancel:
		c.recordAudit(fmt.Sprintf("CANCEL exec req=%s", msg.RequestID))
		c.cancelExec(msg.RequestID)

	case protocol.TypeFilePush:
		if !c.controlAllowed() {
			c.recordAudit(fmt.Sprintf("REJECT push file=%q req=%s (control paused)", msg.FilePath, msg.RequestID))
			_ = c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeFilePushAck,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				Success:   false,
				Error:     errControlPaused,
			})
			return
		}
		c.recordAudit(fmt.Sprintf("PUSH file=%q size=%d req=%s",
			msg.FilePath, msg.FileSize, msg.RequestID))
		log.Printf("Receiving file: %s -> %s (req: %s)", msg.FileName, msg.FilePath, msg.RequestID)
		ackMsg := c.handleFilePush(msg)
		if err := c.sendOn(conn, ackMsg); err != nil {
			log.Printf("Failed to send file push ack: %v", err)
		}

	case protocol.TypeFilePull:
		if !c.controlAllowed() {
			c.recordAudit(fmt.Sprintf("REJECT pull file=%q req=%s (control paused)", msg.FilePath, msg.RequestID))
			_ = c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeFilePullChunk,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				EOF:       true,
				Error:     errControlPaused,
			})
			return
		}
		c.recordAudit(fmt.Sprintf("PULL file=%q req=%s",
			msg.FilePath, msg.RequestID))
		go c.handleFilePull(conn, msg)

	case protocol.TypePing:
		// Server-initiated keepalive; just respond.
		_ = c.sendOn(conn, protocol.Message{Type: protocol.TypePong, DeviceID: c.deviceID})

	case protocol.TypeAuthResult:
		if !msg.Success {
			log.Printf("Auth failed: %s", msg.Error)
		} else {
			log.Println("Authenticated successfully")
		}
	}
}

// handleExec dispatches between buffered (v1 compatible) and streaming
// execution based on msg.Streaming.  Each exec consumes one slot of the
// concurrency semaphore so the agent can't be overwhelmed by a runaway
// controller.
func (c *Client) handleExec(conn *wsconn.Conn, msg protocol.Message) {
	ctx := context.Background()
	if err := c.execSem.Acquire(ctx, 1); err != nil {
		c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeResult,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			ExitCode:  -1,
			Stderr:    "exec semaphore acquire failed: " + err.Error(),
		})
		return
	}
	defer c.execSem.Release(1)

	if msg.Streaming {
		c.runStreamingExec(conn, msg)
		return
	}

	log.Printf("Executing: %s (req: %s)", redact.String(msg.Command), msg.RequestID)
	result := ExecuteWithShell(msg.Command, msg.Shell)

	resp := protocol.Message{
		Type:      protocol.TypeResult,
		RequestID: msg.RequestID,
		DeviceID:  c.deviceID,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		ExitCode:  result.ExitCode,
	}
	if err := c.sendOn(conn, resp); err != nil {
		log.Printf("Failed to send result: %v", err)
	}
}

func (c *Client) runStreamingExec(conn *wsconn.Conn, msg protocol.Message) {
	log.Printf("Streaming exec: %s (req: %s, shell=%s)", msg.Command, msg.RequestID, msg.Shell)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	c.activeMu.Lock()
	c.activeExec[msg.RequestID] = cancel
	c.activeMu.Unlock()
	defer func() {
		c.activeMu.Lock()
		delete(c.activeExec, msg.RequestID)
		c.activeMu.Unlock()
	}()

	_, chunks, result, err := RunStream(ctx, msg.Command, msg.Shell)
	if err != nil {
		c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeResult,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			ExitCode:  -1,
			Stderr:    err.Error(),
		})
		return
	}

	seq := 0
	for chunk := range chunks {
		out := protocol.Message{
			Type:      protocol.TypeExecChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			Seq:       seq,
		}
		if chunk.Kind == ChunkStdout {
			out.Stdout = string(chunk.Data)
		} else {
			out.Stderr = string(chunk.Data)
		}
		if err := c.sendOn(conn, out); err != nil {
			log.Printf("Failed to send exec chunk: %v", err)
			cancel()
			return
		}
		seq++
	}

	exitCode := <-result
	final := protocol.Message{
		Type:      protocol.TypeResult,
		RequestID: msg.RequestID,
		DeviceID:  c.deviceID,
		ExitCode:  exitCode,
		Seq:       seq,
		EOF:       true,
	}
	// Cancellation surfaces as a non-zero exec.ExitError; map
	// context-driven kills onto the documented synthetic code so the
	// controller can distinguish "user cancelled" from "process crashed".
	if ctx.Err() == context.Canceled || ctx.Err() == context.DeadlineExceeded {
		final.ExitCode = protocol.ExitCodeCancelled
		final.Stderr = "exec cancelled"
	}
	if err := c.sendOn(conn, final); err != nil {
		log.Printf("Failed to send exec final result: %v", err)
	}
}

func (c *Client) cancelExec(reqID string) {
	c.activeMu.Lock()
	cancel, ok := c.activeExec[reqID]
	c.activeMu.Unlock()
	if !ok {
		log.Printf("ExecCancel: no active exec for req %s", reqID)
		return
	}
	log.Printf("ExecCancel: killing req %s", reqID)
	cancel()
}

func (c *Client) handleFilePush(msg protocol.Message) protocol.Message {
	ack := protocol.Message{
		Type:      protocol.TypeFilePushAck,
		RequestID: msg.RequestID,
		DeviceID:  c.deviceID,
	}

	cleanPath, err := c.resolveFilePath(msg.FilePath)
	if err != nil {
		ack.Success = false
		ack.Error = err.Error()
		return ack
	}
	msg.FilePath = cleanPath

	if msg.FileSize > protocol.MaxPushFileSize {
		ack.Success = false
		ack.Error = fmt.Sprintf("file too large: %d bytes (max %d)", msg.FileSize, protocol.MaxPushFileSize)
		return ack
	}

	data, err := base64.StdEncoding.DecodeString(msg.FileData)
	if err != nil {
		ack.Success = false
		ack.Error = fmt.Sprintf("base64 decode failed: %v", err)
		return ack
	}

	// Verify actual decoded size (don't trust msg.FileSize)
	if int64(len(data)) > protocol.MaxPushFileSize {
		ack.Success = false
		ack.Error = fmt.Sprintf("decoded data exceeds size limit: %d bytes (max %d)", len(data), protocol.MaxPushFileSize)
		return ack
	}

	// Ensure parent directory exists
	dir := filepath.Dir(msg.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		ack.Success = false
		ack.Error = fmt.Sprintf("mkdir failed: %v", err)
		return ack
	}

	if err := os.WriteFile(msg.FilePath, data, 0644); err != nil {
		ack.Success = false
		ack.Error = fmt.Sprintf("write file failed: %v", err)
		return ack
	}

	log.Printf("File saved: %s (%d bytes)", msg.FilePath, len(data))
	ack.Success = true
	ack.Stdout = fmt.Sprintf("File saved: %s (%d bytes)", msg.FilePath, len(data))
	return ack
}

// handleFilePull reads a remote file and streams it back as a series of
// TypeFilePullChunk messages, ending with EOF=true.  Errors deliver a
// single chunk with EOF=true and Error set; the controller treats that
// as the failure path (no partial file written).
func (c *Client) handleFilePull(conn *wsconn.Conn, msg protocol.Message) {
	if err := c.fileSem.Acquire(context.Background(), 1); err != nil {
		_ = c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeFilePullChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			EOF:       true,
			Error:     "file pull semaphore acquire failed: " + err.Error(),
		})
		return
	}
	defer c.fileSem.Release(1)

	cleanPath, err := c.resolveFilePath(msg.FilePath)
	if err != nil {
		_ = c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeFilePullChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			EOF:       true,
			Error:     err.Error(),
		})
		return
	}

	f, err := os.Open(cleanPath)
	if err != nil {
		c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeFilePullChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			EOF:       true,
			Error:     err.Error(),
		})
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeFilePullChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			EOF:       true,
			Error:     err.Error(),
		})
		return
	}
	if stat.Size() > protocol.MaxPullFileSize {
		c.sendOn(conn, protocol.Message{
			Type:      protocol.TypeFilePullChunk,
			RequestID: msg.RequestID,
			DeviceID:  c.deviceID,
			EOF:       true,
			Error:     fmt.Sprintf("file too large: %d bytes (max %d)", stat.Size(), protocol.MaxPullFileSize),
		})
		return
	}

	log.Printf("Pulling file: %s (%d bytes, req: %s)", cleanPath, stat.Size(), msg.RequestID)

	buf := make([]byte, protocol.FilePullChunkSize)
	seq := 0
	for {
		n, rerr := f.Read(buf)
		// io.Reader contract: a non-Read-EOF error AFTER returning some
		// bytes is permitted, so n>0 and rerr==io.EOF can both be true.
		// We must always emit an EOF-marker chunk before returning, even
		// when the previous chunk already set EOF=true — the controller
		// uses EOF=true on a TypeFilePullChunk to close the request.
		isEOF := rerr == io.EOF

		if n > 0 {
			chunk := protocol.Message{
				Type:      protocol.TypeFilePullChunk,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				Seq:       seq,
				FileData:  base64.StdEncoding.EncodeToString(buf[:n]),
				FileSize:  stat.Size(),
				EOF:       isEOF,
			}
			if err := c.sendOn(conn, chunk); err != nil {
				log.Printf("Failed to send pull chunk: %v", err)
				return
			}
			seq++
		} else if isEOF {
			// Empty trailing read: file size was an exact multiple of
			// FilePullChunkSize, and we never marked EOF on the last
			// data-bearing chunk.  Emit a zero-byte EOF chunk to close.
			c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeFilePullChunk,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				Seq:       seq,
				EOF:       true,
				FileSize:  stat.Size(),
			})
		}

		if isEOF {
			return
		}
		if rerr != nil {
			c.sendOn(conn, protocol.Message{
				Type:      protocol.TypeFilePullChunk,
				RequestID: msg.RequestID,
				DeviceID:  c.deviceID,
				Seq:       seq,
				EOF:       true,
				Error:     rerr.Error(),
			})
			return
		}
	}
}

// heartbeat owns one connection generation. It only writes to its supplied
// socket, so an old heartbeat cannot close or write to a newer reconnect.
func (c *Client) heartbeat(conn *wsconn.Conn, stop <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			msg := protocol.Message{Type: protocol.TypeHeartbeat, DeviceID: c.deviceID}
			if err := c.sendOn(conn, msg); err != nil {
				log.Printf("Heartbeat send failed (%v); closing conn to trigger reconnect", err)
				_ = conn.Close()
				return
			}
		}
	}
}

func (c *Client) sendOn(conn *wsconn.Conn, msg protocol.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// summarizeExec turns an inbound TypeExec into a one-line audit entry
// like `EXEC req=abc12345 shell=powershell stream=true cmd="..."`.
// The command itself is truncated to 200 chars to keep the audit file
// readable when the operator pipes a multi-KB script in.
func (c *Client) summarizeExec(msg protocol.Message) string {
	cmd := redact.String(strings.ReplaceAll(msg.Command, "\n", "⏎"))
	if len(cmd) > 200 {
		cmd = cmd[:197] + "..."
	}
	shellLabel := msg.Shell
	if shellLabel == "" {
		shellLabel = "default"
	}
	stream := ""
	if msg.Streaming {
		stream = " stream=true"
	}
	return fmt.Sprintf("EXEC req=%s shell=%s%s cmd=%q",
		msg.RequestID, shellLabel, stream, cmd)
}
