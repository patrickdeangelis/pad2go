// Package protocol implements the Nintendo Switch 2 controller BLE protocol:
// GATT identifiers, command framing, input report decoding, stick
// calibration and HD-rumble frame encoding.
//
// The byte layouts are ported from Switch2Connect (src/controller.py,
// src/discoverer.py, src/utils.py).
package protocol

// Nintendo identifiers.
const (
	NintendoVendorID         = 0x057e
	NintendoBLECompanyID     = 0x0553
	ProControllerPID         = 0x2009 // Switch 1 Pro Controller
	JoyConLPID               = 0x2006 // Switch 1 Joy-Con (L)
	JoyConRPID               = 0x2007 // Switch 1 Joy-Con (R)
	JoyCon2LeftPID           = 0x2067
	JoyCon2RightPID          = 0x2066
	ProController2PID        = 0x2069
	NSOGameCubeControllerPID = 0x2073
)

// ControllerNames maps supported product IDs to display names.
var ControllerNames = map[uint16]string{
	JoyCon2RightPID:          "Joy-Con 2 (Right)",
	JoyCon2LeftPID:           "Joy-Con 2 (Left)",
	ProController2PID:        "Pro Controller 2",
	NSOGameCubeControllerPID: "NSO GameCube Controller",
	ProControllerPID:         "Pro Controller",
	JoyConLPID:               "Joy-Con (Left)",
	JoyConRPID:               "Joy-Con (Right)",
}

// GATT UUIDs.
const (
	ServiceUUID                     = "ab7de9be-89fe-49ad-828f-118f09df7fd0"
	InputReportUUID                 = "ab7de9be-89fe-49ad-828f-118f09df7fd2"
	VibrationWriteJoyConRUUID       = "fa19b0fb-cd1f-46a7-84a1-bbb09e00c149"
	VibrationWriteJoyConLUUID       = "289326cb-a471-485d-a8f4-240c14f18241"
	VibrationWriteProControllerUUID = "cc483f51-9258-427d-a939-630c31f72b05"
	CommandWriteUUID                = "649d4ac9-8eb7-4e6c-af44-1ea54fe5f005"
	CommandResponseUUID             = "c765a961-d9d8-4d36-a20a-5315b111836a"
)

// Commands and subcommands.
const (
	CmdLEDs                = 0x09
	SubLEDsSetPlayer       = 0x07
	CmdVibration           = 0x0A
	SubVibrationPlayPreset = 0x02
	CmdHapticsInit         = 0x03
	SubHapticsEnable       = 0x0A
	CmdMemory              = 0x02
	SubMemoryRead          = 0x04
	CmdPair                = 0x15
	SubPairSetMAC          = 0x01
	SubPairLTK1            = 0x04
	SubPairLTK2            = 0x02
	SubPairFinish          = 0x03
	CmdFeature             = 0x0c
	SubFeatureInit         = 0x02
	SubFeatureEnable       = 0x04
)

// Feature flags.
const (
	FeatureMotion       = 0x04
	FeatureMouse        = 0x10
	FeatureMagnetometer = 0x80
)

// Addresses in controller memory.
const (
	AddrControllerInfo           = 0x00013000
	AddrCalibrationJoystick1     = 0x0130A8
	AddrCalibrationJoystick2     = 0x0130E8
	AddrUserCalibrationJoystick1 = 0x1fc042
	AddrUserCalibrationJoystick2 = 0x1fc062
)

// MaxMemoryRead is the largest single memory read the controller accepts.
const MaxMemoryRead = 0x4F

// LEDPattern maps a player number (1-8) to the player LED bitmask.
var LEDPattern = map[int]byte{
	1: 0x01, 2: 0x03, 3: 0x07, 4: 0x0F,
	5: 0x09, 6: 0x05, 7: 0x0D, 8: 0x06,
}

// IMU scale factors.
const (
	AccelLSBPerG        = 4096.0
	GyroLSBPerDPSPro    = 14.285714
	GyroLSBPerDPSJoyCon = 16.384
)

// PhysicalButtonMask clears status bits that leak into the button field on
// Joy-Con 2 / Pro Controller 2 reports.
const PhysicalButtonMask = 0x03FFFFFF

// IsJoyCon reports whether pid is a Joy-Con 2 of either side.
func IsJoyCon(pid uint16) bool { return pid == JoyCon2LeftPID || pid == JoyCon2RightPID }

// IsProLike reports whether pid uses the "Pro" (two-stick, dual-motor) layout.
func IsProLike(pid uint16) bool {
	return pid == ProController2PID || pid == ProControllerPID || pid == NSOGameCubeControllerPID
}

// HasSecondStick reports whether pid has two analog sticks.
func HasSecondStick(pid uint16) bool { return IsProLike(pid) }
