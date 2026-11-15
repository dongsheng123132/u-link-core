package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestRotatingLogWriterRotatesBeforeExceedingLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	w, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("789")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "789" {
		t.Fatalf("current log = %q, want %q", got, "789")
	}
	if got := readLogFile(t, path+".1"); got != "12345678" {
		t.Fatalf("first backup = %q, want %q", got, "12345678")
	}
}

func TestRotatingLogWriterSplitsOversizedWriteAcrossBoundedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	w, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte(strings.Repeat("x", 25))); err != nil || n != 25 {
		t.Fatalf("Write() = (%d, %v), want (25, nil)", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1", path + ".2", path + ".3"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Size() > 8 {
			t.Errorf("%s is %d bytes, exceeds limit 8", name, info.Size())
		}
	}
}

func TestRotatingLogWriterRetainsOnlyConfiguredBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	w, err := newRotatingLogWriter(path, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"one", "two", "tri", "for", "fiv"} {
		if _, err := w.Write([]byte(entry)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "fiv" {
		t.Fatalf("current log = %q, want fiv", got)
	}
	if got := readLogFile(t, path+".1"); got != "for" {
		t.Fatalf("newest backup = %q, want for", got)
	}
	if got := readLogFile(t, path+".2"); got != "tri" {
		t.Fatalf("oldest retained backup = %q, want tri", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: stat error = %v", err)
	}
}

func TestNewRotatingLogWriterRotatesOversizedStartupLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 12)), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "fresh" {
		t.Fatalf("current log = %q, want fresh", got)
	}
	if got := readLogFile(t, path+".1"); got != strings.Repeat("x", 12) {
		t.Fatalf("startup backup has %d bytes, want original 12", len(got))
	}
}

func TestRotatingLogWriterLocksFileAndClosesPermanently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	first, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRotatingLogWriter(path, 8, 3); err == nil {
		t.Fatal("second writer unexpectedly acquired the same log")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("after close")); err != os.ErrClosed {
		t.Fatalf("Write() after Close() = %v, want os.ErrClosed", err)
	}
	second, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatalf("new writer after close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRotatingLogWriterPreservesEntryWhenRotationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	w, err := newRotatingLogWriter(path, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("old")); err != nil {
		t.Fatal(err)
	}
	blocked := path + ".3"
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("new")); err != nil || n != 3 {
		t.Fatalf("Write() after failed rotation = (%d, %v), want (3, nil)", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "oldnew" {
		t.Fatalf("current log = %q, want oldnew", got)
	}
}

func TestNewRotatingLogWriterContinuesWhenStartupRotationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u-link.log")
	if err := os.WriteFile(path, []byte("oversized"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocked := path + ".3"
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := newRotatingLogWriter(path, 8, 3)
	if err != nil {
		t.Fatalf("writer should continue if the existing log is appendable: %v", err)
	}
	if w.startupRotationErr == nil {
		t.Fatal("expected startup rotation failure to be recorded")
	}
	if _, err := w.Write([]byte("entry")); err != nil {
		t.Fatalf("Write() after startup rotation failure: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "oversizedentry" {
		t.Fatalf("current log = %q, want oversizedentry", got)
	}
}
