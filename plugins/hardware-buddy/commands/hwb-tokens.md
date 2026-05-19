---
description: Parse the current Claude Code session transcript and push tokens to the device.
argument-hint: "[--transcript PATH]"
---

```bash
hwbuddy tokens ${ARGUMENTS}
```

Sums ``input_tokens + output_tokens + cache_creation_input_tokens`` across
every assistant record in the transcript JSONL. Pushes three windows to
the device:

- **lifetime** → ``tokens`` field (feeds the firmware's 50K-tokens-per-level system)
- **today** → ``tokens_today`` (UTC-date filter)
- **5h window** → ``tokens_window_5h`` + ``window_reset_s``

If you don't pass ``--transcript``, it auto-finds the most recent JSONL
under ``~/.claude/projects/<cwd-slug>/`` — i.e. the transcript for this
Claude Code session.

The hook pipeline already pushes tokens on every ``UserPromptSubmit`` /
``PostToolUse`` / ``Stop`` event, so manual invocation is mostly for
verification.

The firmware's ``stats().tokens`` field uses a "first-sight latch" — the
first push after a device reboot is treated as the baseline; only
*subsequent* pushes credit deltas. ``today`` shows the absolute number
on every push.
