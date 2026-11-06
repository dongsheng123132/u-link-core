package desktop

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dongsheng123132/remote-agent/internal/tlspin"
)

// DoctorCheck is one line of the self-check report shown in the dashboard.
type DoctorCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
	statusSkip = "skip"
)

func relayAddresses(relay string) (host, port string, secure bool, err error) {
	u, err := url.Parse(strings.TrimSpace(relay))
	if err != nil || u.Host == "" {
		return "", "", false, fmt.Errorf("中继地址格式不对")
	}
	host = u.Hostname()
	port = u.Port()
	secure = u.Scheme == "wss" || u.Scheme == "https"
	if port == "" {
		if secure {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port, secure, nil
}

// doctorRelayTLS proves the relay is reachable and that the configured pin (or
// system trust, when no pin is set) actually accepts its certificate.
func doctorRelayTLS(ctx context.Context, relay, pin string) error {
	host, port, secure, err := relayAddresses(relay)
	if err != nil {
		return err
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("连不上 %s:%s：%w", host, port, err)
	}
	defer raw.Close()
	if !secure {
		return nil
	}
	conn := tls.Client(raw, tlspin.Config(relay, pin))
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}
	return nil
}

// doctor runs the checks behind the dashboard's self-check card.  It never
// mutates state and every failure carries a concrete next step.
func (a *App) doctor(ctx context.Context) []DoctorCheck {
	cfg := a.snapshotConfig()
	checks := make([]DoctorCheck, 0, 8)

	add := func(id, label, status, detail, hint string) {
		checks = append(checks, DoctorCheck{ID: id, Label: label, Status: status, Detail: detail, Hint: hint})
	}

	// 1. Relay configured.
	switch {
	case strings.TrimSpace(cfg.Relay) == "":
		add("relay", "中继地址", statusFail, "未配置", "在「设置」里填 wss://你的中继:8900")
	default:
		add("relay", "中继地址", statusOK, cfg.Relay, "")
	}

	// 2. Relay reachability + TLS trust (pin or system CAs).
	if strings.TrimSpace(cfg.Relay) != "" {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := doctorRelayTLS(cctx, cfg.Relay, cfg.TLSPin)
		cancel()
		switch {
		case err == nil:
			add("relay_tls", "中继连通 / TLS", statusOK, "已建立 TLS 连接", "")
		case cfg.TLSPin == "":
			add("relay_tls", "中继连通 / TLS", statusFail, err.Error(),
				"自签名证书要在「设置」里填 TLS 证书指纹；受信任 CA 证书则无需填写")
		default:
			add("relay_tls", "中继连通 / TLS", statusFail, err.Error(),
				"核对证书指纹是否与 relay 上 `openssl x509 -fingerprint -sha256` 一致，并确认证书覆盖该地址")
		}
	}

	// 3. Agent token.
	if strings.TrimSpace(cfg.AgentToken) == "" {
		add("agent_token", "被控端令牌", statusFail, "未配置", "填 relay 的 AGENT_TOKEN，本机才能注册")
	} else {
		add("agent_token", "被控端令牌", statusOK, "已配置", "")
	}

	// 4. Agent connection state.
	a.mu.RLock()
	state, agentErr := a.agentState, a.agentErr
	a.mu.RUnlock()
	switch state {
	case "online":
		add("agent", "本机注册状态", statusOK, "在线（"+cfg.DeviceID+"）", "")
	case "connecting":
		add("agent", "本机注册状态", statusWarn, "连线中", "稍等；若一直停在此状态，看下一条原因")
	default:
		detail := "离线"
		if agentErr != "" {
			detail = agentErr
		}
		add("agent", "本机注册状态", statusWarn, detail, "若刚改过设置，点「重启连线」")
	}

	// 5. Controller token + a real RPC.
	if strings.TrimSpace(cfg.ControllerToken) == "" {
		add("controller_token", "控制端令牌", statusWarn, "未配置（只能被控，不能控制别人）", "需要控制其他设备时填 relay 的 CONTROLLER_TOKEN")
	} else if strings.TrimSpace(cfg.Relay) != "" {
		cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		devices, err := listDevices(cctx, cfg)
		cancel()
		if err != nil {
			add("controller", "控制端验证", statusFail, err.Error(), "确认控制端令牌与 relay 的 CONTROLLER_TOKEN 一致")
		} else {
			add("controller", "控制端验证", statusOK, fmt.Sprintf("已获取 %d 台在线设备", len(devices)), "")
		}
	}

	// 6. Proxy hygiene: a system proxy is the most common silent cause of
	// wss:// failures on developer machines.
	proxy := firstNonEmpty(os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy"), os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy"))
	if proxy == "" {
		add("proxy", "系统代理", statusOK, "未设置", "")
	} else if cfg.BypassProxy {
		add("proxy", "系统代理", statusOK, "已检测到代理，U-Link 直连中继（已绕过）", "")
	} else {
		add("proxy", "系统代理", statusWarn, "检测到代理："+proxy, "连不上中继时在设置里打开「绕过系统代理」")
	}

	// 7. Local dashboard port.
	a.mu.RLock()
	port := a.port
	a.mu.RUnlock()
	if port == 0 {
		add("local_port", "本地控制台", statusWarn, "未监听", "重启应用后重试")
	} else {
		add("local_port", "本地控制台", statusOK, fmt.Sprintf("127.0.0.1:%d", port), "")
	}

	return checks
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// enrollCommand returns the command a user runs on the next machine.  It only
// needs the public agent token, never the controller token.
func (a *App) enrollCommand(deviceID string) string {
	cfg := a.snapshotConfig()
	if strings.TrimSpace(cfg.Relay) == "" || strings.TrimSpace(cfg.AgentToken) == "" {
		return ""
	}
	id := strings.TrimSpace(deviceID)
	if id == "" {
		id = "my-second-machine"
	}
	cmd := fmt.Sprintf("ulink-agent --server %s --token %s --id %s", cfg.Relay, cfg.AgentToken, id)
	if cfg.TLSPin != "" {
		cmd += " --tls-pin " + cfg.TLSPin
	}
	return cmd
}
