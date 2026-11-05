package kvm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKVMDREST(t *testing.T) {
	var lastPath, lastBody, lastUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		lastUser = r.Header.Get("X-KVMD-User")
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		switch r.URL.Path {
		case "/api/streamer/snapshot":
			w.Write([]byte("JPEGDATA"))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	d, err := New(Config{ID: "pi1", Kind: "kvmd", URL: srv.URL, User: "admin", Password: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	frame, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(frame) != "JPEGDATA" || lastPath != "/api/streamer/snapshot" {
		t.Fatalf("snapshot: %q %s", frame, lastPath)
	}
	if lastUser != "admin" {
		t.Fatalf("auth header missing: %q", lastUser)
	}
	if err := d.SendKey(ctx, "f2"); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/api/hid/events/send_key" {
		t.Fatalf("send_key path: %s", lastPath)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lastBody), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["key"] != "f2" {
		t.Fatalf("send_key payload: %s", lastBody)
	}
	if err := d.MouseClick(ctx, "left"); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/api/hid/events/send_mouse" {
		t.Fatalf("send_mouse path: %s", lastPath)
	}
	if err := d.Type(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/api/hid/events/send_text" {
		t.Fatalf("send_text path: %s", lastPath)
	}
}

func TestPlainREST(t *testing.T) {
	var lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		if r.URL.Path == "/screenshot" {
			w.Write([]byte("FRAME"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d, err := New(Config{ID: "nano1", Kind: "rest", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	frame, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(frame) != "FRAME" {
		t.Fatalf("frame: %q", frame)
	}
	if err := d.SendKey(ctx, "enter"); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/keyboard/press" {
		t.Fatalf("key path: %s", lastPath)
	}
	if err := d.MouseScroll(ctx, -3); err != nil {
		t.Fatal(err)
	}
	if lastPath != "/mouse/scroll" {
		t.Fatalf("scroll path: %s", lastPath)
	}
	if lastBody == "" {
		t.Fatal("empty body")
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "nope"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestLoadConfigMissing(t *testing.T) {
	f, err := LoadConfig("/tmp/does-not-exist-ulink-kvm.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Devices) != 0 {
		t.Fatal("expected empty device list")
	}
}
