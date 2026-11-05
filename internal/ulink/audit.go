package ulink

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AuditEntry is one line in the local audit log.  Every remote action is
// recorded so a human can reconstruct what an AI agent did across machines.
type AuditEntry struct {
	Time       time.Time `json:"ts"`
	Session    string    `json:"session"`
	Action     string    `json:"action"`
	Device     string    `json:"device,omitempty"`
	Command    string    `json:"command,omitempty"`
	ExitCode   int       `json:"exit_code,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
}

// AppendAudit appends one entry to path as JSONL, creating 0700/0600 files.
func AppendAudit(path string, e AuditEntry) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

// ReadAuditTail returns the last n entries in file order.
func ReadAuditTail(path string, n int) ([]AuditEntry, error) {
	if n <= 0 {
		n = 50
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// Keep a bounded ring of the last n entries so large logs don't load
	// into memory.
	ring := make([]AuditEntry, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if len(ring) == n {
			copy(ring, ring[1:])
			ring[len(ring)-1] = e
		} else {
			ring = append(ring, e)
		}
	}
	return ring, sc.Err()
}
