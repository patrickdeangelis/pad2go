// DualSense virtual output: a report-level DualSense exposed through WinUHid
// on Windows or /dev/uhid on Linux. It uses the full mapped frame, so touch,
// motion and analog triggers reach the game, not only the Xbox state.
package virtualpad

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad"
)

type dualsenseBackend struct{}

func openDualSense() (Backend, error) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("output dualsense needs WinUHid (Windows) or /dev/uhid (Linux)")
	}
	if err := hidpad.Check(); err != nil {
		return nil, fmt.Errorf("DualSense virtual output unavailable: %w", err)
	}
	return dualsenseBackend{}, nil
}

func (dualsenseBackend) Name() string {
	if runtime.GOOS == "windows" {
		return "DualSense (WinUHid)"
	}
	return "DualSense (UHID)"
}

// Kind lets the UI name the output without parsing Name.
func (dualsenseBackend) Kind() string { return "dualsense" }

func (dualsenseBackend) Close() error { return nil }

func (dualsenseBackend) NewPad(onRumble RumbleFunc) (Pad, error) {
	dev, err := openDualSensePad()
	if err != nil {
		return nil, err
	}
	p := &dualsensePad{dev: dev, rumble: onRumble, done: make(chan struct{})}
	go p.feedbackLoop()
	return p, nil
}

var openDualSensePad = func() (hidpad.Device, error) {
	return hidpad.Open(hidpad.DualSenseWired, hidpad.Options{})
}

type dualsensePad struct {
	dev    hidpad.Device
	rumble RumbleFunc
	done   chan struct{}
	once   sync.Once
}

func (p *dualsensePad) Update(s mapping.XboxState) error {
	return p.dev.SubmitReport(encodeDualSense(xboxFrame(s)))
}

func (p *dualsensePad) UpdateFrame(f mapping.Frame) error {
	return p.dev.SubmitReport(encodeDualSense(f))
}

func (p *dualsensePad) Close() error {
	p.once.Do(func() { close(p.done) })
	return p.dev.Close()
}

func (p *dualsensePad) feedbackLoop() {
	for {
		select {
		case <-p.done:
			return
		case fb, ok := <-p.dev.Feedback():
			if !ok {
				return
			}
			if fb.Rumble != nil && p.rumble != nil {
				p.rumble(fb.Rumble.Left, fb.Rumble.Right)
			}
		}
	}
}

// xboxFrame adapts a backend that only knows Xbox state. The full app path
// uses UpdateFrame with touch and motion.
func xboxFrame(s mapping.XboxState) mapping.Frame {
	var buttons uint32
	for _, m := range []struct {
		src uint16
		dst uint32
	}{
		{mapping.XBA, protocol.BtnB}, {mapping.XBB, protocol.BtnA},
		{mapping.XBX, protocol.BtnX}, {mapping.XBY, protocol.BtnY},
		{mapping.XBUp, protocol.BtnUp}, {mapping.XBDown, protocol.BtnDown},
		{mapping.XBLeft, protocol.BtnLeft}, {mapping.XBRight, protocol.BtnRight},
		{mapping.XBStart, protocol.BtnPlus}, {mapping.XBBack, protocol.BtnMinus},
		{mapping.XBGuide, protocol.BtnHome}, {mapping.XBLStick, protocol.BtnLStick},
		{mapping.XBRStick, protocol.BtnRStick}, {mapping.XBLB, protocol.BtnL},
		{mapping.XBRB, protocol.BtnR},
	} {
		if s.Buttons&m.src != 0 {
			buttons |= m.dst
		}
	}
	return mapping.Frame{Player: protocol.Input{
		Buttons:        buttons,
		Left:           protocol.Stick{X: float64(s.LX) / 32767, Y: float64(s.LY) / 32767},
		Right:          protocol.Stick{X: float64(s.RX) / 32767, Y: float64(s.RY) / 32767},
		LeftTrigger:    s.LeftTrigger,
		RightTrigger:   s.RightTrigger,
		AnalogTriggers: true,
	}}
}

// encodeDualSense builds the 64-byte USB input report (report ID 0x01) from
// the mapped frame. Offsets follow the DualSense USB descriptor.
func encodeDualSense(f mapping.Frame) []byte {
	in := f.Player
	report := make([]byte, 64)
	report[0] = 0x01

	var face byte
	if in.Buttons&protocol.BtnB != 0 {
		face |= 0x20 // Cross
	}
	if in.Buttons&protocol.BtnA != 0 {
		face |= 0x40 // Circle
	}
	if in.Buttons&protocol.BtnY != 0 {
		face |= 0x10 // Square
	}
	if in.Buttons&protocol.BtnX != 0 {
		face |= 0x80 // Triangle
	}
	report[8] = face | dualSenseHat(in.Buttons)

	var shoulder byte
	if in.Buttons&protocol.BtnL != 0 {
		shoulder |= 0x01
	}
	if in.Buttons&protocol.BtnR != 0 {
		shoulder |= 0x02
	}
	if in.Buttons&protocol.BtnZL != 0 {
		shoulder |= 0x04
	}
	if in.Buttons&protocol.BtnZR != 0 {
		shoulder |= 0x08
	}
	if in.Buttons&protocol.BtnMinus != 0 {
		shoulder |= 0x10
	}
	if in.Buttons&protocol.BtnPlus != 0 {
		shoulder |= 0x20
	}
	if in.Buttons&protocol.BtnLStick != 0 {
		shoulder |= 0x40
	}
	if in.Buttons&protocol.BtnRStick != 0 {
		shoulder |= 0x80
	}
	report[9] = shoulder

	var system byte
	if in.Buttons&protocol.BtnHome != 0 {
		system |= 0x01
	}
	if in.Buttons&protocol.BtnPSCClick != 0 {
		system |= 0x02
	}
	if in.Buttons&protocol.BtnMute != 0 {
		system |= 0x04
	}
	report[10] = system

	report[1] = stickByte(in.Left.X)
	report[2] = stickByte(-in.Left.Y)
	report[3] = stickByte(in.Right.X)
	report[4] = stickByte(-in.Right.Y)
	if in.AnalogTriggers {
		report[5], report[6] = in.LeftTrigger, in.RightTrigger
	} else {
		if in.Buttons&protocol.BtnZL != 0 {
			report[5] = 255
		}
		if in.Buttons&protocol.BtnZR != 0 {
			report[6] = 255
		}
	}

	for i, touch := range in.Touch {
		base := 33 + i*4
		if !touch.Active {
			report[base] = 0x80
			continue
		}
		report[base] = touch.ID & 0x7f
		report[base+1] = byte(touch.X)
		report[base+2] = byte(touch.X>>8)&0x0f | byte(touch.Y&0x0f)<<4
		report[base+3] = byte(touch.Y >> 4)
	}

	// PlayerMotion is in DS4 axes; undo that convention and encode against
	// this device's feature 0x05 calibration: 2 dps per gyro unit and 8192
	// accel units per g.
	motion := f.PlayerMotion
	for i := range 3 {
		gyro := float64(motion.Gyro[i])
		if i != 0 {
			gyro = -gyro
		}
		accel := -float64(motion.Accel[i])
		binary.LittleEndian.PutUint16(report[16+i*2:], uint16(clampInt16(math.Round(gyro/2))))
		binary.LittleEndian.PutUint16(report[22+i*2:], uint16(clampInt16(math.Round(accel*8192))))
	}

	report[53] = 0x2A // level 10 (100%), status 2 (discharging/full)
	return report
}

func dualSenseHat(buttons uint32) byte {
	up := buttons&protocol.BtnUp != 0
	down := buttons&protocol.BtnDown != 0
	left := buttons&protocol.BtnLeft != 0
	right := buttons&protocol.BtnRight != 0
	switch {
	case up && right:
		return 1
	case right && !down:
		return 2
	case down && right:
		return 3
	case down && !left:
		return 4
	case down && left:
		return 5
	case left && !up:
		return 6
	case up && left:
		return 7
	case up:
		return 0
	default:
		return 8
	}
}

func stickByte(v float64) byte {
	v = math.Max(-1, math.Min(1, v))
	raw := 128 + math.Round(v*127)
	if v < 0 {
		raw = 128 + math.Round(v*128)
	}
	return byte(max(0, min(255, int(raw))))
}

func clampInt16(v float64) int16 {
	return int16(math.Max(-32768, math.Min(32767, v)))
}
