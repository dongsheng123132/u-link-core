# Contributing to U-Link Core

Thanks for helping. This is a small, self-hosted tool that runs commands on
machines you own — so contributions that keep it **safe, auditable and
unobtrusive** are the most valuable.

## Ground rules

1. **Never weaken the security defaults silently.** Device keys, pairing
   codes, file-root confinement, audit and redaction exist for a reason. If you
   need to change one, open an issue first and explain the threat model.
2. **No telemetry.** No analytics, no phone-home, no account. The relay is
   yours.
3. **Keep the protocol boring.** One flat JSON message struct, additive
   message types, backward compatibility for old agents.
4. **CLI is the product.** Every command should work with `--json`, stable exit
   codes and no interactive prompts.
5. **Platform parity.** macOS, Windows and Linux. Avoid platform-only features
   unless there is a `--shell`/`--os` escape hatch.

## Good first contributions

- A new recipe under `recipes/windows/` (printer / share / driver / software).
- A new KVM backend in `internal/kvm` (PiKVM/KVMD and REST style are done).
- A fix for quoting, encoding or path handling on Windows.
- Docs in your language (README translations are welcome).

## Development

```bash
make ulink          # build the CLI
go test ./...
go vet ./...
./scripts/e2e-test.sh   # relay + agent + controller end-to-end (Linux/macOS)
```

## Pull requests

- One topic per PR; keep the diff readable.
- Describe the **behaviour change** and how you verified it.
- If you touch the wire protocol, say explicitly whether old agents keep working.
- Security-sensitive changes: please describe the attack you prevent.

## Reporting vulnerabilities

Do **not** open a public issue. See [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your work is licensed under Apache-2.0.
