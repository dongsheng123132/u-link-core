package ulink

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndReadAuditTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	for i := 1; i <= 3; i++ {
		e := AuditEntry{
			Time:     time.Unix(int64(i), 0).UTC(),
			Session:  "s",
			Action:   "run",
			Device:   "dev",
			Command:  "cmd",
			ExitCode: i,
			OK:       i == 0,
		}
		if err := AppendAudit(path, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	entries, err := ReadAuditTail(path, 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].ExitCode != 2 || entries[1].ExitCode != 3 {
		t.Fatalf("want [2 3], got [%d %d]", entries[0].ExitCode, entries[1].ExitCode)
	}
}

func TestReadAuditTailMissingFile(t *testing.T) {
	entries, err := ReadAuditTail(filepath.Join(t.TempDir(), "missing.jsonl"), 5)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty, got %d", len(entries))
	}
}

func TestAppendAuditPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "audit.jsonl")
	if err := AppendAudit(path, AuditEntry{Action: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("audit file is too open: %v", info.Mode().Perm())
	}
}
