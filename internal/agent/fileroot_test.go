package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFilePathConfinesRoots(t *testing.T) {
	root := t.TempDir()
	c := &Client{fileRoots: []string{root}}
	inside := filepath.Join(root, "logs", "app.log")
	if got, err := c.resolveFilePath(inside); err != nil || got != inside {
		t.Fatalf("inside path rejected: %q %v", got, err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if _, err := c.resolveFilePath(outside); err == nil {
		t.Fatal("outside path accepted")
	}
	if _, err := c.resolveFilePath(filepath.Join(root, "..", "escape")); err == nil {
		t.Fatal("parent traversal accepted")
	}
}

func TestResolveFilePathLegacy(t *testing.T) {
	c := &Client{}
	p := filepath.Join(t.TempDir(), "file")
	got, err := c.resolveFilePath(p)
	if err != nil {
		t.Fatalf("legacy path rejected: %v", err)
	}
	abs, _ := filepath.Abs(p)
	if got != abs {
		t.Fatalf("want %q, got %q", abs, got)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("unexpected file: %v", err)
	}
}
