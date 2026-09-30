# switch2go

A Go port of [TommyWabg/Switch2Connect](https://github.com/TommyWabg/Switch2Connect):
connect **Switch 2 Joy-Cons**, the **Switch 2 Pro Controller** and the **NSO
GameCube Controller** to a PC over Bluetooth LE and use them as virtual **Xbox
360 controllers**, with an optional **CemuHook/DSU** motion server for emulators.

It is a single static binary with no Python runtime. It also runs on Linux
(uinput) as well as Windows (ViGEmBus), and the DSU server works on macOS too.

Ported from upstream commit `e5dd90b` (2026-09-13).

## Quick start

```bash
go install github.com/angelispatrick/switch2go/cmd/switch2go@latest
```

```bash
switch2go init
```

```bash
switch2go
```

Then hold **SYNC** on an unpaired controller (or press any button on one
already paired to this PC). Controllers paired by this tool reconnect with a
button press next time.

Other commands: `switch2go scan` lists nearby controllers without
connecting; `-v` turns on debug logging; `-config path.yaml` picks a config file.

### Windows

1. Install the [ViGEmBus](https://github.com/nefarius/ViGEmBus) driver.
2. Put `ViGEmClient.dll` (x64) next to `switch2go.exe`. It ships with
   ViGEm-based tools (e.g. the `vgamepad` Python package) or can be built from
   [ViGEmClient](https://github.com/nefarius/ViGEmClient).
3. Hide the physical controller from games with HidHide if you see doubled input.

### Linux

Requires BlueZ and write access to `/dev/uinput`:

```bash
sudo modprobe uinput
```

Add a udev rule (e.g. `KERNEL=="uinput", GROUP="input", MODE="0660"`) or run as root.

### macOS

macOS has no virtual gamepad API, so set `output: none` and
`gyro_passthrough_mode: Cemuhook` to feed motion to emulators. macOS does not
expose the adapter MAC, so set `host_mac` if you want pairing.

## Configuration

`switch2go init` writes a commented `config.yaml`. Key names match the
original's `config.yaml`, and an existing original config loads as-is (unknown
keys are ignored; `button_remaps.xbox` overrides apply).

| Key | Meaning |
| --- | --- |
| `output` | `auto`, `vigem`, `uinput` or `none` |
| `host_mac` | this PC's Bluetooth MAC; auto-detected on Windows/Linux |
| `abxy_mode` | `Xbox` (positional: Switch B → Xbox A) or `Switch` (by label) |
| `combine_joycons` | merge a left + right Joy-Con into one pad |
| `default_hold_mode`, `joycon_hold_mode` | `Vertical` / `Horizontal` for single Joy-Cons |
| `gc_trigger_mode` | `Hair Trigger`, `100% at Bump`, `100% at Max` |
| `joystick_deadzone_percent` | per family: `joycon`, `pro_controller`, `nso_gamecube_controller` |
| `vibration_strength_xbox` | 0–10 (5 = 100%) |
| `gyro_passthrough_mode` | `Cemuhook` enables the DSU server (`cemuhook_host`, `cemuhook_port`) |
| `home_mapping`, `capt_mapping`, `c_mapping`, `gl_mapping`, `gr_mapping`, `sll/srl/slr/srr_mapping` | `Default`, `None`, or a Switch button name |

## What was ported

| Feature | Status |
| --- | --- |
| BLE discovery, pairing/bonding, reconnect-by-button | ✅ |
| SW2 init sequence, input format 0x30, feature enable | ✅ |
| Factory/user stick calibration, radial deadzones | ✅ |
| Joy-Con 2 L/R, Pro Controller 2, NSO GameCube (analog triggers, trigger modes) | ✅ |
| Player LEDs, connection haptics | ✅ |
| Joy-Con merging, vertical/horizontal single Joy-Con | ✅ |
| Xbox 360 output via ViGEmBus (Windows) | ✅ |
| Xbox 360 output via uinput (Linux) — new in this port | ✅ |
| Game rumble → HD rumble (LF/HF, strength, delay) | ✅ (basic mapping) |
| CemuHook/DSU motion server | ✅ |
| Extra-button remaps to controller buttons | ✅ |
| Keyboard/mouse/media remaps, gyro mouse, IR mouse | ❌ |
| PS4/PS5 (DualSense) and Switch 1/2 emulation (WinUHid, USBIP) | ❌ |
| Adaptive triggers, audio haptics, Xbox impulse triggers | ❌ |
| ESP32-S3 bridge, wired USB | ❌ |
| Profiles, power saving, auto-disconnect, GUI/tray | ❌ |

The original is ~58k lines of Windows-specific Python. This port covers the
controller protocol and the Xbox path. The drivers, firmware and Windows
integrations above are left out.

### Behavioural differences

- Rumble uses the original's basic ViGEm mapping (`amp = 800·motor/256`, scaled
  by strength, re-sent at 60 Hz). The tuned frequency masks and audio-haptic mixing are not ported.
- For a horizontal right Joy-Con with `abxy_mode: Switch`, the original's
  face-button table is not a rotation of its `Xbox` table. This port uses the
  rotation for both layouts.
- The DSU server converts calibrated [-1, 1] stick values to 0–255. The
  original scaled them as if they were raw 0–4095 readings.

## Performance

Compared with the original Python on the same inputs (Apple M1 Pro), per-report
processing is about 47× faster (12.3 µs → 0.26 µs) and startup drops from ~470 ms/70 MB
to under 10 ms/11 MB. Both add latency far below the BLE link's own 7.5–15 ms.
See [bench/RESULTS.md](bench/RESULTS.md).

## Development

```bash
go test -race ./...
```

Protocol encodings are checked against golden values produced by the original
Python code. The controller handshake is tested against a simulated controller,
and the DSU server over real UDP. `S2C_UINPUT_IT=1` enables an end-to-end uinput
test (needs root and `/dev/uinput`).

Layout:

```
cmd/switch2go   CLI
internal/protocol    BLE protocol: UUIDs, commands, reports, calibration, rumble
internal/controller  per-controller handshake, commands, input, rumble
internal/ble         tinygo.org/x/bluetooth transport and scanner
internal/mapping     remaps, Joy-Con orientation/merging, Switch → Xbox
internal/app         player slots, rumble routing, DSU publishing
internal/dsu         CemuHook/DSU UDP server
internal/virtualpad  ViGEmBus (Windows), uinput (Linux), none
internal/config      YAML config
```

## License

GPL-3.0, same as the upstream project (see `LICENSE`). Protocol knowledge and
constants come from Switch2Connect by TommyWabg.
