# Security Policy

## What this is

U-Link Core is a self-hosted remote-execution fabric for **your own devices or
employees who have explicitly consented**. It is not a remote desktop, not a
customer backdoor, and not a public relay service.

## Threat model (what we defend)

- Device impersonation: every device proves possession of an Ed25519 key; the
  relay binds device ID → public key and rejects a different key for the same ID.
- Unauthorized enrollment: a new device needs a one-time code, approved by an
  operator (or issued by an operator, depending on the flow).
- Relay compromise: TLS is required; payload end-to-end encryption is on the
  roadmap. Do not run the relay on an untrusted host.
- Operator mistakes: commands are audited; dangerous recipes should be previewed
  and approved. Keep the audit log and export it to your SIEM.

## What we do NOT defend (yet)

- A compromised operator token can control every device on that relay. Use a
  dedicated relay per organization/tenant; rotate tokens; restrict network access.
- The relay does not provide end-to-end encryption. A relay operator can inspect
  and potentially alter relayed protocol traffic; TLS protects each client-to-relay
  connection, not the payload from the relay.
- With a self-signed TLS certificate, clients must pin its SHA-256 fingerprint.
  Obtain and distribute the fingerprint over a trusted out-of-band channel.
- No built-in screen/remote-desktop. Use a separate, licensed remote-support tool
  for GUI/UAC-only cases.
- No Windows admin helper in this core edition; printer/share/driver operations
  need an explicit admin path (see `docs/ENTERPRISE-IT.md`).

## Acceptable use

Use U-Link only on devices you own or on devices whose users explicitly
consented to remote administration. Do not use it for covert access,
persistence, surveillance, or control of third-party devices without permission.

## Reporting a vulnerability

Email: hefangsheng@gmail.com (PGP not configured).
Please include: affected version/commit, reproduction, impact, and whether the
relay is self-hosted. We aim to acknowledge within 72 hours. Do not open a public
issue for an unfixed vulnerability.

## Hardening checklist

- Device keys and operator-approved pairing are enabled by default. The relay
  refuses non-loopback listeners unless device keys and TLS are both enabled.
  For a temporary legacy migration, bind to loopback only and explicitly disable
  device-key enforcement.
- Use a strong random `AGENT_TOKEN` and `CONTROLLER_TOKEN`; do not commit them.
- For publicly trusted TLS certificates, clients use normal certificate and
  hostname verification. For self-signed certificates, set `ULINK_TLS_PIN` on
  every client using a fingerprint obtained over a trusted channel.
- Put the relay behind a firewall/VPN; do not expose it to the public internet
  unless you understand the abuse risk.
- Restrict `file_roots` on agents to the directories operators actually need.
- Keep audit logs; export them off-box; rotate/retain per policy.
- Uninstall the agent and revoke its key when an employee leaves.
