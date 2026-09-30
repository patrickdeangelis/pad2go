# Domain glossary

Terms used across the code and docs. Keep code names aligned with these.

**Controller**: one physical Switch 2 controller connected over BLE (`controller.Controller`).

**Kind**: a controller's family: Joy-Con (L), Joy-Con (R), Pro Controller, GameCube. It is derived once from the product ID (`protocol.Kind`), and model-specific behaviour keys off it, never off raw product IDs.

**Report**: the raw input notification a controller sends: buttons, raw 12-bit sticks, IMU, battery (`protocol.Report`).

**Input**: a *calibrated* report: stick calibration, Joy-Con gain and deadzone already applied, sticks in [-1, 1] (`protocol.Input`). A controller emits Inputs, never Reports.

**Settle gate**: after connecting, a controller drops input until the first neutral report (or 1 s), so a held wake button doesn't fire actions.

**Player slot**: one numbered virtual Xbox 360 controller exposed to the OS, with its player LEDs.

**Player pad**: the state behind one player slot (`mapping.PlayerPad`): which controllers feed it and how their Inputs become one Xbox report and per-controller motion. Remap, hold-mode rotation, pair merging and layout are applied here, in that order.

**Joy-Con pair**: a left and a right Joy-Con merged into one player slot.

**Hold mode**: how a *single* Joy-Con is held: Vertical (acts as the right half of a pad) or Horizontal (sideways, stick rotated, SL/SR become ZL/ZR). Joy-Cons in a pair are never rotated.

**Remap**: redirecting an extra button (Home, Capture, C, GL/GR, SL/SR) to another Switch button or to nothing. Remapped buttons bypass hold-mode rotation.

**Layout**: how Switch face buttons map to Xbox ones: positional (`abxy_mode: Xbox`) or by label (`abxy_mode: Switch`).

**Rumble**: game force feedback (large/small motor) converted to HD-rumble frames and kept alive at ~60 Hz while active.

**DSU motion**: gyro/accelerometer samples served to emulators over the CemuHook/DSU UDP protocol.

**Pairing**: bonding a controller in sync mode to this host's Bluetooth address (the **host MAC**), so it later reconnects with a button press. A controller bonded to another host is a **foreign controller**.
