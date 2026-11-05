# KVM adapter (PiKVM / NanoKVM)

`ulink` can drive external **IP-KVM** nodes — the small devices that grab a
machine's HDMI output and inject USB keyboard/mouse. They are the only way to
reach a machine that has not booted into an OS (BIOS/boot menu, black screen).

This is an **adapter**, not a fork: we speak each device's own HTTP API, so
upstream firmware upgrades do not break us.

## Supported backends

| kind | Device | Frame | Input |
|---|---|---|---|
| `kvmd` | PiKVM (KVMD) | `GET /api/streamer/snapshot` | `POST /api/hid/events/send_key\|send_text\|send_mouse` |
| `rest` | NanoKVM / GL.iNet-style HID servers | `GET /screenshot` | `POST /keyboard/type\|press`, `/mouse/move\|click\|scroll` |

## Configure

`~/.ulink/kvm.json`:

```json
{"devices":[
  {"id":"pi-lab","kind":"kvmd","url":"https://pikvm.lan","user":"admin","password":"admin","insecure":true},
  {"id":"nano-1","kind":"rest","url":"https://nanokvm.lan","insecure":true}
]}
```

## Use

```bash
ulink kvm list
ulink kvm shot pi-lab --out frame.jpg     # one JPEG frame
ulink kvm key pi-lab f2                    # press F2 (BIOS setup key)
ulink kvm type pi-lab --text "hello"       # type text
```

## Safety

Driving a BIOS/boot menu is high risk: a wrong keystroke can change boot
order, disable Secure Boot or wipe a disk. Recommended rules:

- treat a frame as **untrusted input** (prompt-injection risk);
- approve the *specific* action against the *current* frame; if the screen or
  focus changed since approval, the approval is void;
- never let an agent self-approve UAC or destructive actions;
- keep an audit trail of frames and actions.

This adapter is the plumbing only. The policy layer (approval, budgets,
audit) belongs to your deployment.
