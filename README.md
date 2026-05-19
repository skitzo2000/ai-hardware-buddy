# ai-hardware-buddy

A Claude Code plugin that mirrors your session — running tools,
permission prompts, transcripts, token usage — to a small ESP32 BLE
device with a TFT screen. Originally a port of Anthropic's
[`claude-desktop-buddy`](https://github.com/anthropics/claude-desktop-buddy)
firmware; this project drives that firmware from **Claude Code** (the
CLI / IDE-extension / web), replacing the Claude Desktop app's built-in
Hardware Buddy GUI.

Works on **Linux, macOS, and Windows** — the plugin ships a single Go
binary, no separate runtime install. MIT licensed.

## Companion project: claude-desktop-buddy

Two repos, one pet.

This repo is the **Claude Code plugin** — the bits that let the buddy
approve tool calls, mirror sessions, and track tokens for your CLI
sessions. Its sibling is the **firmware** that runs on the device
itself:

- **M5StickC Plus** — Anthropic's original buddy device. Firmware at
  [anthropics/claude-desktop-buddy](https://github.com/anthropics/claude-desktop-buddy).
- **CYD ESP32-2432S028R** — ~$15 cheap-yellow-display board with a
  touchscreen. The port lives at
  [skitzo2000/claude-desktop-buddy](https://github.com/skitzo2000/claude-desktop-buddy)
  and ships a one-click
  [**web flasher**](https://skitzo2000.github.io/claude-desktop-buddy/) —
  no toolchain required.

|                | M5StickC Plus                          | CYD ESP32                                |
|----------------|----------------------------------------|------------------------------------------|
| Claude Desktop | Anthropic's built-in Hardware Buddy    | Web flasher → Claude Desktop GUI         |
| Claude Code    | This plugin                            | Web flasher → this plugin                |

**Each side runs standalone.** The CYD firmware works with Claude
Desktop's GUI without this plugin, and this plugin works with any
board running buddy firmware — you don't need the CYD if you already
have an M5.

## What it does

- Surfaces Claude Code permission prompts on the device — tap-to-approve
  via a real `PreToolUse` hook.
- Mirrors session lifecycle (`SessionStart`, `UserPromptSubmit`,
  `PostToolUse`, `Stop`, `Notification`, `PreCompact`, `SubagentStop`) —
  the pet's mood + HUD reflect what Claude is doing.
- Pushes token usage parsed from Claude Code's transcript JSONL —
  cumulative, today, and the rolling 5-hour quota window.
- Auto-recovers from BLE drops when you move rooms or power-cycle the
  device.
- Self-contained: the plugin's `bin/hwbuddy` is a single statically-built
  Go binary that runs as both the MCP server and the hook-side CLI.

## Install

Two steps the first time (plus one-time pairing).

### 1. Pair your device with the OS (one-time)

Linux:

```bash
bluetoothctl
[bluetoothctl]> agent KeyboardOnly
[bluetoothctl]> default-agent
[bluetoothctl]> scan le
# wait for [NEW] Device EC:E3:XX:XX:XX:XX Claude-XXXX
[bluetoothctl]> scan off
[bluetoothctl]> pair EC:E3:XX:XX:XX:XX
# device shows a 6-digit passkey; type it
[bluetoothctl]> trust EC:E3:XX:XX:XX:XX
[bluetoothctl]> exit
```

macOS: System Settings → Bluetooth → pair with passkey display.

### 2. Install the plugin

In Claude Code:

```
/plugin marketplace add https://github.com/skitzo2000/ai-hardware-buddy
/plugin install hardware-buddy@ai-hardware-buddy
```

Or from a local checkout:

```
/plugin marketplace add /path/to/ai-hardware-buddy
/plugin install hardware-buddy@ai-hardware-buddy
```

Restart `claude` so the plugin's hooks load and the MCP server starts.
**No edits to your `~/.claude/settings.json` are required.**

### 3. (Optional) `$HWBUDDY_ADDRESS` for auto-attach

If you have a single buddy on a stable identifier, set this in your shell
rc:

```bash
# linux / windows — colon-separated MAC
export HWBUDDY_ADDRESS=EC:E3:XX:XX:XX:XX

# macOS — CoreBluetooth never exposes the peer MAC; use the system-assigned
# peripheral UUID instead (read it out of `/hwb-ping` after a successful
# discover-by-name connect, or skip this step entirely and let the scan flow
# pick the device up by its `Claude-XXXX` name on every start).
export HWBUDDY_ADDRESS=00112233-4455-6677-8899-AABBCCDDEEFF
```

The MCP server will auto-connect when it starts and you skip the
`/hwb-connect` step. If unset, the scan flow (`/hwb-connect` with no
argument) finds any `Claude-*` peripheral and works identically on every
platform.

### 4. Verify

In Claude Code:

```
/hwb-ping       # MCP server liveness + BLE state
/hwb-test       # synthetic approval prompt on the device
```

## Slash commands

| Command | What it does |
|---|---|
| `/hwb-ping` | Server liveness + BLE state. |
| `/hwb-connect [--address MAC]` | Attach the MCP server's BLE link. |
| `/hwb-status [...]` | Manually push session counters + msg. |
| `/hwb-tokens [--transcript PATH]` | Parse transcript, push tokens. |
| `/hwb-test` | Fake approval prompt — verify touch zones. |

## Architecture

```
┌──────────────┐   PreToolUse / events    ┌───────────┐   BLE NUS   ┌────────────┐
│ Claude Code  ├─────────────────────────►│ hwbuddy   │◄───────────►│  ESP32     │
│  (session)   │  stdio + unix socket     │  MCP srv  │  JSON lines │  pet       │
└──────────────┘                          └───────────┘             └────────────┘
```

- **MCP server** (`bin/hwbuddy`, Go) declared in `.mcp.json`. Claude
  Code launches one instance per session and keeps it alive for the
  session's lifetime; it holds the persistent BLE connection.
- **Hooks** in `hooks/hooks.json` invoke the binary via `type: command`
  for each Claude Code lifecycle event. The CLI mode of the binary
  forwards the request to the running MCP server over a Unix socket
  side channel (`$XDG_RUNTIME_DIR/hwbuddy.sock`).
- **MCP tools** are also directly callable by Claude when it wants to
  push status from inside a tool-use turn.
- **BLE protocol** is Nordic UART Service. Newline-delimited JSON in
  both directions; outgoing shapes in `internal/protocol/protocol.go`,
  incoming acks in the firmware's `src/data.h` + `src/xfer.h`.

## Silent passthrough

If the MCP server is down or the device is offline, every hook returns
exit 0 (allow / proceed). Unplugging the buddy must never break a
Claude Code session.

## Building from source

Requires Go 1.22+ and a C compiler (cgo is needed for the BLE library
on each platform).

```bash
git clone https://github.com/skitzo2000/ai-hardware-buddy
cd ai-hardware-buddy
go build -o bin/hwbuddy-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/') ./cmd/hwbuddy
```

GitHub Actions cross-builds for `linux-amd64`, `linux-arm64`,
`darwin-amd64`, `darwin-arm64`, and `windows-amd64` on each tag and
attaches the artifacts to the GitHub release.

## Platform support

| Platform | Build | Runtime tested |
|---|---|---|
| linux-amd64 | green | yes (primary dev target) |
| linux-arm64 | green | spot-tested |
| windows-amd64 | green | not tested |
| darwin-amd64 (Intel) | green | **not tested** — no Mac available to the maintainer |
| darwin-arm64 (Apple silicon) | green | **not tested** — no Mac available to the maintainer |

The macOS path is in particular a best-effort port:

- Apple's CoreBluetooth stack identifies peers by a system-assigned
  peripheral UUID, not by the BLE MAC. `$HWBUDDY_ADDRESS` accordingly
  takes a UUID on macOS, not a MAC.
- The `bluetoothctl`-based BlueZ recovery used when Linux holds a stale
  connection to a trusted device is a no-op on macOS — there's no
  equivalent CLI in Apple's stack.
- Pairing must already exist via System Settings → Bluetooth before
  `hwbuddy connect` will work, same as the Linux flow.

If you're running on a Mac and something breaks, please open an issue —
the project is public and PRs are welcome. The tracking issue for the
known macOS gaps is
[#2](https://github.com/skitzo2000/ai-hardware-buddy/issues/2).

## Hardware

Any board running the [`claude-desktop-buddy`](https://github.com/anthropics/claude-desktop-buddy)
firmware that advertises as `Claude-XXXX` over Bluetooth LE will work:

- **CYD ESP32-2432S028R** — the cheap-yellow-display board, ~$15 on
  AliExpress. Easiest path: plug it in and use the
  **[web flasher](https://skitzo2000.github.io/claude-desktop-buddy/)** —
  no build toolchain needed. Source / build instructions at
  [skitzo2000/claude-desktop-buddy](https://github.com/skitzo2000/claude-desktop-buddy)
  (default branch is `cyd`).
- **M5StickC Plus** — the upstream target. If you already have one
  paired with Claude Desktop's built-in Hardware Buddy GUI, this plugin
  extends the same pet to Claude Code on Linux / macOS / Windows — no
  new firmware, no second board. Original Anthropic firmware at
  [anthropics/claude-desktop-buddy](https://github.com/anthropics/claude-desktop-buddy).

## License

MIT. See [LICENSE](./LICENSE).
