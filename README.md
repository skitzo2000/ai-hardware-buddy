# ai-hardware-buddy

A Claude Code plugin that mirrors your session — running tools,
permission prompts, transcripts, token usage — to a small ESP32 BLE
device with a TFT screen. Originally a port of Anthropic's
[`claude-desktop-buddy`](https://github.com/anthropics/claude-desktop-buddy)
firmware; this project drives that firmware from **Claude Code** (the
CLI / IDE-extension / web), replacing the Claude Desktop app's built-in
Hardware Buddy GUI.

Works on **Linux, macOS, and Windows** — the plugin ships a single Go
binary, no separate runtime install. Apache 2.0.

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

If you have a single buddy on a stable MAC, set this in your shell rc:

```bash
export HWBUDDY_ADDRESS=EC:E3:XX:XX:XX:XX
```

The MCP server will auto-connect when it starts and you skip the
`/hwb-connect` step.

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

Apache 2.0. See [LICENSE](./LICENSE).
