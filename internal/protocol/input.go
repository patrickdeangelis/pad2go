package protocol

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

func u16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func s16(b []byte) int16  { return int16(binary.LittleEndian.Uint16(b)) }

// StickXY unpacks a 3-byte packed 12-bit X/Y stick value.
func StickXY(b []byte) (x, y int) {
	v := int(b[0]) | int(b[1])<<8 | int(b[2])<<16
	return v & 0xFFF, v >> 12
}

// Advertisement is the decoded Nintendo manufacturer data of a BLE advertisement.
type Advertisement struct {
	VendorID  uint16
	ProductID uint16
	// ReconnectMAC is the host the controller is bonded to (0 while in pairing mode).
	ReconnectMAC uint64
}

// ParseAdvertisement decodes manufacturer data for company ID 0x0553.
func ParseAdvertisement(data []byte) (Advertisement, error) {
	if len(data) < 16 {
		return Advertisement{}, errors.New("manufacturer data too short")
	}
	var mac uint64
	for i := range 6 {
		mac |= uint64(data[10+i]) << (8 * i)
	}
	return Advertisement{VendorID: u16(data[3:5]), ProductID: u16(data[5:7]), ReconnectMAC: mac}, nil
}

// Supported reports whether the advertisement is from a supported Nintendo controller.
func (a Advertisement) Supported() bool {
	_, ok := ControllerNames[a.ProductID]
	return a.VendorID == NintendoVendorID && ok
}

// Pairing reports whether the controller is in pairing (sync) mode.
func (a Advertisement) Pairing() bool { return a.ReconnectMAC == 0 }

// ControllerInfo is the device information block at AddrControllerInfo.
type ControllerInfo struct {
	SerialNumber string
	VendorID     uint16
	ProductID    uint16
	Colors       [4][3]byte
}

// ParseControllerInfo decodes a controller info block (at least 37 bytes).
func ParseControllerInfo(b []byte) (ControllerInfo, error) {
	if len(b) < 37 {
		return ControllerInfo{}, errors.New("controller info too short")
	}
	info := ControllerInfo{
		SerialNumber: strings.TrimRight(string(b[2:16]), "\x00"),
		VendorID:     u16(b[18:20]),
		ProductID:    u16(b[20:22]),
	}
	for i := range 4 {
		copy(info.Colors[i][:], b[25+3*i:28+3*i])
	}
	return info, nil
}

// StickCalibration holds a stick's center and positive/negative ranges.
type StickCalibration struct {
	CenterX, CenterY int
	MaxX, MaxY       int // range above center
	MinX, MinY       int // range below center
	Valid            bool
}

// DefaultStickCalibration is the conservative fallback used when factory data is invalid.
func DefaultStickCalibration() StickCalibration {
	return StickCalibration{2048, 2048, 1500, 1500, 1500, 1500, false}
}

// FixedStickCalibration is the full-range calibration used for the NSO GameCube controller.
func FixedStickCalibration() StickCalibration {
	return StickCalibration{2048, 2048, 2047, 2047, 2047, 2047, true}
}

// ParseStickCalibration decodes 9 bytes of factory/user stick calibration,
// falling back to DefaultStickCalibration when the data is implausible.
func ParseStickCalibration(b []byte) StickCalibration {
	if len(b) < 9 {
		return DefaultStickCalibration()
	}
	cx, cy := StickXY(b[0:3])
	nx, ny := StickXY(b[3:6])
	mx, my := StickXY(b[6:9])
	invalidCenter := (cx == 0 && cy == 0 && mx == 0 && my == 0) ||
		(cx == 4095 && cy == 4095 && mx == 4095 && my == 4095) ||
		cx < 1024 || cx > 3072 || cy < 1024 || cy > 3072
	invalidRange := mx <= 0 || my <= 0 || nx <= 0 || ny <= 0 ||
		mx > 4095-cx || my > 4095-cy || nx > cx || ny > cy
	if invalidCenter || invalidRange {
		return DefaultStickCalibration()
	}
	return StickCalibration{cx, cy, mx, my, nx, ny, true}
}

func axis(raw, center, maxAbs, minAbs int) float64 {
	v := raw - center
	if v >= 0 {
		return math.Min(float64(v)/float64(max(maxAbs, 1)), 1)
	}
	return -math.Min(float64(-v)/float64(max(minAbs, 1)), 1)
}

func clamp1(v float64) float64 { return math.Max(-1, math.Min(1, v)) }

// Apply converts raw 12-bit stick values to [-1,1] with a radial deadzone.
func (c StickCalibration) Apply(rawX, rawY int, gain, deadzone float64) (float64, float64) {
	x := clamp1(axis(rawX, c.CenterX, c.MaxX, c.MinX) * gain)
	y := clamp1(axis(rawY, c.CenterY, c.MaxY, c.MinY) * gain)
	if math.Hypot(x, y) < deadzone {
		return 0, 0
	}
	return x, y
}

// GameCube trigger modes (config key gc_trigger_mode).
const (
	GCTriggerHair = "Hair Trigger"
	GCTriggerBump = "100% at Bump"
	GCTriggerMax  = "100% at Max"
)

// DefaultGCTriggerCalibration is [Lmin, Lbump, Lmax, Rmin, Rbump, Rmax].
var DefaultGCTriggerCalibration = []int{36, 190, 240, 36, 190, 240}

// Report is a decoded input report. Sticks are raw 12-bit values; use
// StickCalibration.Apply to normalize them.
type Report struct {
	Time           uint32
	Buttons        uint32
	LeftStickRaw   [2]int
	RightStickRaw  [2]int
	MouseX, MouseY uint16
	MouseRoughness uint16
	MouseDistance  uint16
	Magnetometer   [3]int16
	BatteryVoltage float64 // volts
	BatteryCurrent float64 // amps
	Temperature    float64 // °C
	Accel          [3]int16
	Gyro           [3]int16
	// Analog triggers (NSO GameCube only), 0-255.
	LeftTrigger, RightTrigger       uint8
	LeftTriggerRaw, RightTriggerRaw uint8
}

// ParseOptions configures report decoding.
type ParseOptions struct {
	ProductID            uint16
	GCTriggerMode        string
	GCTriggerCalibration []int
}

// ErrShortReport is returned for reports too short to decode.
var ErrShortReport = errors.New("input report too short")

// ParseReport decodes an input notification.
func ParseReport(data []byte, opt ParseOptions) (Report, error) {
	if opt.ProductID == NSOGameCubeControllerPID {
		return parseGameCube(data, opt)
	}
	if len(data) < 60 {
		return Report{}, ErrShortReport
	}
	var r Report
	r.Time = binary.LittleEndian.Uint32(data[0:4])
	r.Buttons = binary.LittleEndian.Uint32(data[4:8]) & PhysicalButtonMask
	r.LeftStickRaw[0], r.LeftStickRaw[1] = StickXY(data[10:13])
	r.RightStickRaw[0], r.RightStickRaw[1] = StickXY(data[13:16])
	r.MouseX, r.MouseY = u16(data[16:18]), u16(data[18:20])
	r.MouseRoughness = u16(data[20:22])
	r.MouseDistance = u16(data[22:24])
	r.Magnetometer = [3]int16{s16(data[25:27]), s16(data[27:29]), s16(data[29:31])}
	r.BatteryVoltage = float64(u16(data[31:33])) / 1000
	r.BatteryCurrent = float64(u16(data[33:35])) / 100
	r.Temperature = 25 + float64(u16(data[46:48]))/127
	r.Accel = [3]int16{s16(data[48:50]), s16(data[50:52]), s16(data[52:54])}
	r.Gyro = [3]int16{s16(data[54:56]), s16(data[56:58]), s16(data[58:60])}
	return r, nil
}

func remapTrigger(v, minIn, maxIn int) uint8 {
	if v < minIn {
		v = minIn
	}
	if v > maxIn {
		v = maxIn
	}
	if maxIn <= minIn {
		return 0
	}
	return uint8(float64(v-minIn) / float64(maxIn-minIn) * 255)
}

func parseGameCube(data []byte, opt ParseOptions) (Report, error) {
	if len(data) < 14 {
		return Report{}, ErrShortReport
	}
	var r Report
	r.Time = uint32(data[0])
	b1, b2, b3 := data[2], data[3], data[4]
	bits := []struct {
		b    byte
		mask byte
		btn  uint32
	}{
		{b1, 0x01, BtnB}, {b1, 0x02, BtnA}, {b1, 0x04, BtnY}, {b1, 0x08, BtnX},
		{b1, 0x10, BtnZR}, {b1, 0x20, BtnR}, {b1, 0x40, BtnPlus},
		{b2, 0x01, BtnDown}, {b2, 0x02, BtnRight}, {b2, 0x04, BtnLeft}, {b2, 0x08, BtnUp},
		{b2, 0x10, BtnZL}, {b2, 0x20, BtnL},
		{b3, 0x01, BtnHome}, {b3, 0x02, BtnCapture}, {b3, 0x10, BtnC},
	}
	for _, e := range bits {
		if e.b&e.mask != 0 {
			r.Buttons |= e.btn
		}
	}
	r.LeftStickRaw[0], r.LeftStickRaw[1] = StickXY(data[5:8])
	r.RightStickRaw[0], r.RightStickRaw[1] = StickXY(data[8:11])

	cal := opt.GCTriggerCalibration
	if len(cal) == 4 {
		cal = []int{cal[0], cal[1], cal[1], cal[2], cal[3], cal[3]}
	}
	if len(cal) < 6 {
		cal = DefaultGCTriggerCalibration
	}
	mode := opt.GCTriggerMode
	if mode == "" {
		mode = GCTriggerBump
	}
	lMax, rMax := cal[2], cal[5]
	if mode == GCTriggerBump {
		lMax, rMax = cal[1], cal[4]
	}
	r.LeftTriggerRaw, r.RightTriggerRaw = data[12], data[13]
	if mode == GCTriggerHair {
		lThresh := float64(cal[0]) + float64(cal[2]-cal[0])*0.05
		rThresh := float64(cal[3]) + float64(cal[5]-cal[3])*0.05
		if float64(r.LeftTriggerRaw) >= lThresh {
			r.LeftTrigger = 255
		}
		if float64(r.RightTriggerRaw) >= rThresh {
			r.RightTrigger = 255
		}
	} else {
		r.LeftTrigger = remapTrigger(int(r.LeftTriggerRaw), cal[0], lMax)
		r.RightTrigger = remapTrigger(int(r.RightTriggerRaw), cal[3], rMax)
	}
	// Past 50% the analog triggers also set the digital ZL/ZR bits.
	if r.LeftTrigger >= 128 {
		r.Buttons |= BtnZL
	}
	if r.RightTrigger >= 128 {
		r.Buttons |= BtnZR
	}
	// The digital "click" at the end of the trigger travel is also exposed as
	// its own button unless the trigger only reaches 100% at the click.
	if mode != GCTriggerMax {
		if b1&0x10 != 0 {
			r.Buttons |= BtnGCRClick
		}
		if b2&0x10 != 0 {
			r.Buttons |= BtnGCLClick
		}
	}
	// The GameCube pad has no MINUS or stick clicks.
	r.Buttons &^= BtnMinus | BtnRStick | BtnLStick

	r.BatteryVoltage = 3.7
	r.Temperature = 25
	if len(data) >= 46 {
		r.Accel = [3]int16{s16(data[34:36]), s16(data[36:38]), s16(data[38:40])}
		r.Gyro = [3]int16{s16(data[40:42]), s16(data[42:44]), s16(data[44:46])}
	}
	return r, nil
}

// BatteryPercent estimates charge from pack voltage (3.3 V empty, 4.2 V full).
func BatteryPercent(volts float64) int {
	p := (volts - 3.3) / (4.2 - 3.3) * 100
	return int(math.Max(0, math.Min(100, math.Round(p))))
}
