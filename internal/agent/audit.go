package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// AuditLog records every operator action against the agent for the
// customer's local review.  It writes plain-text lines to a per-day
// session file in %TEMP%\uclaw\sessions\YYYY-MM-DD.log on Windows
// (mirroring agent.ps1's $AGENT_DIR convention) or /tmp/uclaw/...
// elsewhere.  The customer can open this file at any time to see what
// the operator ran.
//
// Why a separate file instead of stdout: the Windows console window
// is also the place where the agent prints its own connection
// chatter, and customers tend to close it (then they can't review the
// session afterwards).  A persistent file is the durable artifact.
type AuditLog struct {
	path  string
	count atomic.Int64
	mu    sync.Mutex
	f     *os.File
}

// auditDir returns the per-user directory for session logs.
func auditDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("TEMP"); dir != "" {
			return filepath.Join(dir, "uclaw", "sessions")
		}
	}
	return filepath.Join(os.TempDir(), "uclaw", "sessions")
}

// NewAuditLog opens (or creates) today's session log file.  Failure to
// open returns an AuditLog with f=nil; subsequent Append calls are
// no-ops, which is the right behavior — auditing is best-effort and
// must never crash the agent on a permission glitch.
func NewAuditLog() *AuditLog {
	dir := auditDir()
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
	a := &AuditLog{path: path}
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		a.f = f
		// Mark session start so customers can see when each daily
		// agent run began.  Adds <50 bytes per session.
		fmt.Fprintf(f, "\n[%s] === session start (pid=%d) ===\n",
			time.Now().Format("15:04:05"), os.Getpid())
	}
	return a
}

// Path returns the absolute audit log file path so the agent can show
// it to the customer on shutdown.
func (a *AuditLog) Path() string { return a.path }

// Count returns how many actions were recorded in this session.
func (a *AuditLog) Count() int64 { return a.count.Load() }

// Append writes one line to the audit log.  Safe to call from
// multiple goroutines.  Failure is silently ignored (best-effort).
func (a *AuditLog) Append(line string) {
	a.count.Add(1)
	if a.f == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(a.f, "[%s] %s\n", time.Now().Format("15:04:05"), line)
	// Sync so the customer can `tail -f` the file in real time and so
	// a sudden process kill doesn't lose the last few lines — auditing
	// at this scale is below the bar where fsync overhead matters.
	_ = a.f.Sync()
}

// Close flushes and closes the file.  Idempotent.
func (a *AuditLog) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f != nil {
		fmt.Fprintf(a.f, "[%s] === session end ===\n", time.Now().Format("15:04:05"))
		_ = a.f.Close()
		a.f = nil
	}
}
