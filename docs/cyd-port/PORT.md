# PORT.md — Claude Desktop Buddy → CYD (ESP32-2432S028R)

Design document for porting [`anthropics/claude-desktop-buddy`](https://github.com/anthropics/claude-desktop-buddy)
from the M5StickC Plus reference board to the Cheap Yellow Display.

Upstream snapshot used for this analysis: cloned at `./upstream/` on
2026-05-16. Numbers below are line counts and file paths from that snapshot.

---

## 1. Executive summary

**Decisions locked in (2026-05-16):**
- Target board: **ESP32-2432S028R** (two USB ports, yellow PCB, JST speaker header — most likely match for user's board; verify in Phase 1)
- Orientation: **portrait** (240×320) — matches upstream layout intent
- Audio: **in scope** — user's board has a speaker header populated; we drive GPIO 26 (DAC2) → onboard mini-amp → speaker
- IMU: **optional support, not required** — code compiles and runs without one; if a user wires an MPU6050 to the P3 header (SDA/SCL on free GPIOs), shake/face-down/auto-rotate features light up
- Repo strategy: **fork** of `anthropics/claude-desktop-buddy`, branch `cyd`

**The port is feasible but non-trivial — call it 2–4 evenings of work to a
working build, plus iteration for ergonomics.** It is *not* a `platformio.ini`
swap. The upstream has deep, line-by-line dependencies on M5StickC Plus
peripherals (AXP192 PMIC, MPU6886 IMU, BM8563 RTC, AB-1218 buzzer, AXP power
button, GPIO 10 LED) that the CYD does not have. Most of these touch
`main.cpp` only and can be replaced behind a thin HAL.

What works in our favour:

- The upstream already uses **TFT_eSPI sprites** for all drawing. `M5.Lcd`
  *is* a `TFT_eSPI` instance — every `spr.drawString(...)`, `spr.fillRect(...)`,
  `buddies/*.cpp` species renderer, and `character.cpp` GIF blit ports
  **as-is**. We only retarget the framebuffer.
- BLE stack (`ble_bridge.cpp`, NimBLE / Arduino BLE) is generic ESP32 — no
  changes.
- `stats.h`, `xfer.h`, `data.h` parsing logic, ArduinoJson, LittleFS — all
  pure-software, fully portable.
- ASCII buddies (`src/buddies/*.cpp`) draw to the shared sprite using only
  geometry constants — porting is a screen-resolution remap, not a rewrite.

What does not:

- 9 distinct M5-specific peripheral APIs (`M5.Lcd`, `M5.Axp`, `M5.Imu`,
  `M5.Rtc`, `M5.Beep`, `M5.BtnA`, `M5.BtnB`, `M5.update`, `M5.begin`).
  Total of ~70 call sites in `main.cpp` alone.
- Screen geometry: M5 is **135×240 portrait**, CYD is **240×320 portrait**
  (or 320×240 landscape). The buddy canvas (BUDDY_CANVAS_W = 135,
  BUDDY_X_CENTER = 67) is hardcoded across 18 species files.
- No IMU on CYD → kill shake-to-dizzy, face-down-to-nap, accelerometer-driven
  clock auto-rotate. The clock mode loses its auto-orient detection but the
  manual "lock landscape/portrait" setting can stay.
- No real RTC on CYD → fall back to a software `time_t` counter seeded by the
  bridge's `{"time":[…]}` sync, persisted to NVS once a minute. Loses
  accuracy across hard power-off (no coin cell) — acceptable for a desk pet
  that re-syncs on connect.
- No battery / power-button / AXP192 → drop the battery info page, drop
  "tap power = screen off" (replace with touch tap on a screen corner),
  drop "hold 6s = power off" (USB-only board, doesn't apply).

---

## 2. Hardware comparison

| Subsystem            | M5StickC Plus reference            | CYD ESP32-2432S028R                          | Port impact |
|----------------------|------------------------------------|----------------------------------------------|-------------|
| MCU                  | ESP32-PICO-D4 (single-core eff.)   | ESP32-WROOM-32 (dual-core)                   | None — same Arduino core, same BLE stack |
| Flash                | 4 MB                               | 4 MB                                         | None |
| PSRAM                | None                               | None on stock CYD                            | None — current code fits without it |
| Display              | ST7789V, 135×240 portrait, SPI     | ILI9341, 240×320 portrait, SPI               | **High** — driver swap + geometry remap |
| Touch                | None (two physical buttons)        | XPT2046 resistive, separate SPI bus          | **High** — new input layer |
| IMU                  | MPU6886 (3-axis accel)             | **None**                                     | Strip shake/face-down/auto-rotate |
| RTC                  | BM8563 (battery-backed, I²C)       | **None**                                     | Software RTC, lossy on power-off |
| PMIC                 | AXP192 (battery, brightness, power)| **None** — direct USB/5V                     | Drop battery UI, brightness via PWM on backlight GPIO |
| Buzzer/audio         | AB-1218 PWM piezo on GPIO 26       | Unloaded speaker pad + JC mini-amp footprint | Optional — solder speaker or stub all beeps |
| LED                  | Red LED on GPIO 10 (active-low)    | RGB LED on GPIOs 4 / 16 / 17                 | Trivial — gain colour, lose nothing |
| Power button         | AXP soft power button (left side)  | Hardware reset button only                   | Drop "tap power to sleep"; use touch corner |
| Buttons A/B          | Front (GPIO 37) and side (GPIO 39) | None                                         | Map to on-screen touch zones |
| Battery              | 120 mAh LiPo, ~2 hr                | None — USB-powered desk device               | Drop battery info; the device is always plugged in anyway |
| SD card              | None                               | microSD slot (TF, shared SPI)                | **Bonus** — character packs >1.8 MB possible |
| USB                  | USB-C via internal CH9102          | USB-B (mini or micro) via CH340              | None — flashing works the same |

---

## 3. Pin map (CYD)

These are the well-known CYD pin assignments (Random Nerd Tutorials,
mischianti, and the marcelrv reference). Verify against your specific
board revision — there are at least three CYD variants in the wild
(2432S028 single-USB, 2432S028R red-board dual-USB, 2432S032 3.2" big-brother).
The mapping below targets the most common 2432S028R.

### Display (ILI9341, SPI bus 1 — VSPI)

| Pin   | Function          |
|-------|-------------------|
| GPIO 14 | TFT_SCLK        |
| GPIO 13 | TFT_MOSI        |
| GPIO 12 | TFT_MISO        |
| GPIO 15 | TFT_CS          |
| GPIO  2 | TFT_DC          |
| GPIO  – | TFT_RST (tied to EN, set `-1` in TFT_eSPI config) |
| GPIO 21 | TFT_BL (backlight, PWM-capable) |

### Touch (XPT2046, SPI bus 2 — HSPI, separate from display)

| Pin    | Function       |
|--------|----------------|
| GPIO 25 | XPT2046_CLK    |
| GPIO 32 | XPT2046_MOSI   |
| GPIO 39 | XPT2046_MISO (input-only — fine for MISO) |
| GPIO 33 | XPT2046_CS     |
| GPIO 36 | XPT2046_IRQ (input-only — pen-down interrupt) |

### RGB LED (active-LOW)

| Pin    | Colour |
|--------|--------|
| GPIO  4 | Red   |
| GPIO 16 | Green |
| GPIO 17 | Blue  |

### Other

| Pin    | Function                                |
|--------|-----------------------------------------|
| GPIO 26 | Audio out (to mini-amp / speaker pad) — DAC channel 2, usable for tones |
| GPIO 34 | LDR (ambient light sensor) — input-only |
| GPIO 27 | Free (extended GPIO connector P3)       |
| GPIO 22 | Free (extended GPIO connector P3)       |
| GPIO 35 | Free, input-only (extended GPIO connector P3) |
| GPIO  5  | SD CS (shared SPI with TFT bus)        |

**SPI bus contention note:** TFT and SD share VSPI on the CYD. The touch
controller is on HSPI (independent). For our purposes this is fine —
streaming GIF frames from SD over the same bus the display is being
written to costs throughput but works; the AnimatedGIF library can read
into a RAM buffer first if frame-rate suffers.

---

## 4. Library mapping

### `platformio.ini` — replace upstream

```ini
[env:cyd]
platform = espressif32
board = esp32dev
framework = arduino
monitor_speed = 115200
board_build.filesystem = littlefs
board_build.partitions = no_ota.csv
board_build.f_cpu = 240000000L      ; CYD has thermal headroom — run flat-out
build_src_filter = +<*> +<buddies/> +<hal/>
build_flags =
    -DCORE_DEBUG_LEVEL=0
    -DBOARD_CYD
    ; TFT_eSPI pin config — must be in build_flags, NOT User_Setup.h,
    ; so the library is shared/clean across projects:
    -DUSER_SETUP_LOADED=1
    -DILI9341_DRIVER=1
    ; (use -DILI9341_2_DRIVER=1 only as a fallback if the standard driver
    ;  shows colour artifacts or inverted display on your specific panel)
    -DTFT_WIDTH=240
    -DTFT_HEIGHT=320
    -DTFT_MISO=12
    -DTFT_MOSI=13
    -DTFT_SCLK=14
    -DTFT_CS=15
    -DTFT_DC=2
    -DTFT_RST=-1
    -DTFT_BL=21
    -DTFT_BACKLIGHT_ON=HIGH
    -DLOAD_GLCD=1
    -DLOAD_FONT2=1
    -DLOAD_FONT4=1
    -DSPI_FREQUENCY=55000000
    -DSPI_READ_FREQUENCY=20000000
    -DSPI_TOUCH_FREQUENCY=2500000
lib_deps =
    bodmer/TFT_eSPI @ ^2.5.43
    paulstoffregen/XPT2046_Touchscreen @ ^1.4
    bitbank2/AnimatedGIF @ ^2.1.1
    bblanchon/ArduinoJson @ ^7.0.0
```

### Dual SPI bus pattern (critical for CYD)

The CYD puts the display on VSPI (the default `SPI` instance) and the touch
controller on a **physically separate** HSPI bus. TFT_eSPI's built-in
`-DTOUCH_CS=...` mechanism assumes display and touch share one bus with
different CS pins — that does **not** apply here. Wire the touch
controller onto its own `SPIClass` instance in `hal/board.cpp`:

```cpp
// hal/board.cpp
TFT_eSPI tft;                           // uses default SPI (VSPI) per build flags
SPIClass hspiTouch(HSPI);
XPT2046_Touchscreen ts(33, 36);         // CS, IRQ

void boardBegin() {
    tft.init();
    tft.setRotation(0);                 // portrait
    hspiTouch.begin(25, 39, 32);        // SCK, MISO, MOSI for HSPI
    ts.begin(hspiTouch);                // critical: bind touch to HSPI
    ts.setRotation(0);
    // ...
}
```

### Library swaps

| Upstream                  | CYD replacement                              | Notes |
|---------------------------|----------------------------------------------|-------|
| `m5stack/M5StickCPlus`    | (removed — replaced by HAL stubs)            | The library bundles TFT_eSPI; we use it directly |
| `M5.Lcd` (TFT_eSPI subclass) | `TFT_eSPI tft;` (global)                  | All `spr.*` calls keep working (sprites attach to `tft`) |
| `M5.Imu.getAccelData()`   | HAL stub returning `(0, 0, 1.0)`              | Means: never shaken, always face-up |
| `M5.Rtc.GetTime/SetTime`  | Software RTC in `hal/rtc.cpp`                | Backed by `time_t` + NVS minute-marker |
| `M5.Axp.ScreenBreath(N)`  | `ledcWrite(BL_CHAN, map(N, 0, 100, 0, 255))` | LEDC PWM on GPIO 21 |
| `M5.Axp.SetLDO2(false)`   | `ledcWrite(BL_CHAN, 0)` + flag for sprite-skip | Backlight off = screen off |
| `M5.Axp.PowerOff()`       | `ESP.deepSleep(0)`                            | Wake via reset button only |
| `M5.Axp.GetBtnPress()`    | (removed — no left power button)              | Replace UX with touch-corner gesture |
| `M5.Axp.GetBatVoltage()`  | Stub → `4.2`                                  | Always "fully charged" — page becomes USB info |
| `M5.Axp.GetVBusVoltage()` | Stub → `5.0`                                  | Always on USB |
| `M5.Axp.GetTempInAXP192()`| Stub → `25` or remove the readout              | |
| `M5.BtnA.wasPressed()`    | `touch.wasTapped(ZONE_APPROVE)` (new API)    | See §6 input adapter |
| `M5.BtnB.wasPressed()`    | `touch.wasTapped(ZONE_DENY)`                 | |
| `M5.BtnA.pressedFor(600)` | `touch.wasLongPressed(ZONE_APPROVE, 600)`    | |
| `M5.Beep.tone(f, d)`      | `tone(26, f, d)` (Arduino built-in)          | Audio is **optional** — speaker isn't loaded on stock board |
| `M5.update()`             | `touch.update()` (HAL)                       | |
| `M5.begin()`              | `tft.init(); touch.begin(); ...` (HAL init)  | |

---

## 5. File-by-file porting plan

The work falls into four buckets: **(A) HAL — write new**, **(B) Surgical
edits to existing files**, **(C) Geometry retuning**, **(D) Port unchanged**.

### A. New files to write — the HAL layer

Create `src/hal/` containing thin adapters so `main.cpp` doesn't need to
care about board specifics. This is the work item that controls scope —
once HAL is solid, the rest is mechanical.

| File                 | Provides                                    | Approx size |
|----------------------|---------------------------------------------|-------------|
| `hal/board.h`        | `boardBegin()`, `boardLoop()` (called once per tick), display/touch object exports | 30 lines |
| `hal/board.cpp`      | `TFT_eSPI tft;` + `XPT2046_Touchscreen ts(33, 36);` instantiation + init | 80 lines |
| `hal/input.h/.cpp`   | Touch zone hit-tester, debounce, tap/long-press detection, `inputA*/inputB*()` API matching `M5.BtnA/B` semantics | 150 lines |
| `hal/power.h/.cpp`   | Backlight PWM on/off/level, deep-sleep wrapper, fake battery readings (returns USB/100%) | 60 lines |
| `hal/imu.h/.cpp`     | Optional MPU6050 driver behind compile-time flag. `-DHAS_IMU` enables I²C reads from the P3 header pins (GPIO 22 SDA / GPIO 27 SCL is the conventional CYD-extension assignment); without the flag returns face-up + no-shake constants. Same function signature in both modes — `main.cpp` doesn't know the difference | 80 lines |
| `hal/rtc.h/.cpp`     | Software RTC: `setTime/getTime/getDate` mirroring `M5.Rtc.*` API; `time_t` + `millis()` drift; NVS persistence on the minute boundary | 100 lines |
| `hal/audio.h/.cpp`   | `boardTone(freq, dur)` driving GPIO 26 (DAC2) → onboard mini-amp → speaker header. Use LEDC PWM channel for non-blocking tone scheduling matching `M5.Beep.tone()` semantics (start tone + auto-stop after `dur` ms via timer). | 80 lines |
| `hal/led.h/.cpp`     | `ledSet(r, g, b)` on the RGB LED — gain colours (red still for attention, green for celebrate, blue idle pulse if we want) | 40 lines |

The HAL is the only place that knows we're on CYD vs M5StickC Plus. If we
ever wanted to revert to the M5 build, we'd ship `hal/m5stickc/*.cpp` as
the alternative.

### B. Surgical edits

#### `src/main.cpp` (1265 lines, ~70 M5-specific calls)

1. Replace `#include <M5StickCPlus.h>` with `#include "hal/board.h"`.
2. `TFT_eSprite spr = TFT_eSprite(&M5.Lcd);` →
   `TFT_eSprite spr = TFT_eSprite(&tft);` (single edit; `tft` exported by HAL).
3. Global geometry constants:
   ```cpp
   const int W = 240, H = 320;   // was 135, 240
   const int CX = W / 2;          // 120, was 67
   const int CY_BASE = 160;       // was 120 — vertical centre shifts
   ```
   Search-replace will not work — every panel layout (`drawSettings`,
   `drawMenu`, `drawReset`, `drawApproval`, `drawHUD`, etc.) hardcodes a
   panel width `mw = 118` chosen to fit a 135-wide screen. On 240×320 we
   want `mw = 200` and re-centre. **§6 has a full geometry remap table.**
4. `M5.begin/Imu.Init/Beep.begin` → `boardBegin()`.
5. `M5.update()` at top of loop → `boardLoop()` → which internally polls
   touch and refreshes the input state.
6. `M5.BtnA.wasReleased() / .pressedFor(600) / .isPressed()` → `inputA.*`
   API mirroring the same semantics over touch zones. Same for BtnB.
7. `M5.Axp.ScreenBreath(N)` → `powerBacklight(N)` (HAL maps 0–100 to PWM).
8. `M5.Axp.SetLDO2(true/false)` → `powerScreen(on/off)` (turns backlight off
   and sets a `screenOff` flag the render path already respects).
9. `M5.Axp.PowerOff()` in the menu — change the menu item from "turn off"
   to "sleep" and have it call `powerSleep()` which `esp_deep_sleep_start()`s.
   Wake via reset button (the user will know).
10. `M5.Axp.GetBtnPress()` block (lines ~1056–1063) — delete entirely. The
    equivalent gesture on CYD is "tap the top-right corner to put the
    screen to sleep" — implement as `if (touch.wasTapped(ZONE_SLEEP))`.
11. `M5.Imu.getAccelData()` everywhere — wrap behind `imuRead(&ax,&ay,&az)`
    HAL call that returns `(0, 0, 1.0)`. This single change makes
    `isFaceDown()` always return false, `checkShake()` always return false,
    and `clockUpdateOrient()` always return `clockOrient = 0`. **Net
    effect:** shake/face-down/auto-rotate become silently disabled. Manual
    rotation lock setting still works.
12. `M5.Rtc.GetTime/GetDate/SetTime/SetDate` → `rtcGetTime/...` HAL calls.
    The `RTC_TimeTypeDef` / `RTC_DateTypeDef` structs are M5 types — define
    drop-in replacements in `hal/rtc.h`.
13. `M5.Axp.GetBatVoltage/Current/VBusVoltage/TempInAXP192` in `drawInfo()`
    page 3 — replace the page with a "DEVICE" page showing only what we
    actually have: uptime, heap, brightness, BT linked-or-not, and a
    "running on USB" line. Drop the battery percentage bar entirely.
14. `M5.Beep.tone(f, d)` → `boardTone(f, d)` (HAL — no-op if speaker not
    fitted). Most beep call sites are fine as silent stubs; the prompt
    arrival chirp is the only one the user might miss.
15. `pinMode(LED_PIN, OUTPUT); digitalWrite(LED_PIN, HIGH);` → `ledSet(0,0,0);`
    The attention-pulse block (lines ~1004–1010):
    ```cpp
    if (activeState == P_ATTENTION && settings().led) {
        ledSet((now / 400) % 2 ? 0xFF : 0, 0, 0);   // red flash
    } else {
        ledSet(0, 0, 0);
    }
    ```

#### `src/data.h` (185 lines)

One change: the `M5.Rtc.SetTime/SetDate` block (lines 84–85) → `rtcSetTime/...`
HAL calls. Same `RTC_TimeTypeDef` argument names.

#### `src/xfer.h` (~220 lines — not previously flagged as a port target)

The `status` command handler at **lines 115–117** reads
`M5.Axp.GetBatVoltage()` / `GetBatCurrent()` / `GetVBusVoltage()` to build
the battery telemetry block in the status ack response:

```json
"bat": { "pct": 87, "mV": 4012, "mA": -120, "usb": true }
```

Replace these with a `powerStatus()` HAL call returning a struct of
`{pct, mV, mA, usb}`. On the CYD the implementation returns
`{100, 5000, 0, true}` (always-on USB, always "full"). The desktop's
Hardware Buddy window will show 100% with USB indicator — accurate, not
fake. No other M5 dependencies in `xfer.h`.

#### `src/buddy.cpp` (197 lines)

Two changes:
1. Drop `#include <M5StickCPlus.h>` — replace with `#include "hal/board.h"`.
2. Geometry: `BUDDY_X_CENTER = 67` → `120`. `BUDDY_CANVAS_W = 135` → `240`.
   But — the species art is hardcoded to a fixed character grid. Rather
   than rescale, **leave the buddies at 1× and centre the column on the
   wider canvas**. The buddies will look small on the bigger screen at 1×;
   the existing `_scale = 2` path (used on the home screen) gives us 2×
   buddies that look right on 240×320. Alternative: bump `_scale` to 3
   for the home screen and keep 2× as the peek scale — feels appropriate
   for the bigger screen.

#### `src/character.cpp` (not read in detail, but the .h is clean)

Likely no changes — GIF decoder writes to the sprite via `TFT_eSPI` calls
which are board-agnostic. Verify the peek-mode centre coordinates match
the new canvas. The 96px-wide upstream GIFs render at native size with
72px of horizontal margin on 240×320 — fine, but we could optionally
support 1.5× scale on the CYD for nicer presentation.

### C. Geometry retune

The upstream is designed for a 135×240 portrait window with very tight
layout. On 240×320 the existing layouts will look cramped in the centre
with wasted margins. A pass per panel:

| Panel              | Upstream (mw, mh, contents)            | CYD target                                                       |
|--------------------|----------------------------------------|------------------------------------------------------------------|
| `drawMenu`         | mw=118, mh ≈ 16 + 6×14 + 14 = 114      | mw=200, mh ≈ 24 + 6×20 + 18 = 162, larger text                   |
| `drawSettings`     | mw=118, mh ≈ 16 + 10×14 + 14 = 170     | mw=220, mh ≈ 24 + 10×20 + 18 = 242                               |
| `drawReset`        | mw=118, mh ≈ 16 + 3×14 + 14 = 72       | mw=200, mh ≈ 24 + 3×24 + 18 = 114                                |
| `drawApproval`     | AREA=78 at bottom                      | AREA=120; tool name renders at size 3 instead of 2; hint wraps at ~32 char |
| `drawHUD`          | 3 lines × 8px = 24px, 21-char wrap     | 4 lines × 16px = 64px, **40-char wrap** (much more transcript visible) |
| `drawPet` / stats  | y starts at 70                         | y starts at 90; tinyHeart and mood pips space out wider |
| `drawInfo` pages   | wraps at 21 chars                      | wraps at 40 chars; needs every `ln(...)` string reflowed or use the wrap helper |
| `drawClock`        | 4× HH:MM, 2× seconds, 1× date          | 6× HH:MM, 3× seconds, 2× date for the bigger screen |
| `drawPasskey`      | size 3 digits at (W-108)/2             | size 5 digits (6 ÷×) centred                          |

The `wrapInto(in, out[][24], ...)` helper hardcodes a 24-char buffer
width. Bump to 48 for the wider screen, and re-tune the `WIDTH = 21`
constants in `drawHUD()` and elsewhere.

### D. Port unchanged

The following files need **no edits** beyond the include change in `main.cpp`:

- `src/ble_bridge.cpp` and `.h` — ESP32 BLE works the same.
- `src/buddies/*.cpp` (18 species files) — they use only `buddyPrintLine`,
  `buddyPrint`, `buddySetCursor` from `buddy_common.h`. Those go through
  the same `_tgt` (sprite) pointer the new build sets up. Result: species
  render at upstream's intended ratio, just on a larger canvas.
- `src/stats.h` — pure NVS; no peripheral access.
- `src/buddy_common.h` — defines geometry constants used by species; we
  patch values, not code.
- `src/data.h` — one RTC change noted above; everything else portable.

### E. Tools (Python)

`tools/prep_character.py`, `tools/flash_character.py`, `tools/test_serial.py`,
`tools/test_xfer.py` are all desktop-side — no changes needed.

---

## 6. Input adapter — touch as buttons

The single biggest UX shift: two physical buttons → resistive touch. We
preserve the upstream's "A is the primary action, B is the secondary
action" mental model.

### Touch zones (240×320 portrait)

```
┌───────────────────────────────┐
│ ZONE_SLEEP                    │  Tap top-right corner → screen off
│                              ◯│  (replaces M5.Axp short-press)
│                               │
│       BUDDY / GIF             │
│         (upper)               │
│                               │
│                               │
│                               │
├───────────────────────────────┤
│       HUD / TRANSCRIPT        │  Normal: scroll = tap-anywhere-here
│                               │  Approval: see below
│                               │
├──────────────┬────────────────┤
│              │                │
│  ZONE_DENY   │  ZONE_APPROVE  │  Bottom bar: split halves
│   (B)        │     (A)        │  - tap = primary action
│              │                │  - long-press 600ms ZONE_APPROVE = menu
│              │                │  - tap ZONE_DENY in lists = "change"
└──────────────┴────────────────┘
```

`hal/input.cpp` exposes:

```cpp
struct Button {
    bool wasPressed();      // edge-trigger, one tap = one true
    bool wasReleased();     // tap finished
    bool isPressed();       // is finger currently down on this zone
    bool pressedFor(uint32_t ms);   // long-press threshold reached
};

extern Button inputA;       // ZONE_APPROVE
extern Button inputB;       // ZONE_DENY
extern Button inputSleep;   // ZONE_SLEEP (replaces M5.Axp)
```

This lets us preserve the upstream main loop almost verbatim:

```cpp
// Before:
if (M5.BtnA.wasReleased()) { ... }
if (M5.BtnA.pressedFor(600) && !btnALong) { ... }

// After:
if (inputA.wasReleased()) { ... }
if (inputA.pressedFor(600) && !btnALong) { ... }
```

### Touch implementation notes

- **Calibration**: XPT2046 is resistive; raw values are 0–4095 and rotate
  with display rotation. Run a one-time calibration on first boot
  (touch the 4 corner crosshairs) and persist offsets to NVS. The
  `XPT2046_Touchscreen` library doesn't ship calibration — we either bring
  in a helper or hardcode reasonable defaults (most CYDs use the same
  panel and ship near-aligned).
- **Debounce**: 50 ms minimum. The library reports continuous press while
  the finger is down — we synthesise edges from that.
- **Pen-down IRQ**: GPIO 36 fires on touch — wire as an optional fast-path
  to short-circuit the SPI poll. Polling every 16 ms in `loop()` is fine
  for our cadence.

---

## 7. Risk register

| Risk                                            | Likelihood | Impact | Mitigation |
|-------------------------------------------------|------------|--------|------------|
| TFT_eSPI + BLE concurrent stutter on core 0     | Medium     | Medium | Pin BLE callbacks to core 1 (`xTaskCreatePinnedToCore`); use DMA on TFT_eSPI sprite push if it bites |
| ILI9341 SPI clock too high causes display glitch | Low        | Low    | Default to 27 MHz; bump to 40 MHz only if frame-rate suffers |
| GIF decode + display + BLE keeps us under 16 ms | Medium     | Low    | Drop sprite push to 30 fps; species buddies already tick at 5 fps |
| Touch unresponsive during sprite push           | Medium     | Medium | Touch is on separate SPI bus — should be parallel. Verify; if not, sample touch during display vblank |
| Software RTC drifts after power-off             | High       | Low    | Stamp current `time_t` to NVS every 60 s; on boot, restore as the floor. Bridge `{"time":[…]}` re-syncs on first heartbeat anyway |
| User soldered speaker is too loud / wrong impedance | Low      | Low    | Volume defaults to off; settings → sound is opt-in |
| Character pack >1.8 MB silently breaks         | Low        | Low    | We can lift the limit (SD card storage), but upstream desktop app enforces 1.8 MB cap on the push side — match it |
| LE Secure Connections bonding flakiness         | Medium     | High if it fails — link is sniffable | **Upstream uses legacy Arduino-ESP32 `BLEDevice.h`, not NimBLE-Arduino.** Verification agent confirmed `ESP_LE_AUTH_REQ_SC_MITM_BOND` is set with `ESP_IO_CAP_OUT` (DisplayOnly), passkey rendered via `onPassKeyNotify` at `ble_bridge.cpp:73-87`. The legacy library is known to be flaky on bonding across reboots. If first-pair works but reconnect after a desktop or device reboot fails, the fix is a NimBLE-Arduino migration — out of scope for v1, listed as M6 stretch goal |
| TFT_eSPI 1.4.5 (M5 vendored) vs 2.5.x (bodmer current) API drift | Low | Low | Verification agent confirmed the 19 sprite methods upstream uses are stable across that span. If `spr.*` behaves unexpectedly, pin to bodmer/TFT_eSPI @ 2.5.43 explicitly |
| Backlight brightness PWM frequency audible      | Low        | Low    | Use 20 kHz LEDC channel |
| CYD board variant pin differences (2432S028 vs R) | Medium    | Medium | Document the targeted variant; provide a `#define BOARD_CYD_R` switch if needed |

---

## 8. Phased delivery plan

If we proceed, I'd cut the work into four phases. Each ends in a
demonstrable build.

### Phase 1 — display + sprite (½ day)
- Stand up `platformio.ini` with TFT_eSPI build flags.
- Write `hal/board.cpp` with `tft` init.
- Stub HAL input, IMU, RTC, audio, LED, power as no-ops returning safe defaults.
- Compile `main.cpp` with `#include "hal/board.h"` swap — no other changes.
- Result: device boots, splash screen renders ("Hello! / a buddy appears"),
  pixels are right but everything else is frozen.

### Phase 2 — touch + species (½ day)
- Implement `hal/input.cpp` touch zones (A approve / B deny / sleep corner).
- Calibrate touch (one-time, persisted).
- Patch `BUDDY_X_CENTER` and global `W/H`.
- Result: device boots, you can cycle the 18 ASCII buddies via touch,
  settings menu works.

### Phase 3 — geometry retune (1 evening)
- Rewrite the 8 panel layouts in `main.cpp` for 240×320 (table in §5C).
- Resize fonts, re-flow text wrap widths.
- Add the touch-corner sleep gesture, kill battery info page, replace
  with a cleaner USB/system page.
- Result: looks like a CYD-native pet, not a stretched M5 port.

### Phase 4 — BLE end-to-end (½–1 evening)
- Verify NimBLE bond + secure connection.
- Pair with Claude desktop, run an approval flow.
- Test folder-push for a custom character pack.
- Optional: solder a speaker, test the attention chirp.
- Result: working pet on a CYD; ready to share.

### Phase 5 (optional, post-MVP) — CYD-specific wins
- **microSD character storage**: lift the 1.8 MB constraint, scan SD for
  packs on boot, allow swapping via menu.
- **Light sensor (LDR on GPIO 34)**: auto-brightness — already implied by
  upstream's brightness setting, just driven from ambient instead.
- **RGB LED expressiveness**: red flash for attention, green pulse for
  celebrate, blue dim breathing for idle.
- **Audio quality**: use `i2s_write` to GPIO 26 DAC for fuller tones, or
  embed a few short sample WAVs (chime on prompt, applause on level-up).

---

## 9. Open questions — resolved

All five resolved on 2026-05-16:

1. **Target CYD variant** → assumed **ESP32-2432S028R** based on user's
   board having two USB ports + yellow PCB + speaker header. The user
   mentioned reading "XH-32S" on the silkscreen, which is likely the
   JST-XH connector marking on the speaker header rather than a board ID.
   Verify in Phase 1; if pin map is off, the only files that need
   updating are `hal/board.cpp` and the build flags in `platformio.ini`.
2. **Audio** → **in scope**. Speaker header is populated on user's
   board. `hal/audio.cpp` drives GPIO 26 (DAC2) for all upstream
   beep/chirp call sites.
3. **Orientation** → **portrait** (240×320). Matches upstream layout
   intent; minimises geometry rework. USB cables exit the side of the
   device — acceptable for a desk pet.
4. **MPU6050** → **optional**. Build with `-DHAS_IMU` to enable; without
   it, the IMU HAL returns face-up no-shake constants and the
   shake/face-down/auto-rotate features become silently inert (manual
   rotation-lock setting still works).
5. **Repo location** → **fork of `anthropics/claude-desktop-buddy`**,
   branch `cyd`. Future upstream fixes merge cleanly.

---

## 10. Acceptance criteria

The port is "done" when:

- [ ] Builds cleanly with `pio run -e cyd` (no warnings at level 0).
- [ ] Flashes to a stock CYD via `pio run -e cyd -t upload` with no
      hardware modifications.
- [ ] Boots to the splash screen and one of the 18 ASCII pets appears.
- [ ] BLE advertises as `Claude-XXXX` and is discoverable by Claude
      desktop's Hardware Buddy window.
- [ ] An approval prompt from a Claude Code session lights up the screen
      and can be approved/denied via the on-screen touch zones.
- [ ] Settings menu is reachable via long-press on ZONE_APPROVE.
- [ ] Stats persist across reboot.
- [ ] Folder-push of `characters/bufo/` from the desktop app installs the
      GIF character and the device switches to it live.
- [ ] No M5StickC Plus library is in the dependency tree.

---

*Document end. Reviewed against upstream commit at clone time
2026-05-16. Re-check this doc if upstream changes peripheral usage in
`main.cpp`.*
