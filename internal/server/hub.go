package server

import (
	"crypto/rand"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/wsconn"
)

// Device represents a connected agent device.
type Device struct {
	ID             string
	Hostname       string
	OS             string
	Version        string
	Conn           *wsconn.Conn
	ConnectAt      time.Time
	KeyFingerprint string
}

// pairCode is a one-time enrollment code issued to a controller.
type pairCode struct {
	expiresAt time.Time
}

// pendingEnrollment is an employee-initiated enrollment waiting for IT
// approval.  The code is shown on the employee screen; the IT controller
// sends it back to approve.
type pendingEnrollment struct {
	code      string
	deviceID  string
	hostname  string
	os        string
	version   string
	publicKey string
	conn      *wsconn.Conn
	expiresAt time.Time
}

// pairAttempt is a per-IP enrollment failure window (brute-force guard).
type pairAttempt struct {
	windowStart time.Time
	count       int
}

// Controller represents a connected CLI controller.
type Controller struct {
	Conn *wsconn.Conn
}

// pendingEntry stores a pending request with its creation time for TTL cleanup.
type pendingEntry struct {
	conn      *wsconn.Conn
	deviceID  string
	createdAt time.Time
}

// Hub manages all connected devices and controllers.
type Hub struct {
	mu              sync.RWMutex
	devices         map[string]*Device
	controllers     map[*wsconn.Conn]*Controller
	agentToken      string
	controllerToken string

	// v0.2 device identity: device ID -> base64 Ed25519 public key.
	// requireKeys is opt-in so old agents keep working until the fleet is
	// upgraded; set REQUIRE_DEVICE_KEYS=1 on the relay to enforce.
	deviceKeys   map[string]string
	requireKeys  bool
	pairCodes    map[string]*pairCode
	pairAttempts map[string]*pairAttempt
	enrollments  map[string]*pendingEnrollment

	// pendingResults routes results back to the requesting controller
	pendingMu sync.RWMutex
	pending   map[string]*pendingEntry // request_id -> controller conn + timestamp
}

const pendingTTL = 5 * time.Minute

// NewHub creates a new Hub.
func NewHub(agentToken, controllerToken string) *Hub {
	h := &Hub{
		devices:         make(map[string]*Device),
		controllers:     make(map[*wsconn.Conn]*Controller),
		agentToken:      agentToken,
		controllerToken: controllerToken,
		deviceKeys:      make(map[string]string),
		pairCodes:       make(map[string]*pairCode),
		pairAttempts:    make(map[string]*pairAttempt),
		enrollments:     make(map[string]*pendingEnrollment),
		pending:         make(map[string]*pendingEntry),
	}
	go h.cleanupPending()
	return h
}

// AuthenticateAgent checks the agent token.
func (h *Hub) AuthenticateAgent(token string) bool {
	return h.agentToken == token
}

// AuthenticateController checks the controller token.
func (h *Hub) AuthenticateController(token string) bool {
	return h.controllerToken == token
}

// SetRequireDeviceKeys toggles v0.2 identity enforcement.
func (h *Hub) SetRequireDeviceKeys(v bool) {
	h.mu.Lock()
	h.requireKeys = v
	h.mu.Unlock()
}

// RequireDeviceKeys reports whether key enforcement is on.
func (h *Hub) RequireDeviceKeys() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.requireKeys
}

// DeviceKey returns the bound public key for a device ID.
func (h *Hub) DeviceKey(id string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	key, ok := h.deviceKeys[id]
	return key, ok
}

// BindDeviceKey binds a device ID to a public key.  It refuses to replace
// an existing key with a different one; revocation is explicit.
func (h *Hub) BindDeviceKey(id, publicKey string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.deviceKeys[id]; ok {
		if existing != publicKey {
			return fmt.Errorf("device %s is already bound to a different key", id)
		}
		return nil
	}
	h.deviceKeys[id] = publicKey
	return nil
}

// AllowPairAttempt limits failed enrollments per IP to 10 per 5 minutes.
func (h *Hub) AllowPairAttempt(key string) bool {
	if key == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	a, ok := h.pairAttempts[key]
	if !ok || now.Sub(a.windowStart) > 5*time.Minute {
		h.pairAttempts[key] = &pairAttempt{windowStart: now, count: 0}
		return true
	}
	return a.count < 10
}

// NotePairFailure records one failed enrollment for an IP.
func (h *Hub) NotePairFailure(key string) {
	if key == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	a, ok := h.pairAttempts[key]
	if !ok || now.Sub(a.windowStart) > 5*time.Minute {
		h.pairAttempts[key] = &pairAttempt{windowStart: now, count: 1}
		return
	}
	a.count++
}

// ResetPairAttempts clears the failure window after a successful enroll.
func (h *Hub) ResetPairAttempts(key string) {
	if key == "" {
		return
	}
	h.mu.Lock()
	delete(h.pairAttempts, key)
	h.mu.Unlock()
}

// QueueEnrollment creates a pending employee enrollment and returns the
// short code shown to the employee.  IT must approve it from a controller.
func (h *Hub) QueueEnrollment(deviceID, hostname, os, version, publicKey string, conn *wsconn.Conn) (string, time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for code, e := range h.enrollments {
		if now.After(e.expiresAt) {
			delete(h.enrollments, code)
		}
	}
	if len(h.enrollments) >= 100 {
		return "", time.Time{}, fmt.Errorf("too many pending enrollments")
	}
	if publicKey == "" {
		return "", time.Time{}, fmt.Errorf("device public key required")
	}
	for attempt := 0; attempt < 20; attempt++ {
		var b [4]byte
		_, _ = rand.Read(b[:])
		n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
		code := fmt.Sprintf("%06d", n)
		if _, exists := h.enrollments[code]; exists {
			continue
		}
		expires := now.Add(10 * time.Minute)
		h.enrollments[code] = &pendingEnrollment{
			code: code, deviceID: deviceID, hostname: hostname, os: os,
			version: version, publicKey: publicKey, conn: conn, expiresAt: expires,
		}
		return code, expires, nil
	}
	return "", time.Time{}, fmt.Errorf("could not allocate enrollment code")
}

// ApproveEnrollment burns a code and returns the pending device.
func (h *Hub) ApproveEnrollment(code string) (*pendingEnrollment, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.enrollments[code]
	if !ok {
		return nil, false
	}
	delete(h.enrollments, code)
	if time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e, true
}

// RemoveEnrollmentByConn drops a pending enrollment when its agent
// disconnects before IT approval.
func (h *Hub) RemoveEnrollmentByConn(conn *wsconn.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for code, e := range h.enrollments {
		if e.conn == conn {
			delete(h.enrollments, code)
		}
	}
}

// IssuePairCode creates a one-time 6-digit code valid for 5 minutes.
func (h *Hub) IssuePairCode() (string, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for code, pc := range h.pairCodes {
		if now.After(pc.expiresAt) {
			delete(h.pairCodes, code)
		}
	}
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
		code := fmt.Sprintf("%06d", n)
		if _, exists := h.pairCodes[code]; exists {
			continue
		}
		expires := now.Add(5 * time.Minute)
		h.pairCodes[code] = &pairCode{expiresAt: expires}
		return code, expires
	}
}

// ConsumePairCode validates and burns a one-time code.
func (h *Hub) ConsumePairCode(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	pc, ok := h.pairCodes[code]
	if !ok || time.Now().After(pc.expiresAt) {
		delete(h.pairCodes, code)
		return false
	}
	delete(h.pairCodes, code)
	return true
}

// RevokeDevice removes the device key and drops the live connection.
func (h *Hub) RevokeDevice(id string) bool {
	h.mu.Lock()
	_, hadKey := h.deviceKeys[id]
	delete(h.deviceKeys, id)
	d, hadDevice := h.devices[id]
	if hadDevice {
		delete(h.devices, id)
	}
	h.mu.Unlock()
	if hadDevice && d != nil && d.Conn != nil {
		_ = d.Conn.Close()
	}
	log.Printf("Device revoked: %s (key=%v online=%v)", id, hadKey, hadDevice)
	return hadKey || hadDevice
}

// RegisterDevice registers a new device (legacy, no key fingerprint).
func (h *Hub) RegisterDevice(id, hostname, os, version string, conn *wsconn.Conn) {
	h.RegisterDeviceWithKey(id, hostname, os, version, "", conn)
}

// RegisterDeviceWithKey registers a device and records its key fingerprint.
func (h *Hub) RegisterDeviceWithKey(id, hostname, os, version, fingerprint string, conn *wsconn.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.devices[id] = &Device{
		ID:             id,
		Hostname:       hostname,
		OS:             os,
		Version:        version,
		Conn:           conn,
		ConnectAt:      time.Now(),
		KeyFingerprint: fingerprint,
	}
	log.Printf("Device registered: %s (%s, %s, version=%s, key=%s)", id, hostname, os, version, fingerprint)
}

// RemoveDevice removes a device by connection.
func (h *Hub) RemoveDevice(conn *wsconn.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, d := range h.devices {
		if d.Conn == conn {
			delete(h.devices, id)
			log.Printf("Device removed: %s", id)
			return
		}
	}
}

// RegisterController registers a CLI controller.
func (h *Hub) RegisterController(conn *wsconn.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.controllers[conn] = &Controller{Conn: conn}
}

// RemoveController removes a controller.
func (h *Hub) RemoveController(conn *wsconn.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.controllers, conn)
}

// GetDeviceList returns all registered devices.
func (h *Hub) GetDeviceList() []protocol.DeviceInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var devices []protocol.DeviceInfo
	for _, d := range h.devices {
		devices = append(devices, protocol.DeviceInfo{
			ID:             d.ID,
			Hostname:       d.Hostname,
			OS:             d.OS,
			Version:        d.Version,
			Online:         true,
			ConnectAt:      d.ConnectAt,
			KeyFingerprint: d.KeyFingerprint,
		})
	}
	return devices
}

// ForwardToDevice sends a message to a specific device.
func (h *Hub) ForwardToDevice(deviceID string, msg protocol.Message) error {
	h.mu.RLock()
	device, ok := h.devices[deviceID]
	h.mu.RUnlock()
	if !ok {
		return &DeviceNotFoundError{DeviceID: deviceID}
	}
	if err := device.Conn.WriteJSON(msg); err != nil {
		// A write deadline means this connection is unusable for all
		// future writes. Remove only this exact generation so a freshly
		// reconnected device with the same ID is never evicted.
		h.RemoveDevice(device.Conn)
		_ = device.Conn.Close()
		return err
	}
	return nil
}

// RegisterPending maps a request ID to a controller connection and the
// device it was addressed to, so a different agent cannot inject results.
func (h *Hub) RegisterPending(requestID, deviceID string, conn *wsconn.Conn) {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	h.pending[requestID] = &pendingEntry{conn: conn, deviceID: deviceID, createdAt: time.Now()}
}

// UnregisterPending removes a pending entry without forwarding —
// used when the request couldn't be dispatched (e.g. device offline)
// so the entry doesn't sit around until the TTL sweep.
func (h *Hub) UnregisterPending(requestID string) {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	delete(h.pending, requestID)
}

// ForwardResult sends a message from an agent back to the requesting
// controller.  For streaming exec / file pull, the pending entry must
// survive the intermediate TypeExecChunk / TypeFilePullChunk messages
// — only TypeResult, TypeFilePushAck, or a chunk with EOF=true closes
// the request.
func (h *Hub) ForwardResult(msg protocol.Message) {
	h.pendingMu.Lock()
	entry, ok := h.pending[msg.RequestID]
	if ok && entry.deviceID != "" && msg.DeviceID != "" && entry.deviceID != msg.DeviceID {
		h.pendingMu.Unlock()
		log.Printf("Dropped result for req=%s from device=%s (expected %s)", msg.RequestID, msg.DeviceID, entry.deviceID)
		return
	}
	if ok && isTerminal(msg) {
		delete(h.pending, msg.RequestID)
	}
	h.pendingMu.Unlock()

	if ok {
		if err := entry.conn.WriteJSON(msg); err != nil {
			log.Printf("Failed to forward result: %v", err)
		}
	}
}

// isTerminal reports whether msg ends a request and frees its pending
// entry.  Chunks (intermediate stream frames) keep the entry alive so
// subsequent chunks for the same RequestID still route correctly.
func isTerminal(msg protocol.Message) bool {
	switch msg.Type {
	case protocol.TypeResult, protocol.TypeFilePushAck:
		return true
	case protocol.TypeFilePullChunk:
		return msg.EOF
	}
	// TypeExecChunk is never terminal — the matching TypeResult is.
	return false
}

// cleanupPending removes expired pending entries every minute.
func (h *Hub) cleanupPending() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		h.pendingMu.Lock()
		now := time.Now()
		for id, entry := range h.pending {
			if now.Sub(entry.createdAt) > pendingTTL {
				delete(h.pending, id)
				log.Printf("Pending request expired: %s", id)
			}
		}
		h.pendingMu.Unlock()
	}
}

// DeviceNotFoundError indicates a device was not found.
type DeviceNotFoundError struct {
	DeviceID string
}

func (e *DeviceNotFoundError) Error() string {
	return "device not found: " + e.DeviceID
}
