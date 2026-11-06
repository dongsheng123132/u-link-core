# U-Link Desktop (menu bar / tray)

`cmd/link` builds the same relay, agent and controller from this repository into
a resident desktop app:

- **macOS**: a menu bar item with a status dot (green = online, amber = paused,
  gray = connecting, red = offline) that opens the local dashboard.
- **Windows**: a notification-area (tray) icon with the same menu.

It is not a remote desktop. It is a small control surface for the machines you
have paired, aimed at "run a command / look at the screen / read the audit log"
without keeping a terminal open.

## Requirements

| Platform | Build requirement |
|---|---|
| macOS | Go 1.26+, cgo enabled, Xcode Command Line Tools (`xcode-select --install`) |
| Windows | Go 1.26+ (pure Go, no cgo) |
| Linux | Go 1.26+ (headless build; no tray integration) |

## Build

```bash
# macOS menu bar app (needs cgo)
make link                 # -> build/u-link

# Windows tray app (cross-compile is fine)
make link-windows         # -> build/u-link.exe

# macOS .app bundle + zip for distribution
make link-package-mac     # -> build/U-Link.app, build/U-Link-mac-<arch>.zip
```

Run the macOS binary directly, or double-click the packaged `.app`. On Windows
copy `build/u-link.exe` anywhere writable and run it.

## First run

A fresh install ships **unconfigured** — no relay, no tokens. Until both are
filled in, the app reports `未配置中继地址或被控端令牌` and does not dial
anything.

1. Start (or reuse) a relay from this repository — see
   [deploy/relay](../deploy/relay/README.md).
2. Open the dashboard (menu bar/tray → 打开控制台) and fill in **设置**:

   | Field | Meaning |
   |---|---|
   | 中继地址 | `wss://your-relay:8900` |
   | 被控端令牌 | relay's `AGENT_TOKEN` — lets this machine register |
   | 控制端令牌 | relay's `CONTROLLER_TOKEN` — lets this machine control others |
   | 本机设备 ID | stable ID such as `mac-a1b2` (optional) |
   | TLS pin | SHA-256 fingerprint of a self-signed relay certificate |

3. Save. The app reconnects and the status dot turns green.

For a self-signed relay, set the TLS pin to the fingerprint printed by
`openssl x509 -in tls/cert.pem -noout -fingerprint -sha256`. Pin mode also
checks that the certificate is valid for the relay address you dialed, so an
IP-addressed relay needs an IP SAN in its certificate.

## Guided setup, self-check and adding a device

Fresh installs are unconfigured, so the dashboard leads with a **首次配置**
card that tracks these steps (it disappears once they are done):

1. 中继地址
2. 被控端令牌
3. 控制端令牌
4. TLS 证书指纹 (optional — only for self-signed relays)
5. 连上中继 (status dot turns green)
6. 添加并配对第二台设备

Two helper cards sit next to it:

- **一键自检** runs a read-only check of relay reachability, TLS trust (pin or
  system CAs), both tokens, the local registration state, a real controller
  RPC, proxy settings and the local port. Each line shows the result plus a
  concrete next step, and the report can be copied to clipboard.
- **添加设备** generates the exact command to run on the next machine, for
  example:

  ```bash
  ulink-agent --server wss://relay.example.com:8900 --token <AGENT_TOKEN> \
    --id win-office --tls-pin <SHA256>
  ```

  It contains the public agent token only — never the controller token. Place
  the `ulink-agent` binary on the target machine first, run the command, then
  approve the 6-digit code from the operator side
  (`ulink pair approve <code>` or the dashboard).

A healthy install shows every self-check line green. The most common failures
and what they mean:

| Check | Typical failure | Fix |
|---|---|---|
| 中继连通 / TLS | `certificate signed by unknown authority` | fill the TLS pin for a self-signed relay |
| 中继连通 / TLS | `tls pin mismatch` | re-copy the fingerprint from `openssl x509 -fingerprint -sha256` |
| 控制端验证 | `invalid token` | controller token does not match `CONTROLLER_TOKEN` |
| 本机注册状态 | stuck at 连线中 | wrong agent token, or the relay is not reachable |
| 系统代理 | warns with a proxy URL | turn on 「绕过系统代理」 if the relay is on a local/LAN address |

## Menu

```
● 在线 · mac-a1b2        (disabled status line, refreshes live)
──────────────────
打开控制台
复制本机 ID
设备列表（3 台在线）
允许被控制        ✓
开机自启          ✓
──────────────────
重启连线
──────────────────
报告问题（GitHub）
提交 PR（GitHub）
──────────────────
退出 U-Link
```

The two GitHub entries open the project's issue and pull-request pages. Set
`ULINK_REPO_URL` to point them at a fork or a private build.

## Where state lives

| Platform | Config | Logs |
|---|---|---|
| macOS | `~/Library/Application Support/U-Link/config.json` | `~/Library/Logs/U-Link/` |
| Windows | `%APPDATA%\U-Link\config.json` | `%LOCALAPPDATA%\U-Link\logs\` |

`config.json` is written with `0600` permissions because it holds the controller
token. The controller token is never compiled into the binary and never returned
by the local API.

## Local dashboard API

The dashboard is a single embedded HTML file served from `127.0.0.1` (default
port `17421`). Every `/api` request must carry the local token, which the app
puts in the URL fragment when it opens the browser and which the page then
stores in `sessionStorage`.

Endpoints:

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/status` | version, port, repo URL, agent state, controller state, settings summary |
| GET | `/api/devices` | online devices |
| POST | `/api/exec` | run a command, streamed as server-sent events |
| POST | `/api/exec/cancel` | cancel a running command |
| GET | `/api/logs?limit=N` | recent app + audit log lines |
| GET/POST | `/api/settings` | read/write configuration (tokens are write-only) |
| POST | `/api/agent/restart` | reconnect the agent |

## Security notes

- The dashboard binds to loopback only and rejects requests whose `Host` or
  `Origin` is not the local dashboard (DNS-rebinding / CSRF defence).
- The local token is generated per install and stored in `config.json`.
- Pairing still goes through the relay's device-key flow: a new device needs an
  operator-approved one-time code.
- 「允许被控制」defaults to on, so a freshly paired machine accepts commands
  immediately. Turn it off (tray menu or 设置) on machines that should only
  *issue* commands.
- The controller token is optional: without it the app still registers as an
  agent but cannot control other devices. Set `role` to `agent` in
  `config.json` to make that explicit and hide the controller side entirely.
- Push/pull is restricted to `file_roots` (default: home + temp).

## Uninstall

- macOS: quit from the menu, then remove `/Applications/U-Link.app` and the
  LaunchAgent at `~/Library/LaunchAgents/org.uclaw.u-link.plist`.
- Windows: quit from the tray, delete `%LOCALAPPDATA%\U-Link`, and remove the
  `ULink` value under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.
