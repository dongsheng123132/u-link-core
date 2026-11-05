# Self-hosted U-Link relay (Docker)

10-minute deployment on a small Linux VPS or an internal server.

```bash
cd deploy/relay
cp .env.example .env
# edit .env: long random AGENT_TOKEN / CONTROLLER_TOKEN

mkdir -p tls
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/key.pem -out tls/cert.pem -subj "/CN=ulink-relay"

docker compose up -d --build
curl -k https://127.0.0.1:8900/health   # ok
```

Point agents at `wss://<host>:8900` and the operator CLI at the same URL with
`CONTROLLER_TOKEN`.

## Hardening

- Keep `CONTROLLER_TOKEN` secret; use one relay per organization/tenant.
- Use a real TLS certificate (or pin the self-signed fingerprint on clients).
- Do not expose the relay to the public internet unless you accept the abuse risk.
- Turn on `REQUIRE_DEVICE_KEYS=1` only after every agent has a device key.
- Back up nothing on the relay: it is stateless (devices/pending are in memory).

## Health / logs

```bash
docker compose logs -f relay
docker compose restart relay
```
