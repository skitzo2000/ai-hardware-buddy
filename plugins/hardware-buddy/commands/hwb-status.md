---
description: Push session counters + a one-line msg to the device HUD.
argument-hint: "[--msg TEXT] [--running N] [--waiting N] [--total N] [--completed]"
---

```bash
hwbuddy status ${ARGUMENTS}
```

Each flag is optional and only changes the field if provided — pushing
``--msg`` alone won't reset the counters.

Examples:

- ``/hwb-status --msg "building docs" --running 1`` — one session
- ``/hwb-status --waiting 2 --msg "needs review"`` — pet goes to attention, LED pulses red
- ``/hwb-status --total 0 --running 0 --waiting 0 --msg idle`` — idle baseline
