# ViGEmClient (vendored)

The ViGEm client SDK, compiled into `pad2go.exe` through cgo so no
`ViGEmClient.dll` has to ship. MIT, see `LICENSE`.

Taken from [LizardByte/Virtual-Gamepad-Emulation-Client](https://github.com/LizardByte/Virtual-Gamepad-Emulation-Client)
at `8d71f67` (the maintained fork of the archived `nefarius/ViGEmClient`; it
is what Sunshine builds with MinGW). `../vigemclient_windows.cpp` is its
`src/ViGEmClient.cpp`; it has to sit in the Go package directory for cgo to
compile it.

Local change: `<Windows.h>` and `<SetupAPI.h>` are included in lower case, so
MinGW headers resolve on case-sensitive file systems (the Docker cross-build).
