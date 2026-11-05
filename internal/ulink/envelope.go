package ulink

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Stable process exit codes for AI callers.  Keep these documented in
// docs/ULINK-CLI.md: scripts key off them, not off stderr text.
const (
	ExitOK      = 0
	ExitGeneral = 1
	ExitUsage   = 2
	ExitOffline = 3
	ExitAuth    = 4
	ExitTimeout = 5
	ExitRemote  = 6
)

// Err is the machine-readable error inside an Envelope.
type Err struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Err) Error() string { return e.Message }

// Envelope is the one JSON shape every `--json` command returns.
type Envelope struct {
	OK    bool `json:"ok"`
	Data  any  `json:"data,omitempty"`
	Error *Err `json:"error,omitempty"`
}

// NewOK builds a successful envelope.
func NewOK(data any) Envelope { return Envelope{OK: true, Data: data} }

// NewErr builds a failed envelope.
func NewErr(code, message, hint string) Envelope {
	return Envelope{OK: false, Error: &Err{Code: code, Message: message, Hint: hint}}
}

// WriteJSON writes one envelope and a trailing newline.
func WriteJSON(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// ExitCode maps an error/envelope to a stable process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var e *Err
	if errors.As(err, &e) {
		return ExitCodeForCode(e.Code)
	}
	return ExitGeneral
}

// ExitCodeForCode maps a stable error code to a process exit code.
func ExitCodeForCode(code string) int {
	switch code {
	case "", "ok":
		return ExitOK
	case "usage", "bad_args":
		return ExitUsage
	case "offline", "device_not_found", "connect_failed":
		return ExitOffline
	case "auth", "unauthorized", "forbidden":
		return ExitAuth
	case "timeout":
		return ExitTimeout
	case "remote_failed":
		return ExitRemote
	default:
		return ExitGeneral
	}
}

// ClassifyError turns a transport/exec error into a stable code.
func ClassifyError(err error) *Err {
	if err == nil {
		return nil
	}
	var e *Err
	if errors.As(err, &e) {
		return e
	}
	msg := err.Error()
	switch {
	case containsAny(msg, "device not found", "not found", "no such device"):
		return &Err{Code: "device_not_found", Message: msg, Hint: "run `ulink devices` and use the exact device id"}
	case containsAny(msg, "token", "auth", "unauthorized"):
		return &Err{Code: "auth", Message: msg, Hint: "check --token / ULINK_TOKEN or the U-Link desktop config"}
	case containsAny(msg, "timeout", "deadline exceeded"):
		return &Err{Code: "timeout", Message: msg, Hint: "retry; check the device is online and the relay is reachable"}
	case containsAny(msg, "dial", "connect", "websocket", "no such host", "connection refused"):
		return &Err{Code: "connect_failed", Message: msg, Hint: "check --relay, proxy/TUN routes, and that the relay is reachable"}
	default:
		return &Err{Code: "failed", Message: msg}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && (len(s) >= len(sub)) && containsFold(s, sub) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	s = lowerASCII(s)
	sub = lowerASCII(sub)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// String renders an envelope for humans.
func (e Envelope) String() string {
	if e.OK {
		return "ok"
	}
	if e.Error == nil {
		return "failed"
	}
	return fmt.Sprintf("%s: %s", e.Error.Code, e.Error.Message)
}
