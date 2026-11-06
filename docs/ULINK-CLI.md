# U-Link CLI（v0.3：设备身份 + 员工出码/IT 审批）

> 状态：v0.3 可运行 · 2026-11-05
> v0.2 补设备身份；v0.3 补「员工出码 → IT 审批」和文件根目录/TLS pin。
> 愿景与路线图：[PRODUCT-VISION.md](./PRODUCT-VISION.md)；安全与发布注意事项：[SECURITY.md](../SECURITY.md)

## 1. 现在能做什么

| 命令 | 能力 |
|---|---|
| `ulink devices` | 列 relay 在线设备（含设备公钥指纹） |
| `ulink run` | 远端执行，返回 stdout/stderr/exit_code/duration |
| `ulink audit` | 本地 JSONL 审计 |
| `ulink doctor` | 配置、身份、relay 连通性检查 |
| `ulink pair init/show/trust/code` | 本机 Ed25519 身份 + 本地信任列表 |
| `ulink pair issue` | IT 向 relay 申请一次性 6 位码（员工输码；兼容旧流程） |
| `ulink pair approve <code>` | IT 审批员工端显示的 6 位码（企业主流程） |
| `ulink pair revoke <device>` | 吊销设备公钥并断开连接 |
| `ulink screenshot <device>` | 远端截一屏并拉回本地（AI 的“眼睛”） |
| `ulink type <device> --text "..."` | 往远端当前焦点窗口粘贴文本（实验性） |
| `ulink mcp` | MCP stdio server：`ulink_devices / ulink_run / ulink_audit` |
| agent `--pairing-code` | 员工端提交 IT 发的码（旧流程） |
| agent 无码启动 | 员工端显示 6 位码，等 IT `pair approve`（新流程） |

所有命令支持 `--json`；错误与退出码稳定（见 §4）。

**还没做**：持久 shell、目录 sync、端口转发、多机并行、controller 凭据按设备隔离、Windows admin helper。端到端冒烟见 `scripts/e2e-test.sh`。

## 2. 构建与快速开始

```bash
mkdir -p build && go build -o build/ulink ./cmd/ulink
./build/ulink doctor
./build/ulink devices
./build/ulink --json run <device> -- hostname
```

`ulink` 默认复用 U-Link 桌面端 config（`~/Library/Application Support/U-Link/config.json` / `%APPDATA%\U-Link\config.json`）里的 relay 和 controller token；也可以：

```bash
export ULINK_RELAY=wss://relay.example:8900
export ULINK_TOKEN=...
ulink --relay ... --token ... devices
```

CLI 状态在 `~/.ulink/`（`ULINK_HOME` 可覆盖）：

```
~/.ulink/
  config.json      # 可选；不存在时回退 U-Link 桌面 config
  device.key       # Ed25519 私钥 0600
  identity.json    # device_id / public_key / fingerprint / short_code
  trusted.json     # 已信任的对端公钥（本地）
  audit.jsonl      # 每条远程命令一行 JSON
```

## 3. 设备身份与配对（v0.2 核心）

### 3.1 设计

- 每台设备首次启动生成 **Ed25519 密钥对**；私钥只在本机（`~/.uclaw/device.key`，U-Link 可用 `device_key_path` 覆盖）。
- 新设备首次注册有两条路：
  - **员工出码（企业主流程）**：agent 无码启动 → 向 relay 要一个 6 位码显示给员工 → IT `ulink pair approve <code>` 审批。
  - **IT 出码（兼容流程）**：`ulink pair issue` → agent `--pairing-code` 提交。
- 码 5–10 分钟有效、用一次即作废、按 IP 限流。
- relay 发送 **随机 nonce**，设备用私钥签名 `ulink-register\n<device_id>\n<nonce>`，relay 验签后才注册。
- relay 把 **device_id → 公钥** 绑定；同一 ID 换公钥会被拒绝，除非先 `pair revoke`。
- 已绑定设备重连无需配对码，但仍要 nonce 签名（证明私钥还在本机）。
- 漏洞边界：controller token 仍是全局的；E2E 未做；TLS 已支持证书 pin（`--tls-pin` / `ULINK_TLS_PIN` / config `tls_pin`）。

### 3.2 首次配对（新设备）

```bash
# 1) 在已受信任的控制端申请一次性码
ulink pair issue
# pairing code: 529205
# expires at  : 2026-11-04T11:07:12+08:00

# 2) 在新设备上带码启动 agent
./build/agent --server wss://relay:8900 --token <agent-token> \
  --id pc-office --pairing-code 529205

# 3) 控制端确认
ulink devices
# pc-office ... KEY 984bd...
```

U-Link 桌面端：设置接口支持 `pairing_code` / `device_key_path`，也可直接写 config：

```json
{"pairing_code":"529205","device_key_path":"/Users/you/.uclaw/device.key"}
```

### 3.3 员工出码 → IT 审批（企业主流程）

```bash
# 1) 员工双击/启动 agent（不带 --pairing-code）
./build/agent --server wss://relay:8900 --token <agent-token> --id pc-office
# 员工端打印/托盘显示：
#   Enrollment code: 381385 (waiting for IT approval)

# 2) 员工把 6 位码发给 IT

# 3) IT 审批
ulink pair approve 381385
# approved pc-office
# key: 635c...

# 4) 员工端显示 Enrollment approved，随后 online
ulink devices
```

实现约束：码绑定设备公钥、10 分钟过期、一次性；审批必须 controller 权限；员工断开则 pending 自动清除。

### 3.4 吊销与重配对

```bash
ulink pair revoke pc-office    # relay 删公钥 + 断开设备
```

被吊销设备重连会收到 `registration rejected: valid one-time pairing code required`；重新 `pair issue` 并带新码启动即可。设备密钥文件保留，重配对会绑定同一公钥（不必换 key）。

### 3.5 relay 安全默认

设备密钥 + 一次性配对默认开启。relay 默认只监听 `127.0.0.1`；监听非回环地址时必须同时启用设备密钥和 TLS，否则拒绝启动：

```bash
REQUIRE_DEVICE_KEYS=1 CONTROLLER_TOKEN=... AGENT_TOKEN=... \
  TLS_CERT=cert.pem TLS_KEY=key.pem ./server --listen 0.0.0.0 --port 8900
```

临时兼容旧 agent 时，只能绑定回环地址，并显式传 `--require-device-keys=false` 或 `REQUIRE_DEVICE_KEYS=0`；不要将这种迁移模式暴露到网络。

### 3.6 文件根目录与 TLS pin

- agent 的 push/pull 默认限制在 **home + temp**；用 `--file-roots` / `ULINK_FILE_ROOTS` 增删：
  ```bash
  ./build/agent ... --file-roots "$HOME,C:\Users\Public"
  ```
- 自签 relay 用证书 SHA-256 pin，避免盲信：
  ```bash
  # 在 relay 上取指纹
  openssl x509 -in tls/cert.pem -noout -fingerprint -sha256
  # 客户端
  export ULINK_TLS_PIN=AA:BB:...
  ulink --relay wss://relay:8900 --token ... devices
  # agent
  ./build/agent ... --tls-pin AA:BB:...
  ```
  有可信 CA 的证书不需要 pin，客户端默认执行系统证书链和主机名校验。自签证书必须设置 pin；pin 应从 relay 主机获取，并通过可信带外渠道分发。空 pin 不会跳过 TLS 校验。

## 4. 命令细节

### run

```bash
ulink run <device> -- hostname
ulink run <device> --powershell 'Get-Process | Select-Object -First 3'
ulink --json run <device> -- npm test
```

- `--` 后按空格拼接为远端命令；复杂引号用 `--command '...'`。
- `--powershell` 让 Windows agent 走 PowerShell runner。
- 远端退出码非 0：JSON `ok=false`、`error.code=remote_failed`，进程退出 6。

### screenshot / type（视觉与输入）

```bash
ulink screenshot pc-4e73 --out ./shot.png
ulink type pc-4e73 --text "hello from AI"
```

- `screenshot`：agent 跑平台原生截屏（Windows 用 PowerShell，macOS 用 `screencapture`，Linux 用
  `import/gnome-screenshot/scrot`），把 PNG 存临时目录，再用文件通道拉回；远端临时文件自动清理。
  - macOS 首次会弹「屏幕录制」授权；没授权会失败或长时间等待。
  - 帧只是**只读快照**，不是实时流。
- `type`：把文本放进远端剪贴板再粘贴（base64 传输，无引号风险）。
  - macOS 需要「辅助功能/Automation」权限；Windows 需要交互会话 + 前台窗口；Linux 需要 xclip/xdotool。
  - 实验性：会打到**当前焦点窗口**，用之前先确认焦点在哪个应用。

### audit

```bash
ulink audit --limit 50
ulink --json audit --limit 10
```

### doctor

```bash
ulink doctor
ulink --json doctor
```

### mcp

```bash
ulink mcp
```

Claude Code / DSH / Codex 配置：

```json
{
  "mcpServers": {
    "ulink": {
      "command": "/Users/you/claude/远程无头运维工具cli/build/ulink",
      "args": ["mcp"]
    }
  }
}
```

工具：`ulink_devices` / `ulink_run` / `ulink_audit`。

## 5. JSON 契约与退出码

成功：`{"ok":true,"data":{...}}`
失败：`{"ok":false,"error":{"code":"...","message":"...","hint":"..."}}`

| 退出码 | 含义 |
|---:|---|
| 0 | 成功 |
| 2 | 参数/配置错 |
| 3 | 设备离线/连不上 relay |
| 4 | 鉴权失败 |
| 5 | 超时 |
| 6 | 远端命令非 0 退出 |

`run --json` 的 `data`：`session/device/command/stdout/stderr/exit_code/duration_ms`。

## 6. 协议与代码位置

- 设备密钥：[internal/devicekey/key.go](../internal/devicekey/key.go)
- relay 身份/配对：[internal/server/hub.go](../internal/server/hub.go)、[internal/server/handler.go](../internal/server/handler.go)
- agent 注册握手：[internal/agent/client.go](../internal/agent/client.go) `awaitRegister`
- CLI：[cmd/ulink/main.go](../cmd/ulink/main.go)
- 测试：[internal/server/identity_test.go](../internal/server/identity_test.go)（无码拒绝 / 配对成功 / 同 ID 换钥拒绝 / 吊销）

## 7. 下一步（v0.3 及发布闸门）

1. **controller 凭据按设备隔离 + 可吊销**（当前仍是全局 controller token）。
2. **E2E**（TLS pin 已支持；relay 仍可见明文）。
3. 持久 shell（detach/attach）、`sync`、`forward`、多机并行。
4. 桌面端把员工端验证码做成醒目 UI（当前 CLI 打印/托盘日志）。
5. Windows admin helper（打印机/共享/驱动）；屏幕转接。
