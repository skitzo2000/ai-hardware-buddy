---
name: hardware-buddy
description: Bridges Claude Code to a Claude-XXXX BLE pet device (ESP32 firmware). The plugin ships a Go MCP server that holds the BLE link and exposes tools for approval prompts, session status, transcripts and token pushes. Trigger on "hardware buddy", "claude pet", "approve from device", "buddy device", "CYD pet", "ESP32 buddy", or anything referencing a Claude-XXXX BLE peripheral.
---

# Hardware Buddy

A small companion device — an ESP32 with a colour TFT — that mirrors
Claude Code's current session state and surfaces permission prompts you
can tap through. Replaces the Claude Desktop app's built-in Hardware
Buddy GUI for users on Linux (and anywhere Claude Code runs).

## When to use this skill

Invoke when the user:

- Has flashed the [claude-desktop-buddy CYD port](https://github.com/skitzo2000/claude-desktop-buddy)
  (or the upstream M5StickC firmware).
- Asks for approval prompts to land on the device, session counts to
  ride the pet's mood, or transcript snippets to scroll in the HUD.
- References a "Claude-XXXX" BLE peripheral, the CYD, or wants to pair
  Linux with the buddy.

## How it works

The plugin includes:

- **MCP server** (``bin/hwbuddy``, a Go binary) — declared in the
  plugin's ``.mcp.json``. Claude Code launches one instance per session
  and keeps it alive. It holds the persistent BLE connection.
- **Hooks** in ``hooks/hooks.json`` — fire on every Claude Code event
  (SessionStart, PreToolUse, PostToolUse, UserPromptSubmit, Stop, …).
  Each hook invokes ``hwbuddy event`` (or ``hwbuddy approve`` for
  PreToolUse) which talks to the running MCP server over a Unix socket
  side channel.
- **MCP tools** — also callable by Claude directly when it needs to
  push status or trigger a test prompt from a tool-use call.

```
┌──────────────┐   PreToolUse / events    ┌───────────┐   BLE NUS   ┌────────────┐
│ Claude Code  ├─────────────────────────►│ hwbuddy   │◄───────────►│  ESP32     │
│  (session)   │  stdio + unix socket     │ MCP srv   │  JSON lines │  pet       │
└──────────────┘                          └───────────┘             └────────────┘
```

## Setup checklist (one-time)

1. **Flash the firmware.** See
   [skitzo2000/claude-desktop-buddy](https://github.com/skitzo2000/claude-desktop-buddy)
   branch ``cyd``. On first boot the device runs a 4-corner touch
   calibration UI — tap each green crosshair.

2. **Pair the device with your OS.** Linux:
   ```bash
   bluetoothctl
   > agent KeyboardOnly
   > default-agent
   > scan le
   # wait for [NEW] Device EC:E3:XX:XX:XX:XX Claude-XXXX
   > scan off
   > pair EC:E3:XX:XX:XX:XX
   # type the 6-digit passkey shown on the device
   > trust EC:E3:XX:XX:XX:XX
   > exit
   ```
   macOS: System Settings → Bluetooth → pair with passkey display.

3. **Install the plugin.** In Claude Code:
   ```
   /plugin marketplace add https://github.com/skitzo2000/ai-hardware-buddy
   /plugin install hardware-buddy@ai-hardware-buddy
   ```
   Restart ``claude`` to load. No edits to ``~/.claude/settings.json``
   required.

4. **(Optional) set $HWBUDDY_ADDRESS** in your shell rc to your device's
   MAC. The MCP server auto-connects at startup instead of waiting for
   a manual ``/hwb-connect``.

## Slash commands

| Command | What it does |
|---|---|
| ``/hwb-ping`` | Server liveness + BLE state. |
| ``/hwb-connect [--address MAC]`` | Attach the MCP server's BLE link. |
| ``/hwb-status [...]`` | Manually push session counters + msg. |
| ``/hwb-tokens [--transcript PATH]`` | Parse transcript, push tokens. |
| ``/hwb-test`` | Fake approval prompt — verify touch zones. |

## Wire protocol (for reference)

Nordic UART Service. Newline-delimited JSON, both directions.

- Service ``6e400001-b5a3-f393-e0a9-e50e24dcca9e``
- RX (host → device, write): ``6e400002-…``
- TX (device → host, notify): ``6e400003-…``

Outgoing message shapes live in ``internal/protocol/protocol.go``.
Incoming acks documented in the firmware's ``src/data.h`` + ``src/xfer.h``.

## Silent passthrough

If the MCP server is down or the device is offline, every hook returns
exit 0 (allow / proceed). Unplugging the buddy must never break a Claude
Code session.
