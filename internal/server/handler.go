package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/devicekey"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/redact"
	"github.com/dongsheng123132/remote-agent/internal/wsconn"
	"github.com/gorilla/websocket"
)

const agentReadTimeout = 90 * time.Second

func revokeError(ok bool) string {
	if ok {
		return ""
	}
	return "device not found or already revoked"
}

// summarizeCommand truncates a command string for log readability.
// 80 chars matches typical terminal width and avoids leaking full
// payload (e.g. PowerShell scripts piped via stdin) into journald.
func summarizeCommand(s string) string {
	s = redact.String(strings.ReplaceAll(s, "\n", "⏎"))
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// pendingRegister is per-connection state between TypeRegister and
// TypeRegisterProof.  It never leaves the handler goroutine.
type pendingRegister struct {
	deviceID  string
	hostname  string
	os        string
	version   string
	publicKey string
	nonce     string
	expiresAt time.Time
}

// Handler holds the WebSocket handler logic.
type Handler struct {
	hub *Hub
}

// NewHandler creates a new Handler.
func NewHandler(hub *Hub) *Handler {
	return &Handler{hub: hub}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// beginRegistration validates a register message.  When key enforcement is
// on it sends a challenge and returns nil; the device must answer with a
// signed TypeRegisterProof.  Legacy mode (no key required) registers
// immediately and sends a success result.
func (h *Handler) beginRegistration(conn *wsconn.Conn, msg protocol.Message, remoteIP string) (*pendingRegister, error) {
	if strings.TrimSpace(msg.DeviceID) == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	publicKey := strings.TrimSpace(msg.PublicKey)
	if publicKey != "" {
		if _, err := devicekey.DecodePublic(publicKey); err != nil {
			return nil, fmt.Errorf("invalid device public key: %w", err)
		}
	}

	if !h.hub.RequireDeviceKeys() {
		// Graceful migration: bind a key when the agent sends one, but
		// still accept old agents that don't have keys yet.
		if publicKey != "" {
			if err := h.hub.BindDeviceKey(msg.DeviceID, publicKey); err != nil {
				return nil, err
			}
		}
		fingerprint := ""
		if publicKey != "" {
			pub, _ := devicekey.DecodePublic(publicKey)
			fingerprint = devicekey.Fingerprint(pub)
		}
		h.hub.RegisterDeviceWithKey(msg.DeviceID, msg.Hostname, msg.OS, msg.Version, fingerprint, conn)
		_ = conn.WriteJSON(protocol.Message{
			Type:           protocol.TypeRegisterResult,
			DeviceID:       msg.DeviceID,
			Success:        true,
			KeyFingerprint: fingerprint,
		})
		return nil, nil
	}

	if publicKey == "" {
		return nil, fmt.Errorf("device key required: upgrade the agent and set a pairing code")
	}
	if !h.hub.AllowPairAttempt(remoteIP) {
		return nil, fmt.Errorf("too many failed pairing attempts; wait 5 minutes")
	}
	existing, bound := h.hub.DeviceKey(msg.DeviceID)
	if bound && existing != publicKey {
		h.hub.NotePairFailure(remoteIP)
		return nil, fmt.Errorf("device %s is already bound to a different key; revoke it first", msg.DeviceID)
	}
	if !bound {
		if !h.hub.ConsumePairCode(strings.TrimSpace(msg.PairingCode)) {
			if msg.RequestEnrollment {
				code, expires, err := h.hub.QueueEnrollment(msg.DeviceID, msg.Hostname, msg.OS, msg.Version, publicKey, conn)
				if err != nil {
					h.hub.NotePairFailure(remoteIP)
					return nil, err
				}
				h.hub.ResetPairAttempts(remoteIP)
				_ = conn.WriteJSON(protocol.Message{
					Type:          protocol.TypeEnrollCode,
					DeviceID:      msg.DeviceID,
					Code:          code,
					ExpiresAtUnix: expires.Unix(),
				})
				return nil, nil
			}
			h.hub.NotePairFailure(remoteIP)
			return nil, fmt.Errorf("valid one-time pairing code required for a new device")
		}
	}
	h.hub.ResetPairAttempts(remoteIP)

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(raw[:])
	pending := &pendingRegister{
		deviceID:  msg.DeviceID,
		hostname:  msg.Hostname,
		os:        msg.OS,
		version:   msg.Version,
		publicKey: publicKey,
		nonce:     nonce,
		expiresAt: time.Now().Add(2 * time.Minute),
	}
	_ = conn.WriteJSON(protocol.Message{
		Type:     protocol.TypeRegisterChallenge,
		DeviceID: msg.DeviceID,
		Nonce:    nonce,
	})
	return pending, nil
}

// finishRegistration verifies the signed nonce and binds the device.
func (h *Handler) finishRegistration(conn *wsconn.Conn, pending *pendingRegister, msg protocol.Message) error {
	if pending == nil {
		return fmt.Errorf("no pending registration")
	}
	if time.Now().After(pending.expiresAt) {
		return fmt.Errorf("registration challenge expired")
	}
	if msg.Nonce != pending.nonce {
		return fmt.Errorf("nonce mismatch")
	}
	expected := devicekey.RegisterMessage(pending.deviceID, pending.nonce)
	if !devicekey.VerifyMessage(pending.publicKey, expected, msg.Signature) {
		return fmt.Errorf("invalid registration signature")
	}
	if err := h.hub.BindDeviceKey(pending.deviceID, pending.publicKey); err != nil {
		return err
	}
	pub, _ := devicekey.DecodePublic(pending.publicKey)
	fingerprint := devicekey.Fingerprint(pub)
	h.hub.RegisterDeviceWithKey(pending.deviceID, pending.hostname, pending.os, pending.version, fingerprint, conn)
	_ = conn.WriteJSON(protocol.Message{
		Type:           protocol.TypeRegisterResult,
		DeviceID:       pending.deviceID,
		Success:        true,
		KeyFingerprint: fingerprint,
	})
	return nil
}

// HandleAgent handles WebSocket connections from agent devices.
func (h *Handler) HandleAgent(w http.ResponseWriter, r *http.Request) {
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Agent upgrade error: %v", err)
		return
	}
	rawConn.SetReadLimit(protocol.MaxReadMessageSize)
	conn := wsconn.New(rawConn)
	defer func() {
		h.hub.RemoveEnrollmentByConn(conn)
		h.hub.RemoveDevice(conn)
		rawConn.Close()
	}()

	authenticated := false
	var pending *pendingRegister
	_ = rawConn.SetReadDeadline(time.Now().Add(agentReadTimeout))

	for {
		_, data, err := rawConn.ReadMessage()
		if err != nil {
			// Same logic as the Controller side — abnormal/normal/going
			// away closes are routine (NAT idle, agent reboot, customer
			// closed PowerShell window).  Real protocol errors surface
			// as 1002/1007/1011 and DO get logged.
			if !websocket.IsCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("Agent read error: %v", err)
			}
			return
		}
		_ = rawConn.SetReadDeadline(time.Now().Add(agentReadTimeout))

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Agent parse error: %v", err)
			continue
		}

		switch msg.Type {
		case protocol.TypeAuth:
			if h.hub.AuthenticateAgent(msg.Token) {
				authenticated = true
				if err := conn.WriteJSON(protocol.Message{Type: protocol.TypeAuthResult, Success: true}); err != nil {
					log.Printf("Agent auth response write error: %v", err)
					return
				}
			} else {
				if err := conn.WriteJSON(protocol.Message{Type: protocol.TypeAuthResult, Success: false, Error: "invalid token"}); err != nil {
					log.Printf("Agent auth rejection write error: %v", err)
				}
				return
			}

		case protocol.TypeRegister:
			if !authenticated {
				continue
			}
			p, err := h.beginRegistration(conn, msg, clientIP(r))
			if err != nil {
				log.Printf("Agent registration rejected: %v", err)
				_ = conn.WriteJSON(protocol.Message{
					Type:     protocol.TypeRegisterResult,
					DeviceID: msg.DeviceID,
					Success:  false,
					Error:    err.Error(),
				})
				return
			}
			pending = p

		case protocol.TypeRegisterProof:
			if !authenticated || pending == nil {
				continue
			}
			if err := h.finishRegistration(conn, pending, msg); err != nil {
				log.Printf("Agent registration proof failed: %v", err)
				_ = conn.WriteJSON(protocol.Message{
					Type:     protocol.TypeRegisterResult,
					DeviceID: pending.deviceID,
					Success:  false,
					Error:    err.Error(),
				})
				pending = nil
				return
			}
			pending = nil

		case protocol.TypeResult, protocol.TypeFilePushAck,
			protocol.TypeExecChunk, protocol.TypeFilePullChunk:
			// Streaming exec / file pull messages route back to the
			// originating controller via the same pending-request map.
			// Don't unregister on chunk delivery — only the final
			// TypeResult / EOF chunk closes the request.
			h.hub.ForwardResult(msg)

		case protocol.TypeHeartbeat:
			if err := conn.WriteJSON(protocol.Message{Type: protocol.TypePong}); err != nil {
				log.Printf("Agent heartbeat pong write error: %v", err)
				return
			}

		case protocol.TypePong:
			// Server-initiated TypePing reply; nothing to do, the read
			// itself was the keepalive.
		}
	}
}

// HandleController handles WebSocket connections from CLI controllers.
func (h *Handler) HandleController(w http.ResponseWriter, r *http.Request) {
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Controller upgrade error: %v", err)
		return
	}
	rawConn.SetReadLimit(protocol.MaxReadMessageSize)
	conn := wsconn.New(rawConn)
	defer func() {
		h.hub.RemoveController(conn)
		rawConn.Close()
	}()

	authenticated := false
	_ = rawConn.SetReadDeadline(time.Now().Add(agentReadTimeout))

	for {
		_, data, err := rawConn.ReadMessage()
		if err != nil {
			// 1006 abnormal closure happens on every short-lived CLI
			// invocation (`remote-agent list` exits → conn dies).  It's
			// noise unless we hit a real protocol violation, which would
			// surface as 1002 / 1007 / 1011 instead.
			if !websocket.IsCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("Controller read error: %v", err)
			}
			return
		}
		_ = rawConn.SetReadDeadline(time.Now().Add(agentReadTimeout))

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Controller parse error: %v", err)
			continue
		}

		switch msg.Type {
		case protocol.TypeAuth:
			if h.hub.AuthenticateController(msg.Token) {
				authenticated = true
				h.hub.RegisterController(conn)
				conn.WriteJSON(protocol.Message{Type: protocol.TypeAuthResult, Success: true})
			} else {
				conn.WriteJSON(protocol.Message{Type: protocol.TypeAuthResult, Success: false, Error: "invalid token"})
				return
			}

		case protocol.TypeListDevices:
			if !authenticated {
				continue
			}
			devices := h.hub.GetDeviceList()
			conn.WriteJSON(protocol.Message{Type: protocol.TypeDevices, Devices: devices})

		case protocol.TypePairIssue:
			if !authenticated {
				continue
			}
			code, expires := h.hub.IssuePairCode()
			log.Printf("Pairing code issued (expires %s)", expires.Format(time.RFC3339))
			conn.WriteJSON(protocol.Message{
				Type:          protocol.TypePairCode,
				Code:          code,
				ExpiresAtUnix: expires.Unix(),
			})

		case protocol.TypeEnrollApprove:
			if !authenticated {
				continue
			}
			enr, ok := h.hub.ApproveEnrollment(strings.TrimSpace(msg.Code))
			if !ok {
				conn.WriteJSON(protocol.Message{
					Type:     protocol.TypeEnrollResult,
					DeviceID: msg.DeviceID,
					Success:  false,
					Error:    "enrollment code not found or expired",
				})
				continue
			}
			if err := h.hub.BindDeviceKey(enr.deviceID, enr.publicKey); err != nil {
				conn.WriteJSON(protocol.Message{Type: protocol.TypeEnrollResult, DeviceID: enr.deviceID, Success: false, Error: err.Error()})
				continue
			}
			pub, _ := devicekey.DecodePublic(enr.publicKey)
			fingerprint := devicekey.Fingerprint(pub)
			h.hub.RegisterDeviceWithKey(enr.deviceID, enr.hostname, enr.os, enr.version, fingerprint, enr.conn)
			if enr.conn != nil {
				_ = enr.conn.WriteJSON(protocol.Message{
					Type:           protocol.TypeEnrollApproved,
					DeviceID:       enr.deviceID,
					KeyFingerprint: fingerprint,
				})
			}
			log.Printf("Enrollment approved: device=%s key=%s", enr.deviceID, fingerprint)
			conn.WriteJSON(protocol.Message{
				Type:           protocol.TypeEnrollResult,
				DeviceID:       enr.deviceID,
				Success:        true,
				KeyFingerprint: fingerprint,
			})

		case protocol.TypeRevokeDevice:
			if !authenticated {
				continue
			}
			ok := h.hub.RevokeDevice(msg.DeviceID)
			log.Printf("Revoke device=%s ok=%v", msg.DeviceID, ok)
			conn.WriteJSON(protocol.Message{
				Type:     protocol.TypeRegisterResult,
				DeviceID: msg.DeviceID,
				Success:  ok,
				Error:    revokeError(ok),
			})

		case protocol.TypeHeartbeat:
			// Controller-side keepalive against NAT idle timeouts.
			conn.WriteJSON(protocol.Message{Type: protocol.TypePong})

		case protocol.TypeExec, protocol.TypeFilePull:
			if !authenticated {
				continue
			}
			// Per-request audit line: lets us correlate agent-side
			// failures with the original controller intent later
			// (`journalctl -u remote-agent.service | grep <request_id>`).
			if msg.Type == protocol.TypeExec {
				streaming := ""
				if msg.Streaming {
					streaming = " streaming"
				}
				shellLabel := msg.Shell
				if shellLabel == "" {
					shellLabel = "default"
				}
				log.Printf("[req=%s] exec%s device=%s shell=%s cmd=%q",
					msg.RequestID, streaming, msg.DeviceID, shellLabel, summarizeCommand(msg.Command))
			} else {
				log.Printf("[req=%s] pull device=%s path=%q",
					msg.RequestID, msg.DeviceID, msg.FilePath)
			}
			h.hub.RegisterPending(msg.RequestID, msg.DeviceID, conn)
			if err := h.hub.ForwardToDevice(msg.DeviceID, msg); err != nil {
				// Forward failed (device offline etc.) — drop the pending
				// entry now so it doesn't sit until the 5-minute TTL sweep.
				h.hub.UnregisterPending(msg.RequestID)
				resultType := protocol.TypeResult
				if msg.Type == protocol.TypeFilePull {
					resultType = protocol.TypeFilePullChunk
				}
				errMsg := protocol.Message{
					Type:      resultType,
					RequestID: msg.RequestID,
					ExitCode:  -1,
					Stderr:    err.Error(),
					Error:     err.Error(),
				}
				if msg.Type == protocol.TypeFilePull {
					errMsg.EOF = true
				}
				conn.WriteJSON(errMsg)
			}

		case protocol.TypeExecCancel:
			if !authenticated {
				continue
			}
			log.Printf("[req=%s] cancel device=%s", msg.RequestID, msg.DeviceID)
			// Best-effort: if the agent is offline, the original exec is
			// already dead, so silently dropping is fine.  No pending
			// entry to register — the cancel itself doesn't expect a
			// reply, the exec's own TypeResult will deliver the
			// ExitCodeCancelled status.
			_ = h.hub.ForwardToDevice(msg.DeviceID, msg)

		case protocol.TypeFilePush:
			if !authenticated {
				continue
			}
			// Server-side file size check before forwarding
			if msg.FileSize > protocol.MaxPushFileSize {
				conn.WriteJSON(protocol.Message{
					Type:      protocol.TypeFilePushAck,
					RequestID: msg.RequestID,
					Success:   false,
					Error:     fmt.Sprintf("file too large: %d bytes (max %d)", msg.FileSize, protocol.MaxPushFileSize),
				})
				continue
			}
			h.hub.RegisterPending(msg.RequestID, msg.DeviceID, conn)
			if err := h.hub.ForwardToDevice(msg.DeviceID, msg); err != nil {
				h.hub.UnregisterPending(msg.RequestID)
				conn.WriteJSON(protocol.Message{
					Type:      protocol.TypeFilePushAck,
					RequestID: msg.RequestID,
					Success:   false,
					Error:     err.Error(),
				})
			}
		}
	}
}
