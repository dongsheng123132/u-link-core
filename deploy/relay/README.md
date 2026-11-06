# Self-hosted U-Link relay (Docker)

10-minute deployment on a small Linux VPS or an internal server.

```bash
cd deploy/relay
cp .env.example .env
# edit .env: long random AGENT_TOKEN / CONTROLLER_TOKEN

mkdir -p tls
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/key.pem -out tls/cert.pem -subj "/CN=relay.example.com" \
  -addext "subjectAltName=DNS:relay.example.com" # replace with your relay hostname
openssl x509 -in tls/cert.pem -noout -fingerprint -sha256

# Pin the printed fingerprint on each client when using this self-signed cert.
docker compose up -d --build
curl -k https://127.0.0.1:8900/health   # ok
```

Point agents at `wss://<host>:8900` and the operator CLI at the same URL with
`CONTROLLER_TOKEN`.

## Hardening

- Keep `CONTROLLER_TOKEN` secret; use one relay per organization/tenant.
- Use a real TLS certificate (or pin the self-signed fingerprint on clients).
- Do not expose the relay to the public internet unless you accept the abuse risk.
- Device-key enforcement is enabled by default. Keep `REQUIRE_DEVICE_KEYS=1`;
  the relay refuses a non-loopback listener without both device keys and TLS.
  For a temporary legacy migration, keep the listener bound to loopback.
- If using the generated self-signed certificate, set `ULINK_TLS_PIN` on every
  agent and controller using the SHA-256 fingerprint from `openssl x509`, shared
  over a trusted channel. Public CA certificates use normal verification.
- Back up nothing on the relay: it is stateless (devices/pending are in memory).

## Health / logs

```bash
docker compose logs -f relay
docker compose restart relay
```
