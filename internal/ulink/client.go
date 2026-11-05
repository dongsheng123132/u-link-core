package ulink

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/cli"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
)

// RelayURL normalises a relay base URL into the controller websocket URL.
func RelayURL(relay string) string {
	relay = strings.TrimSpace(relay)
	if strings.HasSuffix(relay, "/ws/controller") {
		return relay
	}
	return strings.TrimRight(relay, "/") + "/ws/controller"
}

// Connect dials the relay as a controller.
func Connect(cfg *Config) (*cli.Client, error) {
	if err := cfg.EnsureConnectedConfig(); err != nil {
		return nil, &Err{Code: "usage", Message: err.Error(), Hint: "run `ulink doctor` for a config check"}
	}
	c := cli.NewClient(RelayURL(cfg.Relay), cfg.ControllerToken)
	c.SetBypassProxy(true)
	if cfg.TLSPin != "" {
		c.SetTLSPin(cfg.TLSPin)
	} // the tool is used on Chinese laptops with Clash/TUN; do not route wss via HTTP_PROXY
	if err := c.Connect(); err != nil {
		return nil, ClassifyError(err)
	}
	return c, nil
}

// NewSessionID returns a short random id used in audit lines so one AI
// task can be correlated across several commands.
func NewSessionID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RunResult is the machine-readable outcome of one remote command.
type RunResult struct {
	Session    string `json:"session"`
	Device     string `json:"device"`
	Command    string `json:"command"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
}

// Run executes one command on a remote device and records an audit line.
func Run(cfg *Config, session, device, command, shell string) (*RunResult, error) {
	if device == "" {
		return nil, &Err{Code: "usage", Message: "device is required", Hint: "run `ulink devices`"}
	}
	if command == "" {
		return nil, &Err{Code: "usage", Message: "command is required", Hint: "usage: ulink run <device> -- <command>"}
	}
	if session == "" {
		session = NewSessionID()
	}

	start := time.Now()
	c, err := Connect(cfg)
	if err != nil {
		_ = AppendAudit(cfg.AuditPath, AuditEntry{
			Time: time.Now().UTC(), Session: session, Action: "run",
			Device: device, Command: command, OK: false, Error: err.Error(),
			DurationMS: time.Since(start).Milliseconds(),
		})
		return nil, err
	}
	defer c.Close()

	msg, err := c.ExecOpts(device, command, cli.ExecOptions{Shell: shell})
	duration := time.Since(start).Milliseconds()
	if err != nil {
		e := ClassifyError(err)
		_ = AppendAudit(cfg.AuditPath, AuditEntry{
			Time: time.Now().UTC(), Session: session, Action: "run",
			Device: device, Command: command, OK: false, Error: e.Message,
			DurationMS: duration,
		})
		return nil, e
	}

	res := &RunResult{
		Session:    session,
		Device:     device,
		Command:    command,
		Stdout:     msg.Stdout,
		Stderr:     msg.Stderr,
		ExitCode:   msg.ExitCode,
		DurationMS: duration,
	}
	_ = AppendAudit(cfg.AuditPath, AuditEntry{
		Time: time.Now().UTC(), Session: session, Action: "run",
		Device: device, Command: command, ExitCode: msg.ExitCode,
		DurationMS: duration, OK: msg.ExitCode == 0,
	})
	return res, nil
}

// ListDevices returns online relay devices.
func ListDevices(cfg *Config) ([]protocol.DeviceInfo, error) {
	c, err := Connect(cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	devices, err := c.ListDevices()
	if err != nil {
		return nil, ClassifyError(err)
	}
	return devices, nil
}

// IssuePairCode asks the relay for a one-time enrollment code.
func IssuePairCode(cfg *Config) (string, time.Time, error) {
	c, err := Connect(cfg)
	if err != nil {
		return "", time.Time{}, err
	}
	defer c.Close()
	code, expires, err := c.IssuePairCode()
	if err != nil {
		return "", time.Time{}, ClassifyError(err)
	}
	return code, expires, nil
}

// ApproveEnrollment approves an employee-initiated enrollment code.
func ApproveEnrollment(cfg *Config, code string) (string, string, error) {
	if strings.TrimSpace(code) == "" {
		return "", "", &Err{Code: "usage", Message: "enrollment code is required", Hint: "ask the employee for the 6-digit code"}
	}
	c, err := Connect(cfg)
	if err != nil {
		return "", "", err
	}
	defer c.Close()
	device, fingerprint, err := c.ApproveEnrollment(strings.TrimSpace(code))
	if err != nil {
		return "", "", ClassifyError(err)
	}
	return device, fingerprint, nil
}

// RevokeDevice removes a device key on the relay and disconnects it.
func RevokeDevice(cfg *Config, device string) error {
	if device == "" {
		return &Err{Code: "usage", Message: "device is required", Hint: "run `ulink devices`"}
	}
	c, err := Connect(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.RevokeDevice(device); err != nil {
		return ClassifyError(err)
	}
	return nil
}

// PingRelay verifies the relay is reachable and the token is accepted.
func PingRelay(cfg *Config) error {
	c, err := Connect(cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.ListDevices()
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	return nil
}
