package desktop

import (
	"strings"
	"sync"
	"time"
)

// LogEntry is one line shown in the local dashboard.  Kind separates
// U-Link's own chatter ("app") from inbound operator actions ("audit").
type LogEntry struct {
	TS    time.Time `json:"ts"`
	Kind  string    `json:"kind"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Ring is a fixed-size in-memory log buffer.  It also implements
// io.Writer so the standard logger can feed it.  Nothing here is
// durable: the on-disk log file and the agent's audit log are the
// durable artifacts.
type Ring struct {
	mu      sync.Mutex
	max     int
	entries []LogEntry
	pending string
}

// NewRing creates a ring holding at most max entries.
func NewRing(max int) *Ring {
	if max <= 0 {
		max = 400
	}
	return &Ring{max: max, entries: make([]LogEntry, 0, max)}
}

// Add appends one entry, dropping the oldest when full.
func (r *Ring) Add(kind, level, text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, LogEntry{
		TS:    time.Now(),
		Kind:  kind,
		Level: level,
		Text:  text,
	})
	if len(r.entries) > r.max {
		drop := len(r.entries) - r.max
		r.entries = append(r.entries[:0], r.entries[drop:]...)
	}
}

// AddAudit records an inbound operator action mirrored from the agent.
func (r *Ring) AddAudit(text string) {
	r.Add("audit", "info", text)
}

// Snapshot returns the newest limit entries in chronological order.
func (r *Ring) Snapshot(limit int) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := 0
	if limit > 0 && len(r.entries) > limit {
		start = len(r.entries) - limit
	}
	out := make([]LogEntry, len(r.entries)-start)
	copy(out, r.entries[start:])
	return out
}

// Write implements io.Writer for log.SetOutput.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	text := r.pending + string(p)
	r.pending = ""
	r.mu.Unlock()

	lines := strings.Split(text, "\n")
	// A trailing fragment without a newline is kept until the next write.
	if n := len(lines); n > 0 {
		last := lines[n-1]
		if last != "" {
			r.mu.Lock()
			r.pending = last
			r.mu.Unlock()
		}
		lines = lines[:n-1]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r.Add("app", guessLevel(line), line)
	}
	return len(p), nil
}

// Flush emits a buffered partial line (if any) at shutdown.
func (r *Ring) Flush() {
	r.mu.Lock()
	line := strings.TrimSpace(r.pending)
	r.pending = ""
	r.mu.Unlock()
	if line != "" {
		r.Add("app", guessLevel(line), line)
	}
}

func guessLevel(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"),
		strings.Contains(lower, "失败"), strings.Contains(lower, "panic"):
		return "error"
	case strings.Contains(lower, "warn"), strings.Contains(lower, "retry"),
		strings.Contains(lower, "retrying"), strings.Contains(lower, "重连"):
		return "warn"
	default:
		return "info"
	}
}
