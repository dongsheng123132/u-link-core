# U-Link recipes (Windows)

Small, deterministic PowerShell recipes for the most common internal-IT tasks.
Every recipe is designed to be called by an AI agent through `ulink run`:

```bash
ulink --json run <device> --powershell -File C:/path/to/printer-list.ps1
```

Rules:

- Prefer **detect → act → verify**; print JSON where practical.
- Mark `needs_admin` / `needs_screen` in the header comment.
- Do not add arbitrary command execution; add a new named recipe instead.
- Dangerous actions (delete share, uninstall driver) must support `-WhatIf` or
  print a dry-run plan first.
- These scripts are examples, not a complete enterprise library.
- `screenshot.ps1` captures one screen for visual diagnosis; it is **not** live
  remote control. Treat screenshots as sensitive: consent + audit + delete after use.
