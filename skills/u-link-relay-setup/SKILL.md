---
name: u-link-relay-setup
description: 部署一套自己的 U-Link relay（Docker + 自签/受信任 TLS + 两个 token + 设备密钥强制），并给客户端分发 TLS pin。触发词：部署 relay、自建中继、u-link relay、relay 怎么装、中继搭不起来、TLS pin 怎么拿。
---

# 部署你自己的 U-Link relay

U-Link Core 是**自托管**的：没有官方公共 relay，每台机器都主动 wss 连**你自己**起的 relay。这套部署约 10 分钟，只需要一台能开 8900 端口的 Linux 机器（VPS 或内网服务器）。

## 1. 前置

- Docker + docker compose
- 一个能被客户端解析到的地址：域名优先；用 IP 也可以，但证书必须带对应 IP SAN
- openssl

仓库里的 `deploy/relay/` 已经写好 Dockerfile、compose 和 `.env.example`。

## 2. 部署

```bash
git clone https://github.com/dongsheng123132/u-link-core.git
cd u-link-core/deploy/relay
cp .env.example .env
```

编辑 `.env`，把两个占位 token 换成随机长字符串：

```ini
AGENT_TOKEN=<随机长串>        # 只用于「注册为设备」，会分发给每台机器
CONTROLLER_TOKEN=<随机长串>   # 控制权限，只给运维自己，绝不外发
REQUIRE_DEVICE_KEYS=1          # 强制 Ed25519 设备密钥 + 审批配对码
```

生成证书。**证书名必须覆盖客户端实际拨号的地址**（`subjectAltName`）：

```bash
mkdir -p tls
# 用域名：
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/key.pem -out tls/cert.pem -subj "/CN=relay.example.com" \
  -addext "subjectAltName=DNS:relay.example.com"
# 或用 IP（IP 变化时证书要重签）：
#   -addext "subjectAltName=IP:192.168.1.4"

openssl x509 -in tls/cert.pem -noout -fingerprint -sha256   # 记下这串指纹
docker compose up -d --build
curl -k https://127.0.0.1:8900/health                      # ok
```

把指纹通过**可信渠道**发给每台客户端，作为 `ULINK_TLS_PIN`（或控制台「设置 → TLS 证书指纹」）。pin 模式会同时校验证书指纹和拨号主机名；证书 SAN 与地址不匹配会直接拒绝。用受信任 CA 签发的证书时无需 pin，留空即可走系统证书校验。

## 3. 客户端

每台机器跑 agent（公开版桌面程序也会自带 agent 角色）：

```bash
ulink-agent --server wss://relay.example.com:8900 --token "$AGENT_TOKEN" \
  --id my-pc --tls-pin <上面那串指纹>
```

首次注册会显示 6 位配对码，运维在自己的机器上审批：

```bash
ulink --relay wss://relay.example.com:8900 --token "$CONTROLLER_TOKEN" \
  pair approve <6位码>
ulink devices     # 应该能看到对方在线
```

桌面版用户不用手敲上面这些：控制台「设置」填好 relay / 两个 token / TLS pin，「添加设备」会生成另一台机器的命令，「一键自检」会逐项检查连通性。

## 4. 安全默认值（不要关）

- relay 默认监听 `127.0.0.1`；compose 里显式改成 `0.0.0.0` 对外。
- **非回环监听必须同时启用 TLS 和设备密钥**，否则服务端直接拒绝启动。
- `REQUIRE_DEVICE_KEYS=0` 只允许在回环调试时用，不能暴露到网络。
- relay 是无状态的（设备/待审批都在内存），重启后需要重新配对。
- 详细令牌权限边界见 [SECURITY.md](../SECURITY.md#token-scope)。

## 5. 常见故障

| 症状 | 原因 | 处置 |
|---|---|---|
| `certificate signed by unknown authority` | 自签证书没配 pin | 客户端填 `ULINK_TLS_PIN` |
| `tls pin mismatch` | 证书换过 / 指纹抄错 | 重新执行 `openssl x509 -fingerprint -sha256` |
| `tls pin hostname mismatch` | 证书 SAN 不含客户端用的地址 | 重签证书，SAN 写全域名/IP；IP 变了要重签 |
| `invalid token` | agent/controller token 填反或不一致 | 对照 relay 的 `.env` |
| 一直 `连线中` | 代理 / 防火墙挡了 wss | 打开「绕过系统代理」，确认 8900 可达 |
| 设备 ID 换公钥被拒 | 该 ID 已绑定别的密钥 | 先 `pair revoke <id>` 再重新配对 |

完整参考：`deploy/relay/README.md` · `docs/DESKTOP-APP.md` · `SECURITY.md`。
