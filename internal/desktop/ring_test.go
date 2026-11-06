package desktop

import (
	"strings"
	"testing"
)

func TestRingDropsOldest(t *testing.T) {
	r := NewRing(3)
	for _, s := range []string{"a", "b", "c", "d"} {
		r.Add("app", "info", s)
	}
	entries := r.Snapshot(0)
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3", len(entries))
	}
	if entries[0].Text != "b" || entries[2].Text != "d" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if got := r.Snapshot(2); len(got) != 2 || got[0].Text != "c" {
		t.Fatalf("limit snapshot wrong: %+v", got)
	}
}

func TestRingWriterHandlesSplitLines(t *testing.T) {
	r := NewRing(10)
	if _, err := r.Write([]byte("first line\nsec")); err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshot(0); len(got) != 1 || got[0].Text != "first line" {
		t.Fatalf("partial write handled wrong: %+v", got)
	}
	if _, err := r.Write([]byte("ond line\nthird\n")); err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot(0)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %+v", got)
	}
	if got[1].Text != "second line" {
		t.Fatalf("split line not reassembled: %+v", got)
	}
}

func TestRingLevelHeuristic(t *testing.T) {
	r := NewRing(10)
	r.Write([]byte("connected to relay\n"))
	r.Write([]byte("connection failed: timeout\n"))
	entries := r.Snapshot(0)
	if entries[0].Level != "info" || entries[1].Level != "error" {
		t.Fatalf("unexpected levels: %+v", entries)
	}
}

func TestRingAuditKind(t *testing.T) {
	r := NewRing(10)
	r.AddAudit("EXEC req=abc cmd=hostname")
	entries := r.Snapshot(0)
	if len(entries) != 1 || entries[0].Kind != "audit" {
		t.Fatalf("audit entry wrong: %+v", entries)
	}
	if !strings.Contains(entries[0].Text, "hostname") {
		t.Fatalf("text lost: %+v", entries)
	}
}
