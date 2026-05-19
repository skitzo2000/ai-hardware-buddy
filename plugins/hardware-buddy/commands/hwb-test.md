---
description: Show a synthetic approval prompt on the device to verify the round trip.
---

```bash
hwbuddy test
```

Pushes a fake ``Bash / rm -rf /tmp/example`` approval prompt to the
device. You should see the bottom of the screen flip to the approval
bar with "B: deny" on the left and "A: approve" on the right. Tap
either to verify the touch zones; the response stays in the device
(this command doesn't await a decision).

For the full PreToolUse round trip (server-side awaits the device's
decision and returns it), let a real ``Bash`` tool call fire — the
plugin's hook handles it.
