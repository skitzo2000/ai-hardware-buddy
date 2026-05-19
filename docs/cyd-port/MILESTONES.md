# MILESTONES.md — Execution plan with sub-agent decomposition

Complement to [`PORT.md`](./PORT.md). Where PORT.md describes *what* needs
to change, this doc describes *in what order* and *which work can run in
parallel*. Each milestone ends in a demoable, testable state.

## Conventions

- **Serial (S)**: must finish before the next item begins.
- **Parallel (P)**: can run concurrently with siblings — fan out agents.
- **Manual (M)**: human-only action (flash, observe, decide).
- **Agent type**: which subagent role to spawn (`general-purpose`, `Explore`).
- **Estimated agent runtime**: rough envelope; lets us batch realistically.

Sub-agents inherit no conversation context. Every prompt must be
self-contained and reference specific files + line ranges from PORT.md
and the upstream snapshot.

---

## Pre-flight (one-time, manual)

Before any agent runs:

| Step | Owner | Action |
|------|-------|--------|
| P0.1 | User  | Fork `anthropics/claude-desktop-buddy` on GitHub → `<your-user>/claude-desktop-buddy-cyd`. Provide the fork URL. |
| P0.2 | Claude| `gh repo clone <fork-url> .` into a working dir, create branch `cyd`. (Alternative: I drive `gh repo fork` once user confirms GitHub auth.) |
| P0.3 | Claude| Verify the upstream snapshot at `./upstream/` matches the fork's main branch (git rev-parse). |

---

## Milestone 1 — Buildable skeleton (½ day)

**End state:** `pio run -e cyd` produces a flashable binary. main.cpp is
gutted to a `setup() { delay(100); } loop() { delay(100); }` skeleton.
Device boots, does nothing, doesn't crash.

| Step | Mode | Agent | Inputs | Outputs |
|------|------|-------|--------|---------|
| 1.1 | S | general-purpose | PORT.md §4 platformio.ini snippet; partition table policy (4 MB app / 4 MB LittleFS on 8 MB flash if user's CYD ships with 8 MB; otherwise default) | `platformio.ini`, `partitions.csv`, `.gitignore` |
| 1.2 | S (after 1.1) | general-purpose | The `src/main.cpp` upstream file; instruction to comment out the whole body and leave only an empty `setup`/`loop` | A minimal `src/main.cpp.skeleton` (renamed; original kept as `main.cpp.upstream` for reference) |
| 1.3 | M | User | `pio run -e cyd` → expect green | Confirmed buildable on user's machine |
| 1.4 | M | User | `pio run -e cyd -t upload` → flash → ESP boots silently | Confirmed flashable |

**Acceptance:** clean build, device flashable, serial shows boot banner.
No display output expected yet.

**Why this milestone exists:** isolates "does the toolchain work for this
board" from "does the firmware work." If 1.4 fails, the problem is the
build environment, not the port.

---

## Milestone 2 — HAL scaffolding + boot to splash (1 evening)

**End state:** Device boots, the upstream splash screen ("Hello! / a buddy
appears") renders on the CYD's 240×320 display. Buttons/touch don't work
yet — we're verifying display + sprite + the sprite pipeline.

This is the **highest-parallelism milestone** in the project. 7 HAL
modules can be written concurrently after the board HAL is in place.

| Step | Mode | Agent | Inputs | Outputs | Est runtime |
|------|------|-------|--------|---------|-------------|
| 2.1 | S | general-purpose | PORT.md §4 dual-SPI pattern; §3 pin map; the `boardBegin()` skeleton shown there | `src/hal/board.h`, `src/hal/board.cpp` exporting `tft`, `ts`, `boardBegin()`, `boardLoop()` | 5 min |
| 2.2 | **P** (6× concurrent after 2.1) | general-purpose ×6 | One module per agent. Each agent gets: function signature it must provide (matched to upstream M5.* call sites enumerated in PORT.md §5B), and "stub OK for now" instruction | `hal/input.{h,cpp}` (stub: no-touch), `hal/imu.{h,cpp}` (face-up stub), `hal/rtc.{h,cpp}` (**real**, software RTC), `hal/audio.{h,cpp}` (stub: no-op), `hal/led.{h,cpp}` (real RGB), `hal/power.{h,cpp}` (real PWM + screen-off flag) | 8–12 min each |
| 2.3 | S (after 2.2) | general-purpose | The list of edits in PORT.md §5B for `src/main.cpp` — items 1–15. Search-replace task with specific find/replace pairs | edited `src/main.cpp` with all `M5.*` calls swapped to `hal/*` equivalents | 15 min |
| 2.4 | S (after 2.2) — can run in parallel with 2.3 | general-purpose | PORT.md §5B `data.h` instruction (one block, lines 84–85) | edited `src/data.h` | 3 min |
| 2.5 | S (after 2.2) — can run in parallel with 2.3 + 2.4 | general-purpose | PORT.md §5B `xfer.h` instruction (status response, lines 115–117); use `powerStatus()` from `hal/power.h` | edited `src/xfer.h` | 5 min |
| 2.6 | S (after 2.3+2.4+2.5) | (no agent — Claude does it) | Compile check. Triage errors | Either green build or a list of fixes needed | — |
| 2.7 | M | User | Flash + observe | Splash screen renders correctly on the CYD |

**Acceptance:**
- `pio run -e cyd` is clean
- Boot shows "Hello! / a buddy appears" with proper colours
- After ~2s the screen transitions to a sleeping pet (one of the 18 ASCII species — whichever index 0 is, which is `capybara` based on the species table in `buddy.cpp:91`)
- Serial monitor shows `buddy: ASCII mode`
- BLE advertises as `Claude-XXXX` (verifiable with a phone BLE scanner)

**What to ignore at this milestone:** layouts looking cramped on the
240×320 screen, no touch input, no audio. We're testing the spine.

---

## Milestone 3 — Touch input working (½ day)

**End state:** You can long-press the bottom-right corner to open the
menu, navigate the menu by tapping bottom-left (Deny=B/scroll) and
bottom-right (Approve=A/select), and cycle through all 18 species via
settings → ascii pet.

| Step | Mode | Agent | Inputs | Outputs |
|------|------|-------|--------|---------|
| 3.1 | S | general-purpose | PORT.md §6 touch-zone diagram; the `Button` API contract; reference upstream's `M5.BtnA/B` semantics (lines ~1046–1138 of main.cpp) | Real `src/hal/input.cpp` replacing the stub: zone hit-test, debounce, edge detection (`wasPressed`/`wasReleased`/`pressedFor`) |
| 3.2 | **P** with 3.1 | general-purpose | Standard XPT2046 4-corner crosshair calibration UI; persist 4 floats to NVS | `src/hal/touch_calibration.cpp` invoked from `boardBegin()` if NVS key missing |
| 3.3 | M | User | Flash; first boot triggers calibration; touch 4 corners; subsequent boots skip | Confirmed touch lands where expected |
| 3.4 | S (after 3.1+3.2 verified) | general-purpose | PORT.md §5C and §6: touch zones for SLEEP, APPROVE, DENY; mapping into existing main.cpp control flow | Final `main.cpp` long-press → menu, taps → navigation works |

**Acceptance:**
- Long-press bottom-right opens menu
- Tap bottom-left scrolls; tap bottom-right confirms
- Settings → ascii pet cycles all 18 species
- Tap top-right corner puts screen to sleep (backlight off); any tap wakes

---

## Milestone 4 — Geometry retune (1 evening)

**End state:** All 8 panels look *native* on 240×320 — no cramped
centre with wasted margins. This is mostly mechanical layout work and
parallelises well.

| Step | Mode | Agent | Inputs | Outputs |
|------|------|-------|--------|---------|
| 4.1 | **P** | general-purpose | PORT.md §5C table row for menu panels; current main.cpp panel functions `drawMenu`, `drawSettings`, `drawReset` | rewritten panel functions with new `mw/mh`, larger text |
| 4.2 | **P** | general-purpose | §5C row for `drawApproval`, `drawHUD`; new AREA, wrap width 40 chars | rewritten approval + HUD |
| 4.3 | **P** | general-purpose | §5C row for `drawPet`, `drawPetStats`, `drawPetHowTo`, `drawInfo` (6 pages, the largest job) | rewritten pet pages + info pages |
| 4.4 | **P** | general-purpose | §5C row for `drawClock` (portrait + landscape variants), `drawPasskey` | rewritten clock + passkey |
| 4.5 | **P** | general-purpose | `wrapInto()` helper: bump buffer from `[24]` to `[48]`, and all `WIDTH = 21` literal callers | edited helper + callers |
| 4.6 | **P** | general-purpose | `buddy.cpp` geometry constants: `BUDDY_X_CENTER 67→120`, `BUDDY_CANVAS_W 135→240`, bump home-screen scale from 2× to 3× if it reads small | edited buddy.cpp |
| 4.7 | S (after all 4.x) | (Claude) | Integration: build + visual review of screenshots from user | possible cleanups |

**Acceptance:**
- Every panel fills its intended share of the screen
- No clipped text on info pages (especially page 3 system info, page 5 credits)
- Transcript shows 4 lines comfortably (was 3 on M5)
- Approval prompt shows tool name at size 3, hint wraps at 40 chars

---

## Milestone 5 — BLE end-to-end + folder push (½–1 evening)

**End state:** Device pairs with Claude desktop, an approval prompt from
a real Claude Code session lights up the screen, you approve via touch
and the session resumes. Folder push of `characters/bufo/` installs the
custom GIF char and it switches live.

| Step | Mode | Agent | Inputs | Outputs |
|------|------|-------|--------|---------|
| 5.1 | M | User | Enable dev mode in Claude desktop → Open Hardware Buddy → Connect → pair (6-digit passkey display test) | Confirmed bonding works |
| 5.2 | M | User | Run a Claude Code session that triggers a permission prompt (e.g., `Bash` tool); approve from CYD | Approval flows back to session, attention LED pulses, response chirp plays |
| 5.3 | M | User | Drag `upstream/characters/bufo/` onto the Hardware Buddy window | GIF char installs over BLE; device switches live; progress bar appears |
| 5.4 (conditional) | S | general-purpose | If 5.1 fails: debug bonding, possibly NimBLE migration if user accepts scope creep | fix or scope-creep recommendation |

**Acceptance:** All four boxes ticked. PORT.md §10 acceptance criteria
verified.

---

## Milestone 6 (stretch) — CYD-specific wins

Independent of each other; pick whichever the user values.

| ID | Goal | Effort | Why |
|----|------|--------|-----|
| 6.1 | microSD char-pack storage — lift the 1.8 MB cap | 1 evening | CYD has SD; can store dozens of character packs |
| 6.2 | LDR auto-brightness on GPIO 34 | 30 min | One ADC read + smoothing into existing brightness setting |
| 6.3 | RGB LED expressive states | 30 min | Already real in hal/led.cpp; add colour cues for celebrate/heart/idle |
| 6.4 | Optional MPU6050 driver (gated `-DHAS_IMU`) | 1 evening | User has option to wire one to P3 header for shake/face-down/auto-rotate |
| 6.5 | NimBLE migration if M5 bonding flakiness materialises | 1 day | Only if M5.x fails repeatedly |

---

## Sub-agent prompt template

When spawning agents for any of the steps above, use this template:

```
Context: I'm porting Anthropic's claude-desktop-buddy ESP32 firmware
from the M5StickC Plus to the CYD (ESP32-2432S028R). The full design
doc is at /home/skitz0/Projects/CYD-ClaudePet/PORT.md — read §<N>
for the spec of the change you're making. The upstream snapshot is at
./upstream/. The new repo is at ./ — work there.

Your specific task: <one-sentence goal>

Inputs:
- Read: <list of upstream files to reference>
- Specs: <PORT.md sections>
- Constraints: <e.g., "match the function signatures exactly so main.cpp
  swap is mechanical">

Deliverable: <list of files to create/edit, with line-budget estimate>

Out of scope: <what NOT to touch, especially other HAL modules in the
same milestone batch — those are other agents' territory>

Report back: a 5-line summary of what you wrote and any open questions.
```

---

## Risk-driven re-ordering

If during M2 the display proves unstable (artifacts, no output), bail to
the ILI9341 driver fallback path BEFORE proceeding to M3 — touch
debugging on a glitching display is misery. The PORT.md driver note
covers this fallback.

If during M3 touch calibration is wildly off, switch to the
`XPT2046_Bitbang` library (bitbang touch SPI on the same hardware pins).
The library swap is contained to `hal/board.cpp` + `hal/input.cpp`.

If during M5 BLE bonding fails reproducibly, escalate to M6.5 (NimBLE
migration) — but only after verifying the desktop app actually exposes
the passkey prompt (legacy-BLE bug repros sometimes look like UX bugs
on the desktop side).

---

## Parallelism summary

The biggest parallelism wins are:
- **M2.2**: 6 HAL modules concurrent (input/imu/rtc/audio/led/power)
- **M4.1–4.6**: 6 panel-rewrite agents concurrent

Everything else is serial because of file-edit conflicts on `main.cpp`.

Sequential dependencies (cannot be parallelised):
- M1 → M2 (need build before HAL)
- M2.1 → M2.2 (need board exports before HAL modules can include them)
- M2.2 → M2.3 (need HAL signatures stable before main.cpp swap)
- M3.1 → M3.4 (need real input before main.cpp button-handler edit)

---

## Stop-the-line conditions

Halt and consult the user (do not let agents run autonomously past these):

1. ILI9341 init fails on first flash → may indicate the user has the
   S035 (ST7796) variant, not S028R
2. Touch reports `z=0` always → IRQ wiring or SPI bus mis-binding
3. BLE bonding fails after 3 attempts → architectural decision (NimBLE
   migration vs accept unencrypted link)
4. Audio plays nothing despite tone() running → amp chip likely not
   populated on user's board; user soldering decision

---

*Document end. This plan is the canonical execution order. Revise this
file (not PORT.md) when the order changes mid-flight.*
