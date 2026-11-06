package desktop

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/protocol"
	"github.com/dongsheng123132/remote-agent/internal/version"
)

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/ping", a.handlePing)
	mux.HandleFunc("/api/status", a.auth(a.handleStatus))
	mux.HandleFunc("/api/devices", a.auth(a.handleDevices))
	mux.HandleFunc("/api/exec", a.auth(a.handleExec))
	mux.HandleFunc("/api/exec/cancel", a.auth(a.handleExecCancel))
	mux.HandleFunc("/api/logs", a.auth(a.handleLogs))
	mux.HandleFunc("/api/settings", a.auth(a.handleSettings))
	mux.HandleFunc("/api/agent/restart", a.auth(a.handleAgentRestart))
	mux.HandleFunc("/api/install-self", a.auth(a.handleInstallSelf))
	return mux
}

// auth rejects requests that did not originate from our own dashboard.
//
// Threat model (see docs/DESKTOP-APP.md §5): a random web page in the
// user's browser must not be able to drive the local API.  Custom
// headers already force a CORS preflight, but we also pin Host (DNS
// rebinding) and Origin (CSRF) so a misconfigured browser can't leak
// through.
func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.checkLocalRequest(w, r) {
			return
		}
		cfg := a.snapshotConfig()
		got := r.Header.Get("X-ULink-Token")
		if cfg.LocalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(cfg.LocalToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "本地会话已失效，请从托盘菜单重新打开控制台"})
			return
		}
		next(w, r)
	}
}

func (a *App) checkLocalRequest(w http.ResponseWriter, r *http.Request) bool {
	a.mu.RLock()
	port := a.port
	a.mu.RUnlock()
	if port == 0 {
		port = a.snapshotConfig().HTTPPort
	}
	hosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
	}
	if !hosts[r.Host] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden host"})
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		ok := origin == "http://"+fmt.Sprintf("127.0.0.1:%d", port) ||
			origin == "http://"+fmt.Sprintf("localhost:%d", port)
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden origin"})
			return false
		}
	}
	return true
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
	_, _ = w.Write(dashboardHTML)
}

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app":     "u-link",
		"version": version.Version,
	})
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, a.status())
}

func (a *App) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Always refresh on demand: the RPC is one cheap round-trip and the
	// dashboard polls every 10s, so a stale empty cache right after
	// startup would otherwise hide devices that just came online.
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	a.refreshDevices(ctx)
	cancel()

	a.mu.RLock()
	devices := make([]protocol.DeviceInfo, len(a.ctrlDevices))
	copy(devices, a.ctrlDevices)
	ready := a.ctrlReady
	errMsg := a.ctrlErr
	a.mu.RUnlock()

	resp := map[string]any{"devices": devices}
	if !ready && errMsg != "" {
		resp["error"] = errMsg
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Device  string `json:"device"`
		Command string `json:"command"`
		Shell   string `json:"shell"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	req.Device = strings.TrimSpace(req.Device)
	req.Command = strings.TrimSpace(req.Command)
	if req.Device == "" || req.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "设备和命令不能为空"})
		return
	}

	cfg := a.snapshotConfig()
	if cfg.Role == "agent" {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "本机为纯被控端，未启用控制角色"})
		return
	}
	if cfg.ControllerToken == "" {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "未配置控制端口令（设置页填写后即可互控）"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), execTimeout)
	defer cancel()

	stream, err := startExecStream(ctx, cfg, req.Device, req.Command, req.Shell)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": friendlyExecError(err)})
		return
	}
	defer stream.close()

	a.execMu.Lock()
	a.execCancels[stream.reqID] = stream.close
	a.execMu.Unlock()
	defer func() {
		a.execMu.Lock()
		delete(a.execCancels, stream.reqID)
		a.execMu.Unlock()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSSE(w, flusher, "meta", map[string]string{"request_id": stream.reqID})
	a.ring.Add("app", "info", fmt.Sprintf("控制端执行 → %s: %s", req.Device, truncate(req.Command, 200)))

	for ev := range stream.events {
		if ev.Stdout != "" {
			if err := writeSSE(w, flusher, "chunk", map[string]string{"stream": "stdout", "text": ev.Stdout}); err != nil {
				return
			}
		}
		if ev.Stderr != "" {
			text := friendlyExecMessage(ev.Stderr)
			if err := writeSSE(w, flusher, "chunk", map[string]string{"stream": "stderr", "text": text}); err != nil {
				return
			}
		}
		if ev.Err != nil {
			_ = writeSSE(w, flusher, "error", map[string]string{"message": friendlyExecError(ev.Err)})
			return
		}
		if ev.Final {
			_ = writeSSE(w, flusher, "done", map[string]int{"exit_code": ev.ExitCode})
			return
		}
	}
}

func (a *App) handleExecCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	a.execMu.Lock()
	cancel := a.execCancels[req.RequestID]
	a.execMu.Unlock()
	if cancel == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "没有正在执行的命令"})
		return
	}
	cancel()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": a.ring.Snapshot(limit)})
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.snapshotConfig()
		// Never echo token material back.  The dashboard only needs to
		// know whether a value exists.
		writeJSON(w, http.StatusOK, map[string]any{
			"relay":                cfg.Relay,
			"agent_token_set":      cfg.AgentToken != "",
			"controller_token_set": cfg.ControllerToken != "",
			"device_id":            cfg.DeviceID,
			"allow_control":        cfg.AllowControl,
			"autostart":            autostartEnabledFor(cfg),
			"device_prefix_filter": cfg.DevicePrefixFilter,
			"show_all_devices":     cfg.ShowAllDevices,
			"role":                 cfg.Role,
			"http_port":            cfg.HTTPPort,
			"pairing_code_set":     cfg.PairingCode != "",
			"tls_pin":              cfg.TLSPin,
			"device_key_path":      cfg.DeviceKeyPath,
		})
	case http.MethodPost:
		a.handleSettingsUpdate(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (a *App) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Relay              *string `json:"relay"`
		AgentToken         *string `json:"agent_token"`
		ControllerToken    *string `json:"controller_token"`
		DeviceID           *string `json:"device_id"`
		AllowControl       *bool   `json:"allow_control"`
		Autostart          *bool   `json:"autostart"`
		DevicePrefixFilter *string `json:"device_prefix_filter"`
		ShowAllDevices     *bool   `json:"show_all_devices"`
		Role               *string `json:"role"`
		PairingCode        *string `json:"pairing_code"`
		DeviceKeyPath      *string `json:"device_key_path"`
		TLSPin             *string `json:"tls_pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}

	if p.Autostart != nil {
		want := *p.Autostart
		if err := setAutostart(want); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "设置开机自启失败: " + err.Error()})
			return
		}
	}

	tokenChanged := false
	restart, err := a.mutateConfig(func(c *Config) (bool, error) {
		restart := false
		if p.Relay != nil {
			if v := strings.TrimSpace(*p.Relay); v != "" && v != c.Relay {
				c.Relay = v
				restart = true
			}
		}
		if p.AgentToken != nil {
			if v := strings.TrimSpace(*p.AgentToken); v != "" && v != c.AgentToken {
				c.AgentToken = v
				restart = true
			}
		}
		if p.ControllerToken != nil {
			if *p.ControllerToken != c.ControllerToken {
				c.ControllerToken = *p.ControllerToken
				tokenChanged = true
			}
		}
		if p.DeviceID != nil {
			if v := strings.TrimSpace(*p.DeviceID); v != "" && v != c.DeviceID {
				c.DeviceID = v
				restart = true
			}
		}
		if p.AllowControl != nil {
			c.AllowControl = *p.AllowControl
		}
		if p.Autostart != nil {
			c.Autostart = *p.Autostart
		}
		if p.DevicePrefixFilter != nil {
			c.DevicePrefixFilter = strings.TrimSpace(*p.DevicePrefixFilter)
		}
		if p.ShowAllDevices != nil {
			c.ShowAllDevices = *p.ShowAllDevices
		}
		if p.Role != nil {
			switch *p.Role {
			case "both", "agent":
				c.Role = *p.Role
			}
		}
		if p.PairingCode != nil {
			if v := strings.TrimSpace(*p.PairingCode); v != c.PairingCode {
				c.PairingCode = v
				restart = true
			}
		}
		if p.DeviceKeyPath != nil {
			if v := strings.TrimSpace(*p.DeviceKeyPath); v != c.DeviceKeyPath {
				c.DeviceKeyPath = v
				restart = true
			}
		}
		if p.TLSPin != nil {
			// Self-hosted relays usually use a self-signed certificate;
			// without this the agent cannot verify them at all.
			if v := strings.TrimSpace(*p.TLSPin); v != c.TLSPin {
				c.TLSPin = v
				restart = true
			}
		}
		return restart, nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	a.ring.Add("app", "info", "设置已更新")
	if tokenChanged {
		go a.refreshDevices(context.Background())
	}
	if p.AllowControl != nil {
		if *p.AllowControl {
			a.ring.Add("audit", "info", "本机已允许远程控制")
		} else {
			a.ring.Add("audit", "warn", "本机已暂停远程控制")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"restart_required": restart,
	})
}

// handleInstallSelf copies the running bundle into /Applications (the
// app process is not sandboxed, unlike the CLI that launched it) and
// points autostart at the installed copy.  The caller should then quit
// this instance and launch the installed one.
func (a *App) handleInstallSelf(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	dest, err := installSelfBundle()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a.ring.Add("app", "info", "已安装到 "+dest)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"installed_to": dest,
		"next":         "quit this instance and open the installed app",
	})
}

func (a *App) handleAgentRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	a.restartAgent()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// friendlyExecError turns relay/agent plumbing errors into something the
// operator can act on.  The "i/o timeout" case is the notorious zombie
// socket: the relay still lists the device as online, but its socket to
// that device is one-way dead, so every write hangs.  Restarting the
// target-side U-Link/agent is the only client-side recovery.
func friendlyExecError(err error) string {
	if err == nil {
		return ""
	}
	return friendlyExecMessage(err.Error())
}

// friendlyExecMessage is friendlyExecError for raw strings, including
// error text the relay synthesizes into a result's stderr field.
func friendlyExecMessage(s string) string {
	switch {
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "timeout"):
		return "目标设备通道半死（relay 到设备的连接僵死，设备本身可能正常）：请在目标机重启 U-Link / agent.exe 后重试；本机到 relay 的通道是正常的"
	case strings.Contains(s, "device not found"):
		return "目标设备不在线（可能已关机、断网，或 agent 未运行）"
	case strings.Contains(s, "auth failed"):
		return "控制端口令不正确，请在「设置」里检查后重试"
	case strings.Contains(s, "connect failed"):
		return "连接 relay 失败：请检查本机网络/代理设置（U-Link 默认绕代理）"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
