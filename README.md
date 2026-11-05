# U-Link Core

> Self-hosted, CLI/MCP-first remote execution for **your own machines** or
> employees who have explicitly consented. No inbound ports, no account, no
> screen recording by default.

U-Link Core is the open-source core of the U-Link family. It lets an operator
(or an AI agent) run commands, move files, audit actions, and diagnose machines
across NAT — using a relay you host yourself.

**Not** a remote desktop. **Not** for controlling customers without consent.
**Not** a public relay service.

## What is included

- `ulink` CLI: `devices / run / audit / doctor / pair / mcp`
- Relay server: WebSocket + TLS, routes `exec` / file push/pull by request ID
- Agent: Windows / macOS / Linux; Ed25519 device key; one-time enrollment code
- MCP stdio server: `ulink_devices / ulink_run / ulink_audit`
- JSON envelope + stable exit codes for AI callers
- Docker Compose for a self-hosted relay
- Basic Windows recipes: printer / SMB share / driver / software diagnosis

## Quick start

### 1. Start the relay

```bash
cd deploy/relay
cp .env.example .env
# edit .env: AGENT_TOKEN, CONTROLLER_TOKEN
mkdir -p tls && openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/key.pem -out tls/cert.pem -subj "/CN=ulink-relay"
docker compose up -d
curl -k https://127.0.0.1:8900/health   # ok
```

### 2. Start an agent on a machine you own

```bash
./ulink-agent --server wss://relay.example.com:8900 --token "$AGENT_TOKEN" \
  --id my-pc --file-roots "$HOME"
```

For a new device, the agent prints a 6-digit code. Approve it from the operator
side:

```bash
ulink --relay wss://relay.example.com:8900 --token "$CONTROLLER_TOKEN" \
  pair approve 123456
```

### 3. Operate

```bash
ulink devices
ulink --json run my-pc -- hostname
ulink audit --limit 20
ulink mcp   # expose to Claude Code / DSH / Codex
```

## Security defaults

- Device ID is bound to an Ed25519 key; a different key for the same ID is rejected.
- New devices need a one-time code; codes expire (10 min) and are rate-limited.
- Push/pull is confined to `--file-roots` (default: home + temp).
- Commands are redacted in audit/log lines for common secret shapes.
- `REQUIRE_DEVICE_KEYS=1` enforces device identity; turn it on after upgrading agents.

See [SECURITY.md](SECURITY.md) and [docs/ENTERPRISE-IT.md](docs/ENTERPRISE-IT.md).

## Enterprise edition

The private U-Link Enterprise edition adds: central console, RBAC/SSO, multi-tenant
MSP support, Windows admin helper, policy/approval flows, screen handoff, audit
export/SIEM, signed installers, and support. The core stays Apache-2.0.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
