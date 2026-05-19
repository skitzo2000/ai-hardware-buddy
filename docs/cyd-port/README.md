# docs

Design docs for the CYD ESP32-2432S028R port of this firmware. The port
lives on the [`cyd`](https://github.com/skitzo2000/claude-desktop-buddy/tree/cyd)
branch; `main` continues to track Anthropic's upstream M5StickC code.

| File | Purpose |
|---|---|
| [PORT.md](./PORT.md) | The 700-line port spec: pin map, library swaps, file-by-file porting plan, geometry retune table, touch-zone diagram, BLE protocol notes |
| [MILESTONES.md](./MILESTONES.md) | The execution plan that produced the `cyd` branch — milestones M1 through M5, sub-agent decomposition, acceptance criteria |

## Upstream reference

The local `docs/upstream-snapshot/` directory (gitignored) is a working
copy of [anthropics/claude-desktop-buddy](https://github.com/anthropics/claude-desktop-buddy)
that the port was authored against. If you want the same reference,
either clone the upstream fresh or use this repo's `main` branch
(`git checkout main` — it tracks upstream identically).

## Companion plugin

Pairs with [ai-hardware-buddy](https://github.com/skitzo2000/ai-hardware-buddy),
the Claude Code plugin that drives this firmware from Linux/macOS/Windows
over BLE. The Hardware Buddy GUI in Claude Desktop is closed-source and
Mac/Win only — the plugin replaces it and uses the same wire protocol
(Nordic UART Service, newline-delimited JSON).
