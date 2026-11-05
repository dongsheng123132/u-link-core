package protocol

import "time"

// Message types
const (
	TypeRegister    = "register"
	TypeExec        = "exec"
	TypeResult      = "result"
	TypeListDevices = "list_devices"
	TypeDevices     = "devices"
	TypeHeartbeat   = "heartbeat"
	TypePong        = "pong"
	TypePing        = "ping" // server→client keepalive (v0.3+)
	TypeAuth        = "auth"
	TypeAuthResult  = "auth_result"

	// v0.2 device identity / pairing.  A new device proves possession of
	// its Ed25519 key by signing a relay nonce; the relay binds device ID
	// to public key and refuses a different key for the same ID.
	TypeRegisterChallenge = "register_challenge"
	TypeRegisterProof     = "register_proof"
	TypeRegisterResult    = "register_result"
	TypePairIssue         = "pair_issue"
	TypePairCode          = "pair_code"
	TypeRevokeDevice      = "revoke_device"

	// v0.3 employee-initiated enrollment: the employee's agent asks for a
	// short code, displays it, and the IT controller approves it.
	TypeEnrollCode     = "enroll_code"
	TypeEnrollApprove  = "enroll_approve"
	TypeEnrollResult   = "enroll_result"
	TypeEnrollApproved = "enroll_approved"
	TypeFilePush       = "file_push"
	TypeFilePushAck    = "file_push_ack"

	// v0.3+ streaming exec: agent emits one TypeExecChunk per pipe read,
	// then a final TypeResult with the exit code.  Triggered by setting
	// Streaming=true on the inbound TypeExec.
	TypeExecChunk = "exec_chunk"

	// v0.3+ cancel a running exec: controller sends TypeExecCancel with
	// the original RequestID; agent kills the process and ends with a
	// TypeResult carrying ExitCode = ExitCodeCancelled.
	TypeExecCancel = "exec_cancel"

	// v0.3+ reverse file transfer: controller asks agent to read a remote
	// file and stream it back as a series of TypeFilePullChunk messages.
	TypeFilePull      = "file_pull"
	TypeFilePullChunk = "file_pull_chunk"
)

// ExitCodeCancelled is the synthetic exit code returned when an exec is
// cancelled by TypeExecCancel.  Picked to avoid colliding with shell
// conventions (130 = SIGINT, 137 = SIGKILL); -2 is what we already use
// in the controller for synthesized errors.
const ExitCodeCancelled = -2

// FilePullChunkSize is how much of a pulled file each TypeFilePullChunk
// carries.  1 MiB keeps each ws frame well below MaxReadMessageSize even
// after base64 expansion + JSON envelope.
const FilePullChunkSize = 1024 * 1024

// MaxPullFileSize bounds total bytes accepted from a single TypeFilePull,
// applied chunk-by-chunk on the controller side.  1 GiB is generous for
// log dumps, dump files, and heap snapshots while still preventing a
// runaway agent from filling the controller's disk.
const MaxPullFileSize = 1024 * 1024 * 1024

// MaxPushFileSize is the maximum file size allowed for push.
// 32 MiB so agent.exe (~10 MiB) and a typical compiled binary can be
// pushed in a single message without the protocol needing chunking.
// gorilla/websocket has no hard frame size limit on the write side and
// our SetReadLimit on connections is configured below to match.
const MaxPushFileSize = 32 * 1024 * 1024

// MaxReadMessageSize is the upper bound for a single inbound ws frame.
// Slightly larger than MaxPushFileSize to accommodate base64 expansion
// (factor 4/3) plus JSON envelope overhead.
const MaxReadMessageSize = 48 * 1024 * 1024

// Message is the base message structure for all WebSocket communication.
type Message struct {
	Type      string `json:"type"`
	DeviceID  string `json:"device_id,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	OS        string `json:"os,omitempty"`
	Version   string `json:"version,omitempty"` // agent build version, set on register
	Command   string `json:"command,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Token     string `json:"token,omitempty"`
	Success   bool   `json:"success,omitempty"`
	Error     string `json:"error,omitempty"`

	// For device list response
	Devices []DeviceInfo `json:"devices,omitempty"`

	// For file push
	FileName string `json:"file_name,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	FileData string `json:"file_data,omitempty"` // base64 encoded
	FileSize int64  `json:"file_size,omitempty"`

	// For streaming exec / file pull (v0.3+).
	// Streaming=true on TypeExec asks the agent to emit incremental
	// TypeExecChunk messages instead of buffering the whole output.
	// Seq orders chunks within a single RequestID.  EOF marks the last
	// chunk of a TypeFilePullChunk stream.  Shell selects the runner
	// (powershell|cmd|sh); empty = platform default.
	Streaming bool   `json:"streaming,omitempty"`
	Seq       int    `json:"seq,omitempty"`
	EOF       bool   `json:"eof,omitempty"`
	Shell     string `json:"shell,omitempty"`

	// v0.2 identity/pairing fields.
	PublicKey         string `json:"public_key,omitempty"`
	PairingCode       string `json:"pairing_code,omitempty"`
	RequestEnrollment bool   `json:"request_enrollment,omitempty"`
	Nonce             string `json:"nonce,omitempty"`
	Signature         string `json:"signature,omitempty"`
	Code              string `json:"code,omitempty"`
	ExpiresAtUnix     int64  `json:"expires_at_unix,omitempty"`
	KeyFingerprint    string `json:"key_fingerprint,omitempty"`
}

// DeviceInfo represents a connected device.
type DeviceInfo struct {
	ID             string    `json:"id"`
	Hostname       string    `json:"hostname"`
	OS             string    `json:"os"`
	Version        string    `json:"version,omitempty"`
	Online         bool      `json:"online"`
	ConnectAt      time.Time `json:"connect_at"`
	KeyFingerprint string    `json:"key_fingerprint,omitempty"`
}
