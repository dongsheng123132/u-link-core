package desktop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	defaultLogMaxBytes = 10 << 20
	defaultLogBackups  = 3
)

// rotatingLogWriter rotates the desktop log at a size threshold and retains
// recent history. Writes are serialized both within and across processes.
type rotatingLogWriter struct {
	mu                 sync.Mutex
	path               string
	file               *os.File
	lockFile           *os.File
	unlock             func() error
	size               int64
	maxBytes           int64
	backups            int
	startupRotationErr error
	closed             bool
}

func newRotatingLogWriter(path string, maxBytes int64, backups int) (*rotatingLogWriter, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("log max size must be positive")
	}
	if backups < 0 {
		return nil, fmt.Errorf("log backup count cannot be negative")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log lock: %w", err)
	}
	unlock, err := acquireLogLock(lockFile)
	if err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("another process is writing this log: %w", err)
	}
	w := &rotatingLogWriter{
		path: path, lockFile: lockFile, unlock: unlock,
		maxBytes: maxBytes, backups: backups,
	}
	cleanup := func() {
		_ = w.unlock()
		_ = w.lockFile.Close()
	}

	if info, err := os.Stat(path); err == nil && info.Size() >= maxBytes {
		if err := rotateLogFiles(path, backups); err != nil {
			w.startupRotationErr = fmt.Errorf("rotate oversized log on startup: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		cleanup()
		return nil, fmt.Errorf("stat log file: %w", err)
	}
	if err := w.openAppend(); err != nil {
		cleanup()
		return nil, err
	}
	return w, nil
}

func (w *rotatingLogWriter) openAppend() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("restrict log file permissions: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat open log file: %w", err)
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file == nil {
		if err := w.openAppend(); err != nil {
			return 0, err
		}
	}

	written := 0
	for written < len(p) {
		if w.size >= w.maxBytes {
			if err := w.rotate(); err != nil {
				return w.writeRemainder(p, written, err)
			}
		}
		room := w.maxBytes - w.size
		chunk := int64(len(p) - written)
		if chunk > room {
			chunk = room
		}
		n, err := w.file.Write(p[written : written+int(chunk)])
		w.size += int64(n)
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

// If rotation fails but the current file can still be opened, preserve the
// log entry there; its size may exceed the threshold until a later rotation.
func (w *rotatingLogWriter) writeRemainder(p []byte, written int, rotateErr error) (int, error) {
	if w.file == nil {
		return written, rotateErr
	}
	remaining := len(p) - written
	n, err := w.file.Write(p[written:])
	w.size += int64(n)
	written += n
	if err != nil {
		return written, err
	}
	if n != remaining { // Defensive guard for a short write.
		return written, io.ErrShortWrite
	}
	return written, nil
}

func (w *rotatingLogWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			w.file = nil
			return fmt.Errorf("close log before rotation: %w", err)
		}
		w.file = nil
	}
	if err := rotateLogFiles(w.path, w.backups); err != nil {
		_ = w.openAppend()
		return fmt.Errorf("rotate log files: %w", err)
	}
	return w.openAppend()
}

func rotateLogFiles(path string, backups int) error {
	if backups == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	oldest := fmt.Sprintf("%s.%d", path, backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := backups - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		to := fmt.Sprintf("%s.%d", path, i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
		w.file = nil
	}
	if w.unlock != nil {
		if err := w.unlock(); closeErr == nil {
			closeErr = err
		}
		w.unlock = nil
	}
	if w.lockFile != nil {
		if err := w.lockFile.Close(); closeErr == nil {
			closeErr = err
		}
		w.lockFile = nil
	}
	return closeErr
}
