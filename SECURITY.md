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
- The current relay uses a self-signed TLS certificate; clients should pin the
  certificate fingerprint when exposing the relay to the internet.
- No built-in screen/remote-desktop. Use a separate, licensed remote-support tool
  for GUI/UAC-only cases.
- No Windows admin helper in this core edition; printer/share/driver operations
  need an explicit admin path (see `docs/ENTERPRISE-IT.md`).

## Reporting a vulnerability

Email: hefangsheng@gmail.com (PGP not configured).
Please include: affected version/commit, reproduction, impact, and whether the
relay is self-hosted. We aim to acknowledge within 72 hours. Do not open a public
issue for an unfixed vulnerability.

## Hardening checklist

- Set `REQUIRE_DEVICE_KEYS=1` **after** all agents are upgraded.
- Use a strong random `CONTROLLER_TOKEN`; do not commit it.
- Expose the relay only over TLS; pin the certificate fingerprint in clients.
- Put the relay behind a firewall/VPN; do not expose it to the public internet
  unless you understand the abuse risk.
- Restrict `file_roots` on agents to the directories operators actually need.
- Keep audit logs; export them off-box; rotate/retain per policy.
- Uninstall the agent and revoke its key when an employee leaves.
