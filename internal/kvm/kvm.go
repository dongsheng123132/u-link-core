// Package kvm adapts external IP-KVM devices (PiKVM/KVMD, NanoKVM,
// GL.iNet-style REST HID servers) to a single small interface: grab a
// frame, send keys and mouse events.
//
// The point is to be an *adapter*, not a fork: we speak the devices' own
// HTTP APIs so upstream firmware upgrades do not break us, and so we can
// add vendors without touching their code.
package kvm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Device is the minimal control surface a KVM node must provide.
type Device interface {
	// Screenshot returns one JPEG frame.
	Screenshot(ctx context.Context) ([]byte, error)
	// SendKey presses and releases a single key ("enter", "f2", "a").
	SendKey(ctx context.Context, key string) error
	// Type sends a text string.
	Type(ctx context.Context, text string) error
	// MouseMove moves the pointer by a relative delta (pixels).
	MouseMove(ctx context.Context, dx, dy int) error
	// MouseClick clicks a button ("left", "right", "middle").
	MouseClick(ctx context.Context, button string) error
	// MouseScroll scrolls the wheel (positive = up).
	MouseScroll(ctx context.Context, amount int) error
}

// Config describes one KVM node in the local adapter config file.
type Config struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "kvmd" (PiKVM) or "rest" (NanoKVM/GL.iNet style)
	URL      string `json:"url"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Insecure bool   `json:"insecure,omitempty"` // skip TLS verify (self-signed)
	Token    string `json:"token,omitempty"`    // bearer token for "rest" kind
}

// File is the on-disk adapter config (~/.ulink/kvm.json).
type File struct {
	Devices []Config `json:"devices"`
}

// LoadConfig reads the adapter config; a missing file is not an error.
func LoadConfig(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// New builds a client for one config entry.
func New(cfg Config) (Device, error) {
	switch strings.ToLower(cfg.Kind) {
	case "", "kvmd", "pikvm":
		return &KVMDREST{cfg: cfg}, nil
	case "rest", "nanokvm", "glkvm":
		return &PlainREST{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unknown kvm kind: %s", cfg.Kind)
	}
}

func httpClient(cfg Config) *http.Client {
	tr := &http.Transport{}
	if cfg.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}
}

// KVMDREST speaks the PiKVM / KVMD HTTP API.
type KVMDREST struct {
	cfg Config
}

func (k *KVMDREST) url(path string) string {
	return strings.TrimRight(k.cfg.URL, "/") + path
}

func (k *KVMDREST) do(ctx context.Context, method, path string, body any, out []byte) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.url(path), rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if k.cfg.User != "" {
		req.Header.Set("X-KVMD-User", k.cfg.User)
		req.Header.Set("X-KVMD-Passwd", k.cfg.Password)
	}
	resp, err := httpClient(k.cfg).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if _, err := io.ReadAll(resp.Body); err != nil {
			return err
		}
	}
	return nil
}

// Screenshot implements Device.
func (k *KVMDREST) Screenshot(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url("/api/streamer/snapshot"), nil)
	if err != nil {
		return nil, err
	}
	if k.cfg.User != "" {
		req.Header.Set("X-KVMD-User", k.cfg.User)
		req.Header.Set("X-KVMD-Passwd", k.cfg.Password)
	}
	resp, err := httpClient(k.cfg).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("snapshot: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// SendKey implements Device.
func (k *KVMDREST) SendKey(ctx context.Context, key string) error {
	return k.do(ctx, http.MethodPost, "/api/hid/events/send_key", map[string]any{"key": key}, nil)
}

// Type implements Device.
func (k *KVMDREST) Type(ctx context.Context, text string) error {
	return k.do(ctx, http.MethodPost, "/api/hid/events/send_text", map[string]any{"text": text}, nil)
}

// MouseMove implements Device.
func (k *KVMDREST) MouseMove(ctx context.Context, dx, dy int) error {
	return k.do(ctx, http.MethodPost, "/api/hid/events/send_mouse", map[string]any{
		"move": map[string]int{"dx": dx, "dy": dy},
	}, nil)
}

// MouseClick implements Device.
func (k *KVMDREST) MouseClick(ctx context.Context, button string) error {
	return k.do(ctx, http.MethodPost, "/api/hid/events/send_mouse", map[string]any{
		"click": map[string]any{"button": button},
	}, nil)
}

// MouseScroll implements Device.
func (k *KVMDREST) MouseScroll(ctx context.Context, amount int) error {
	return k.do(ctx, http.MethodPost, "/api/hid/events/send_mouse", map[string]any{
		"wheel": map[string]int{"delta": amount},
	}, nil)
}

// PlainREST speaks the simple REST HID servers used by NanoKVM and the
// glkvm-mcp project (ustreamer snapshot + a tiny HID HTTP server).
type PlainREST struct {
	cfg Config
}

func (p *PlainREST) url(path string) string {
	return strings.TrimRight(p.cfg.URL, "/") + path
}

func (p *PlainREST) do(ctx context.Context, method, path string, body any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.url(path), rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	}
	if p.cfg.User != "" {
		req.SetBasicAuth(p.cfg.User, p.cfg.Password)
	}
	resp, err := httpClient(p.cfg).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

// Screenshot implements Device.
func (p *PlainREST) Screenshot(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url("/screenshot"), nil)
	if err != nil {
		return nil, err
	}
	if p.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	}
	if p.cfg.User != "" {
		req.SetBasicAuth(p.cfg.User, p.cfg.Password)
	}
	resp, err := httpClient(p.cfg).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("screenshot: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// SendKey implements Device.
func (p *PlainREST) SendKey(ctx context.Context, key string) error {
	return p.do(ctx, http.MethodPost, "/keyboard/press", map[string]any{"key": key})
}

// Type implements Device.
func (p *PlainREST) Type(ctx context.Context, text string) error {
	return p.do(ctx, http.MethodPost, "/keyboard/type", map[string]any{"text": text})
}

// MouseMove implements Device.
func (p *PlainREST) MouseMove(ctx context.Context, dx, dy int) error {
	return p.do(ctx, http.MethodPost, "/mouse/move", map[string]any{"dx": dx, "dy": dy})
}

// MouseClick implements Device.
func (p *PlainREST) MouseClick(ctx context.Context, button string) error {
	return p.do(ctx, http.MethodPost, "/mouse/click", map[string]any{"button": button})
}

// MouseScroll implements Device.
func (p *PlainREST) MouseScroll(ctx context.Context, amount int) error {
	return p.do(ctx, http.MethodPost, "/mouse/scroll", map[string]any{"amount": amount})
}
