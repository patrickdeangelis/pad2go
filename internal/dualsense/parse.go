package dualsense

import (
	"encoding/binary"
	"math"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// Input report 0x01 offsets (report-ID byte = offset 0).
const (
	offLX      = 1
	offLY      = 2
	offRX      = 3
	offRY      = 4
	offL2      = 5
	offR2      = 6
	offFace    = 8 // face buttons and the hat
	offShould  = 9 // shoulders, triggers' digital bits, Create, Options, stick clicks
	offSystem  = 10
	offBattery = 53
)

// stickDeadzone is the fixed radial deadzone, as a fraction of full deflection.
const stickDeadzone = 0.05

// Parse decodes one USB input report. ok is false, and nothing else is
// produced, unless the report is exactly 64 bytes and starts with 0x01.
//
// Buttons are mapped by position onto the Switch-shaped layout (Cross is the
// bottom face button, so it's protocol.BtnB). The digital L2/R2 bits, Mute and
// the touchpad click are not mapped; the analog trigger bytes carry L2/R2.
func Parse(report []byte) (protocol.Input, bool) {
	return parseWithDeadzone(report, stickDeadzone)
}

func parseWithDeadzone(report []byte, deadzone float64) (protocol.Input, bool) {
	if len(report) != InputReportLen || report[0] != 0x01 {
		return protocol.Input{}, false
	}
	var btn uint32
	set := func(b byte, mask byte, to uint32) {
		if b&mask != 0 {
			btn |= to
		}
	}
	face := report[offFace]
	set(face, 0x20, protocol.BtnB) // Cross
	set(face, 0x40, protocol.BtnA) // Circle
	set(face, 0x10, protocol.BtnY) // Square
	set(face, 0x80, protocol.BtnX) // Triangle
	btn |= hatButtons(face & 0x0F)

	shoulder := report[offShould]
	set(shoulder, 0x01, protocol.BtnL)             // L1
	set(shoulder, 0x02, protocol.BtnR)             // R1
	set(shoulder, 0x40, protocol.BtnLStick)        // L3
	set(shoulder, 0x80, protocol.BtnRStick)        // R3
	set(shoulder, 0x20, protocol.BtnPlus)          // Options
	set(shoulder, 0x10, protocol.BtnMinus)         // Create
	set(report[offSystem], 0x01, protocol.BtnHome) // PS
	set(report[offSystem], 0x02, protocol.BtnPSCClick)
	var touch [2]protocol.TouchContact
	for i := range touch {
		b := report[33+i*4 : 37+i*4]
		touch[i] = protocol.TouchContact{Active: b[0]&0x80 == 0, ID: b[0] & 0x7f, X: uint16(b[1]) | uint16(b[2]&15)<<8, Y: uint16(b[2]>>4) | uint16(b[3])<<4}
	}
	var gyro, accel [3]int16
	for i := range 3 {
		gyro[i] = int16(binary.LittleEndian.Uint16(report[16+i*2:]))
		accel[i] = int16(binary.LittleEndian.Uint16(report[22+i*2:]))
	}

	return protocol.Input{
		Buttons:           btn,
		Left:              stick(report[offLX], report[offLY], deadzone),
		Right:             stick(report[offRX], report[offRY], deadzone),
		LeftTrigger:       report[offL2],
		RightTrigger:      report[offR2],
		AnalogTriggers:    true,
		BatteryBand:       BatteryBand(report[offBattery]&0x0F, report[offBattery]>>4),
		BatteryDetail:     uint16(report[offBattery]) + 1,
		DualSenseGyroRaw:  gyro,
		DualSenseAccelRaw: accel,
		Touch:             touch,
	}, true
}

// hatButtons turns the clockwise hat index (0 = N ... 7 = NW) into D-pad bits.
// Values 8 to 15 mean released.
func hatButtons(h byte) uint32 {
	switch h {
	case 0:
		return protocol.BtnUp
	case 1:
		return protocol.BtnUp | protocol.BtnRight
	case 2:
		return protocol.BtnRight
	case 3:
		return protocol.BtnDown | protocol.BtnRight
	case 4:
		return protocol.BtnDown
	case 5:
		return protocol.BtnDown | protocol.BtnLeft
	case 6:
		return protocol.BtnLeft
	case 7:
		return protocol.BtnUp | protocol.BtnLeft
	}
	return 0
}

// axis maps a raw stick byte to [-1, 1] with 128 as exact centre: 255 gives +1
// and 0 gives -1.
func axis(raw byte) float64 {
	if raw >= 128 {
		return float64(int(raw)-128) / 127
	}
	return float64(int(raw)-128) / 128
}

// stick converts one raw stick pair (Y as reported: 0 = up) to a normalized
// stick with +Y up, zeroed inside the radial deadzone.
func stick(rawX, rawY byte, deadzone float64) protocol.Stick {
	x, y := axis(rawX), -axis(rawY)
	if math.Hypot(x, y) <= deadzone {
		return protocol.Stick{}
	}
	return protocol.Stick{X: x, Y: y}
}

// BatteryBand classifies the battery byte at offset 53: level is its low nibble
// (0..10, one unit = 10 %) and status its high nibble. Statuses 0x0A, 0x0B and
// 0x0F (abnormal voltage, abnormal temperature, charging error or unknown) give
// BatteryUnknown.
func BatteryBand(level, status byte) protocol.BatteryBand {
	switch status {
	case 0x02:
		return protocol.BatteryHigh
	case 0x00, 0x01:
	default:
		return protocol.BatteryUnknown
	}
	percent := min(int(level)*10+5, 100)
	switch {
	case percent >= 60:
		return protocol.BatteryHigh
	case percent >= 25:
		return protocol.BatteryMedium
	default:
		return protocol.BatteryLow
	}
}
