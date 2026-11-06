package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/agent"
	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/version"
)

// Options are the command-line overrides accepted by cmd/link.
type Options struct {
	ConfigDir       string
	Headless        bool
	Open            bool
	Relay           string
	AgentToken      string
	ControllerToken string
	DeviceID        string
	Port            int
	Version         string
}

// App is the long-lived U-Link process: agent role + controller role +
// local dashboard + tray.
type App struct {
	opts     Options
	cfgPath  string
	lockPath string
	ring     *Ring
	logFile  *os.File
	firstRun bool

	cfgMu sync.RWMutex
	cfg   Config

	mu               sync.RWMutex
	agentState       agent.ConnState
	agentErr         string
	agentConnectedAt time.Time
	agentReconnects  int
	ctrlReady        bool
	ctrlErr          string
	ctrlDevices      []protocol.DeviceInfo
	ctrlCheckedAt    time.Time
	port             int
	trayActive       bool
	startedAt        time.Time

	agentMu     sync.Mutex
	agentClient *agent.Client
	agentCancel context.CancelFunc

	ctrlCancel context.CancelFunc

	execMu      sync.Mutex
	execCancels map[string]func()

	httpSrv  *http.Server
	trayMu   sync.RWMutex
	traySync func()
	quitOnce sync.Once
	quitCh   chan struct{}
}

// Run starts U-Link and blocks until the tray quits (or, in headless
// mode, until SIGINT/SIGTERM).
func Run(opts Options) error {
	dir, err := ConfigDir(opts.ConfigDir)
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "config.json")
	_, statErr := os.Stat(cfgPath)
	firstRun := os.IsNotExist(statErr)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	applyOverrides(cfg, opts)

	// Single instance: if another U-Link owns the lock file and answers
	// the local ping, just open its dashboard and exit quietly.  Users
	// double-click the app icon twice all the time.
	lockPath := filepath.Join(dir, "u-link.lock")
	if url, ok := findRunning(lockPath, cfg.LocalToken); ok {
		_ = openBrowser(url)
		return nil
	}

	logDir, err := LogDir(dir)
	if err != nil {
		return err
	}
	ring := NewRing(600)
	app := &App{
		opts:        opts,
		cfgPath:     cfgPath,
		lockPath:    lockPath,
		ring:        ring,
		firstRun:    firstRun,
		startedAt:   time.Now(),
		execCancels: make(map[string]func()),
		quitCh:      make(chan struct{}),
	}
	app.cfg = *cfg
	app.agentState = agent.ConnStateConnecting

	if err := app.setupLogging(logDir); err != nil {
		return err
	}
	log.Printf("U-Link %s starting (config: %s)", version.Version, cfgPath)

	if err := app.startHTTP(); err != nil {
		return err
	}
	if err := app.writeLock(); err != nil {
		log.Printf("warning: 写单实例锁失败: %v", err)
	}
	app.startAgent()
	app.startControllerPoller()

	if opts.Headless {
		log.Printf("headless 模式，控制台: %s", app.DashboardURL())
		app.waitForSignal()
		return nil
	}
	// macOS 26 sometimes does not lay out new NSStatusItems until
	// ControlCenter re-runs; do it once per install, in the background.
	go ensureMenuBarVisibleOnce(filepath.Dir(cfgPath))
	return runTray(app)
}

func applyOverrides(cfg *Config, opts Options) {
	if opts.Relay != "" {
		cfg.Relay = opts.Relay
	}
	if opts.AgentToken != "" {
		cfg.AgentToken = opts.AgentToken
	}
	if opts.ControllerToken != "" {
		cfg.ControllerToken = opts.ControllerToken
	}
	if opts.DeviceID != "" {
		cfg.DeviceID = opts.DeviceID
	}
	if opts.Port != 0 {
		cfg.HTTPPort = opts.Port
	}
	cfg.Normalize()
}

func (a *App) setupLogging(logDir string) error {
	path := filepath.Join(logDir, "u-link.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}
	a.logFile = f
	// Order matters: the file and the ring must be written even if
	// stderr is a closed handle (Windows GUI builds have no console).
	log.SetOutput(io.MultiWriter(f, a.ring, os.Stderr))
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("")
	return nil
}

// Shutdown tears everything down.  Idempotent.
func (a *App) shutdown() {
	a.quitOnce.Do(func() {
		close(a.quitCh)
		if a.httpSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.httpSrv.Shutdown(ctx)
			cancel()
		}
		a.agentMu.Lock()
		if a.agentCancel != nil {
			a.agentCancel()
		}
		if a.agentClient != nil {
			a.agentClient.Close()
		}
		a.agentMu.Unlock()
		if a.ctrlCancel != nil {
			a.ctrlCancel()
		}
		a.ring.Flush()
		if a.logFile != nil {
			_ = a.logFile.Close()
		}
		_ = os.Remove(a.lockPath)
	})
}

func (a *App) waitForSignal() {
	ch := make(chan os.Signal, 1)
	notifyTermination(ch)
	select {
	case <-ch:
	case <-a.quitCh:
	}
	a.shutdown()
}

func (a *App) snapshotConfig() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

// mutateConfig applies fn to a copy of the config, persists it, and
// restarts the agent if fn says the change requires reconnecting.
func (a *App) mutateConfig(fn func(*Config) (restart bool, err error)) (bool, error) {
	a.cfgMu.Lock()
	next := a.cfg
	restart, err := fn(&next)
	if err != nil {
		a.cfgMu.Unlock()
		return false, err
	}
	next.Normalize()
	if err := next.Save(a.cfgPath); err != nil {
		a.cfgMu.Unlock()
		return false, err
	}
	a.cfg = next
	a.cfgMu.Unlock()

	if restart {
		a.startAgent()
	}
	a.notifyTray()
	return restart, nil
}

func (a *App) startAgent() {
	cfg := a.snapshotConfig()

	// Self-hosted installs start unconfigured.  Report that instead of
	// spinning on a reconnect loop against an empty URL.
	if strings.TrimSpace(cfg.Relay) == "" || strings.TrimSpace(cfg.AgentToken) == "" {
		a.mu.Lock()
		a.agentState = agent.ConnStateOffline
		a.agentErr = "未配置中继地址或被控端令牌，请在控制台「设置」中填写"
		a.mu.Unlock()
		a.notifyTray()
		return
	}

	a.agentMu.Lock()
	oldCancel := a.agentCancel
	oldClient := a.agentClient

	ctx, cancel := context.WithCancel(context.Background())
	client := agent.NewClient(agentEndpoint(cfg.Relay), cfg.AgentToken, cfg.DeviceID)
	client.SetBypassProxy(cfg.BypassProxy)
	if cfg.PairingCode != "" {
		client.SetPairingCode(cfg.PairingCode)
	}
	if cfg.DeviceKeyPath != "" {
		client.SetDeviceKeyPath(cfg.DeviceKeyPath)
	}
	if len(cfg.FileRoots) > 0 {
		client.SetFileRoots(cfg.FileRoots)
	}
	if cfg.TLSPin != "" {
		client.SetTLSPin(cfg.TLSPin)
	}
	client.SetControlPolicy(func() bool { return a.snapshotConfig().AllowControl })
	client.SetAuditHook(func(line string) { a.ring.AddAudit(line) })
	client.SetEnrollCallback(func(code string, expires time.Time) {
		a.ring.Add("app", "info", fmt.Sprintf("员工端验证码：%s（%s 前有效）", code, expires.Local().Format("15:04:05")))
	})
	client.SetStateCallback(func(s agent.ConnState, msg string) {
		a.mu.Lock()
		prev := a.agentState
		a.agentState = s
		if msg != "" {
			a.agentErr = msg
		} else if s == agent.ConnStateOnline {
			a.agentErr = ""
		}
		if s == agent.ConnStateOnline {
			a.agentConnectedAt = time.Now()
		}
		if s == agent.ConnStateOffline && prev == agent.ConnStateOnline {
			a.agentReconnects++
		}
		a.mu.Unlock()
		a.notifyTray()
	})

	a.agentClient = client
	a.agentCancel = cancel
	a.mu.Lock()
	a.agentState = agent.ConnStateConnecting
	a.mu.Unlock()
	a.agentMu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldClient != nil {
		oldClient.Close()
	}

	go client.RunContext(ctx)
	a.notifyTray()
}

func (a *App) startControllerPoller() {
	ctx, cancel := context.WithCancel(context.Background())
	a.ctrlCancel = cancel
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		a.refreshDevices(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refreshDevices(ctx)
			}
		}
	}()
}

func (a *App) refreshDevices(ctx context.Context) {
	cfg := a.snapshotConfig()
	if cfg.Role == "agent" {
		a.setControllerState(false, "本机为纯被控端（未启用控制角色）", nil)
		return
	}
	if cfg.ControllerToken == "" {
		a.setControllerState(false, "未配置控制端口令", nil)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	devices, err := listDevices(cctx, cfg)
	if err != nil {
		a.setControllerState(false, err.Error(), nil)
		return
	}
	a.setControllerState(true, "", filterDevices(cfg, devices))
}

func (a *App) setControllerState(ready bool, errMsg string, devices []protocol.DeviceInfo) {
	a.mu.Lock()
	a.ctrlReady = ready
	a.ctrlErr = errMsg
	if devices != nil {
		a.ctrlDevices = devices
	} else if errMsg != "" {
		// Keep the last known list so the UI can still show it offline.
	}
	a.ctrlCheckedAt = time.Now()
	a.mu.Unlock()
	a.notifyTray()
}

func filterDevices(cfg Config, devices []protocol.DeviceInfo) []protocol.DeviceInfo {
	if cfg.ShowAllDevices || strings.TrimSpace(cfg.DevicePrefixFilter) == "" {
		return devices
	}
	var prefixes []string
	for _, p := range strings.Split(cfg.DevicePrefixFilter, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			prefixes = append(prefixes, p)
		}
	}
	if len(prefixes) == 0 {
		return devices
	}
	out := make([]protocol.DeviceInfo, 0, len(devices))
	for _, d := range devices {
		id := strings.ToLower(d.ID)
		for _, p := range prefixes {
			if strings.HasPrefix(id, p) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// Status is the JSON payload served to the dashboard (docs/DESKTOP-APP.md §6).
type Status struct {
	Version    string           `json:"version"`
	Port       int              `json:"port"`
	Tray       bool             `json:"tray"`
	StartedAt  time.Time        `json:"started_at"`
	RepoURL    string           `json:"repo_url"`
	Agent      AgentStatus      `json:"agent"`
	Controller ControllerStatus `json:"controller"`
	Settings   StatusSettings   `json:"settings"`
}

type AgentStatus struct {
	State        string     `json:"state"`
	DeviceID     string     `json:"device_id"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Relay        string     `json:"relay"`
	AllowControl bool       `json:"allow_control"`
	AuditCount   int64      `json:"audit_count"`
	ConnectedAt  *time.Time `json:"connected_at,omitempty"`
	LastError    string     `json:"last_error"`
	Reconnects   int        `json:"reconnects"`
}

type ControllerStatus struct {
	Enabled     bool       `json:"enabled"`
	Ready       bool       `json:"ready"`
	Error       string     `json:"error"`
	DeviceCount int        `json:"device_count"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
}

type StatusSettings struct {
	Autostart          bool   `json:"autostart"`
	DevicePrefixFilter string `json:"device_prefix_filter"`
	Role               string `json:"role"`
}

func (a *App) status() Status {
	cfg := a.snapshotConfig()

	a.mu.RLock()
	state := a.agentState
	agentErr := a.agentErr
	connectedAt := a.agentConnectedAt
	reconnects := a.agentReconnects
	ready := a.ctrlReady
	ctrlErr := a.ctrlErr
	deviceCount := len(a.ctrlDevices)
	checkedAt := a.ctrlCheckedAt
	port := a.port
	tray := a.trayActive
	a.mu.RUnlock()

	stateStr := "offline"
	switch state {
	case agent.ConnStateOnline:
		if cfg.AllowControl {
			stateStr = "online"
		} else {
			stateStr = "paused"
		}
	case agent.ConnStateConnecting:
		stateStr = "connecting"
	}

	hostname, _ := os.Hostname()
	var connectedPtr *time.Time
	if !connectedAt.IsZero() {
		t := connectedAt
		connectedPtr = &t
	}
	var checkedPtr *time.Time
	if !checkedAt.IsZero() {
		t := checkedAt
		checkedPtr = &t
	}

	a.agentMu.Lock()
	auditCount := int64(0)
	if a.agentClient != nil {
		auditCount = a.agentClient.AuditCount()
	}
	a.agentMu.Unlock()

	return Status{
		Version:   version.Version,
		Port:      port,
		Tray:      tray,
		StartedAt: a.startedAt,
		RepoURL:   repoURL(),
		Agent: AgentStatus{
			State:        stateStr,
			DeviceID:     cfg.DeviceID,
			Hostname:     hostname,
			OS:           runtime.GOOS,
			Relay:        cfg.Relay,
			AllowControl: cfg.AllowControl,
			AuditCount:   auditCount,
			ConnectedAt:  connectedPtr,
			LastError:    agentErr,
			Reconnects:   reconnects,
		},
		Controller: ControllerStatus{
			Enabled:     cfg.ControllerToken != "",
			Ready:       ready,
			Error:       ctrlErr,
			DeviceCount: deviceCount,
			CheckedAt:   checkedPtr,
		},
		Settings: StatusSettings{
			Autostart:          cfg.Autostart,
			DevicePrefixFilter: cfg.DevicePrefixFilter,
			Role:               cfg.Role,
		},
	}
}

func (a *App) dashboardURL() string {
	a.mu.RLock()
	port := a.port
	a.mu.RUnlock()
	cfg := a.snapshotConfig()
	return fmt.Sprintf("http://127.0.0.1:%d/#t=%s", port, cfg.LocalToken)
}

// DashboardURL is exported for cmd/link --print-url.
func (a *App) DashboardURL() string { return a.dashboardURL() }

func (a *App) openDashboard() {
	if err := openBrowser(a.dashboardURL()); err != nil {
		a.ring.Add("app", "error", "打开控制台失败: "+err.Error())
	}
}

func (a *App) setTrayState(active bool, sync func()) {
	a.mu.Lock()
	a.trayActive = active
	a.mu.Unlock()
	a.trayMu.Lock()
	a.traySync = sync
	a.trayMu.Unlock()
}

func (a *App) notifyTray() {
	a.trayMu.RLock()
	fn := a.traySync
	a.trayMu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (a *App) restartAgent() {
	a.ring.Add("app", "info", "手动重启连线")
	a.startAgent()
}

func (a *App) toggleAllowControl() {
	_, err := a.mutateConfig(func(c *Config) (bool, error) {
		c.AllowControl = !c.AllowControl
		return false, nil
	})
	if err != nil {
		a.ring.Add("app", "error", "切换允许被控制失败: "+err.Error())
		return
	}
	cfg := a.snapshotConfig()
	if cfg.AllowControl {
		a.ring.Add("audit", "info", "本机已允许远程控制")
	} else {
		a.ring.Add("audit", "warn", "本机已暂停远程控制")
	}
}

func (a *App) toggleAutostart() {
	want := !autostartEnabledFor(a.snapshotConfig())
	if err := setAutostart(want); err != nil {
		a.ring.Add("app", "error", "设置开机自启失败: "+err.Error())
		a.notifyTray()
		return
	}
	_, err := a.mutateConfig(func(c *Config) (bool, error) {
		c.Autostart = want
		return false, nil
	})
	if err != nil {
		a.ring.Add("app", "error", "保存开机自启配置失败: "+err.Error())
		return
	}
	if want {
		a.ring.Add("app", "info", "已开启开机自启")
	} else {
		a.ring.Add("app", "info", "已关闭开机自启")
	}
}

func (a *App) copyDeviceID() {
	id := a.snapshotConfig().DeviceID
	if err := setClipboard(id); err != nil {
		a.ring.Add("app", "error", "复制设备 ID 失败: "+err.Error())
		return
	}
	a.ring.Add("app", "info", "已复制设备 ID: "+id)
}

func (a *App) writeLock() error {
	meta := lockMeta{Port: 0, PID: os.Getpid()}
	a.mu.RLock()
	meta.Port = a.port
	a.mu.RUnlock()
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(a.lockPath, data, 0o600)
}

type lockMeta struct {
	Port int `json:"port"`
	PID  int `json:"pid"`
}

func findRunning(lockPath, localToken string) (string, bool) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "", false
	}
	var meta lockMeta
	if err := json.Unmarshal(data, &meta); err != nil || meta.Port <= 0 {
		return "", false
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/ping", meta.Port)
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return fmt.Sprintf("http://127.0.0.1:%d/#t=%s", meta.Port, localToken), true
}

func (a *App) startHTTP() error {
	cfg := a.snapshotConfig()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("本地控制台端口监听失败: %w", err)
		}
		log.Printf("端口 %d 被占用，控制台改用随机端口", cfg.HTTPPort)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	a.mu.Lock()
	a.port = port
	a.mu.Unlock()
	a.httpSrv = &http.Server{Handler: a.routes()}
	go func() {
		if err := a.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("本地控制台退出: %v", err)
		}
	}()
	log.Printf("本地控制台: http://127.0.0.1:%d/", port)
	return nil
}

// autostartEnabledFor prefers the platform's actual state over the
// config copy, so a login-item removed by hand shows up as off.
func autostartEnabledFor(cfg Config) bool {
	if !platformAutostartSupported() {
		return cfg.Autostart
	}
	return platformAutostartEnabled()
}

// SetAutostartEnabled is the CLI face of the tray toggle.
func SetAutostartEnabled(enabled bool) error {
	if !platformAutostartSupported() {
		return fmt.Errorf("当前平台暂不支持开机自启")
	}
	return setAutostart(enabled)
}

// ConfiguredDashboardURL reads the config + single-instance lock and
// returns the dashboard URL of the already-running instance.
func ConfiguredDashboardURL(opts Options) (string, error) {
	dir, err := ConfigDir(opts.ConfigDir)
	if err != nil {
		return "", err
	}
	cfg, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "u-link.lock"))
	if err != nil {
		return "", fmt.Errorf("U-Link 似乎没有在运行（找不到锁文件）")
	}
	var meta lockMeta
	if err := json.Unmarshal(data, &meta); err != nil || meta.Port <= 0 {
		return "", fmt.Errorf("锁文件损坏，请重新启动 U-Link")
	}
	return fmt.Sprintf("http://127.0.0.1:%d/#t=%s", meta.Port, cfg.LocalToken), nil
}
