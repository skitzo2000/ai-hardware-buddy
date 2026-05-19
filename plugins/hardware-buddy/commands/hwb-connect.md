---
description: Connect the buddy MCP server to a paired Claude-XXXX BLE device.
argument-hint: "[--address MAC]"
---

The MCP server runs for the lifetime of this Claude Code session. This
command tells it to attach (or re-attach) to the device. If no address
is provided, it scans for any ``Claude-*`` advertisement.

```bash
hwbuddy connect ${ARGUMENTS}
```

The device must already be paired and bonded at the OS level (one-time
``bluetoothctl pair`` on Linux, or System Settings → Bluetooth on macOS).
If the MCP server lost its link (you moved rooms, lid closed, device
power-cycled), the server's auto-reconnect task will retry on its own —
this command is just for the first attach or to force a re-pair after a
manual ``disconnect``.

If you've set ``$HWBUDDY_ADDRESS`` in your shell, the MCP server
auto-connects at startup and you may never need this command.
