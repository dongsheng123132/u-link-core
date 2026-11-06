package desktop

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// U-Link Core is self-hosted: a fresh install ships with no relay and
	// no tokens.  Point it at your own relay and tokens in the dashboard
	// "设置" card (or config.json) before it can register or control.
	defaultHTTPPort = 17421
)

// Config is the on-disk configuration for one U-Link installation.
//
// ControllerToken is the sensitive one: it grants control over every
// device registered on the relay.  It is stored in a 0600 file and is
// never echoed back by the local API, never compiled into the binary,
// and never shipped to customer machines (see docs/DESKTOP-APP.md §5).
type Config struct {
	Relay              string   `json:"relay"`
	AgentToken         string   `json:"agent_token"`
	ControllerToken    string   `json:"controller_token"`
	DeviceID           string   `json:"device_id"`
	AllowControl       bool     `json:"allow_control"`
	Autostart          bool     `json:"autostart"`
	HTTPPort           int      `json:"http_port"`
	LocalToken         string   `json:"local_token"`
	DevicePrefixFilter string   `json:"device_prefix_filter"`
	ShowAllDevices     bool     `json:"show_all_devices"`
	Role               string   `json:"role"`                      // "both" | "agent" (agent = no controller capability)
	LANFallback        bool     `json:"lan_fallback"`              // reserved for the phase-2 direct-connect path
	BypassProxy        bool     `json:"bypass_proxy"`              // dial the relay directly even if HTTP(S)_PROXY is set
	PairingCode        string   `json:"pairing_code,omitempty"`    // one-time code for first registration
	DeviceKeyPath      string   `json:"device_key_path,omitempty"` // override Ed25519 key path
	FileRoots          []string `json:"file_roots,omitempty"`      // allowed push/pull dirs; empty = home + temp
	TLSPin             string   `json:"tls_pin,omitempty"`         // relay cert SHA-256 pin
}

// DefaultConfig returns the values a fresh install starts with.
func DefaultConfig() *Config {
	return &Config{
		AllowControl:   true,
		ShowAllDevices: true,
		HTTPPort:       defaultHTTPPort,
		Role:           "both",
		BypassProxy:    true,
	}
}

// Normalize fills in anything missing.  It is safe to call repeatedly.
func (c *Config) Normalize() {
	if c.DeviceID == "" {
		c.DeviceID = defaultDeviceID()
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = defaultHTTPPort
	}
	if c.LocalToken == "" {
		c.LocalToken = randomToken()
	}
	if c.Role == "" {
		c.Role = "both"
	}
	c.DevicePrefixFilter = strings.TrimSpace(c.DevicePrefixFilter)
}

// ConfigDir resolves (and creates) the per-user config directory.
func ConfigDir(override string) (string, error) {
	dir := override
	if dir == "" {
		dir = os.Getenv("ULINK_CONFIG_DIR")
	}
	if dir == "" {
		switch runtime.GOOS {
		case "darwin":
			home, herr := os.UserHomeDir()
			if herr != nil {
				return "", herr
			}
			dir = filepath.Join(home, "Library", "Application Support", "U-Link")
		case "windows":
			base, herr := os.UserConfigDir() // %APPDATA%
			if herr != nil || base == "" {
				return "", fmt.Errorf("无法定位 APPDATA: %v", herr)
			}
			dir = filepath.Join(base, "U-Link")
		default:
			base, herr := os.UserConfigDir()
			if herr != nil {
				return "", herr
			}
			dir = filepath.Join(base, "u-link")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir, nil
	}
	return abs, nil
}

// LogDir resolves (and creates) the log directory.  macOS keeps logs in
// ~/Library/Logs (Console.app picks them up); Windows keeps them next to
// the config in %LOCALAPPDATA%\U-Link\logs.
func LogDir(configDir string) (string, error) {
	dir := os.Getenv("ULINK_LOG_DIR")
	if dir == "" {
		switch runtime.GOOS {
		case "darwin":
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, "Library", "Logs", "U-Link")
		case "windows":
			base := os.Getenv("LOCALAPPDATA")
			if base == "" {
				base = configDir
			}
			dir = filepath.Join(base, "U-Link", "logs")
		default:
			dir = filepath.Join(configDir, "logs")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建日志目录失败: %w", err)
	}
	return dir, nil
}

// LoadConfig reads config.json, creating it with defaults on first run.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
	case os.IsNotExist(err):
		// first run: fall through, Normalize + Save below
	default:
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg.Normalize()
	if err := cfg.Save(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes config.json atomically with 0600 permissions.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	return nil
}

// defaultDeviceID builds a stable, human-readable ID.  The "u-link:"
// salt keeps it distinct from the irm|iex agent's pc-XXXX ID on the same
// machine, so both can run without overwriting each other in the relay's
// device table.
func defaultDeviceID() string {
	host, _ := os.Hostname()
	prefix := "lnx"
	switch runtime.GOOS {
	case "darwin":
		prefix = "mac"
	case "windows":
		prefix = "pc"
	}
	sum := sha1.Sum([]byte("u-link:" + host))
	return fmt.Sprintf("%s-%x", prefix, sum[:2])
}

func randomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is fatal for security, but a zero token
		// is better than a crash loop on a broken machine: the API will
		// simply be unreachable and the user can regenerate config.
		return ""
	}
	return hex.EncodeToString(buf)
}
