// Switch Pro virtual output: a report-level Pro Controller exposed through
// WinUHid on Windows or /dev/uhid on Linux. Switch emulators (yuzu, Ryujinx
// and their forks) discover it through SDL's HIDAPI Switch driver, which needs
// the 0x30 input stream and the 0x01/0x21 subcommand handshake.
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
	"github.com/patrickdeangelis/pad2go/internal/virtualpad/switchpad"
)

type switchBackend struct{}

func openSwitch() (Backend, error) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("output switch needs WinUHid (Windows) or /dev/uhid (Linux)")
	}
	if err := hidpad.Check(); err != nil {
		return nil, fmt.Errorf("Switch virtual output unavailable: %w", err)
	}
	return switchBackend{}, nil
}

func (switchBackend) Name() string {
	if runtime.GOOS == "windows" {
		return "Switch Pro (WinUHid)"
	}
	return "Switch Pro (UHID)"
}

// Kind lets the UI name the output without parsing Name.
func (switchBackend) Kind() string { return "switch" }

func (switchBackend) Close() error { return nil }

func (switchBackend) NewPad(onRumble RumbleFunc) (Pad, error) {
	dev, err := openSwitchPad()
	if err != nil {
		return nil, err
	}
	p := &switchPad{dev: dev, rumble: onRumble, done: make(chan struct{})}
	go p.feedbackLoop()
	return p, nil
}

var openSwitchPad = func() (*switchpad.Device, error) {
	return switchpad.Open(switchpad.Options{})
}

type switchPad struct {
	dev    *switchpad.Device
	rumble RumbleFunc
	done   chan struct{}
	once   sync.Once
}

func (p *switchPad) Update(s mapping.XboxState) error {
	return p.dev.SubmitReport(encodeSwitch(xboxFrame(s)))
}

func (p *switchPad) UpdateFrame(f mapping.Frame) error {
	return p.dev.SubmitReport(encodeSwitch(f))
}

func (p *switchPad) Close() error {
	p.once.Do(func() { close(p.done) })
	return p.dev.Close()
}

func (p *switchPad) feedbackLoop() {
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

// encodeSwitch builds the 64-byte 0x30 input report. Buttons use the Switch
// bit layout already carried by mapping.Input; sticks are 12-bit values packed
// two per three bytes; three IMU samples follow the controller state.
func encodeSwitch(f mapping.Frame) []byte {
	in := f.Player
	report := make([]byte, 64)
	report[0] = 0x30
	report[2] = 0x8E // full battery, wired

	buttons := in.Buttons
	if in.LeftTrigger >= 128 {
		buttons |= protocol.BtnZL
	}
	if in.RightTrigger >= 128 {
		buttons |= protocol.BtnZR
	}
	report[3] = byte(buttons)
	report[4] = byte(buttons >> 8)
	report[5] = byte(buttons >> 16)

	putSwitchStick(report[6:9], in.Left.X, in.Left.Y)
	putSwitchStick(report[9:12], in.Right.X, in.Right.Y)
	report[12] = 0x08 // vibrator input enabled

	for i := 0; i < 3; i++ {
		putSwitchIMU(report[13+i*12:25+i*12], f.PlayerMotion)
	}
	return report
}

// putSwitchStick writes the controller's 12-bit X/Y packing. The advertised
// factory calibration uses center 2048 and SDL scales the range by 0.7, so
// +/-1433 reaches the driver's full-scale value.
func putSwitchStick(dst []byte, x, y float64) {
	const center, extent = 2048.0, 1433.0
	rawX := int(math.Round(center + clampUnit(x)*extent))
	rawY := int(math.Round(center + clampUnit(y)*extent))
	rawX = max(0, min(4095, rawX))
	rawY = max(0, min(4095, rawY))
	dst[0] = byte(rawX)
	dst[1] = byte(rawX>>8)&0x0F | byte(rawY&0x0F)<<4
	dst[2] = byte(rawY >> 4)
}

// putSwitchIMU converts the common DS4-axis motion sample back to the Pro
// Controller's raw axes. SDL's calibration (4096 counts per g, 13371/936
// counts per degree/s) turns the values back into m/s² and rad/s.
func putSwitchIMU(dst []byte, motion mapping.Motion) {
	const accelScale = 4096.0
	const gyroScale = 14.2842
	accel := [3]float64{
		-float64(motion.Accel[0]) * accelScale,
		float64(motion.Accel[2]) * accelScale,
		-float64(motion.Accel[1]) * accelScale,
	}
	gyro := [3]float64{
		float64(motion.Gyro[0]) * gyroScale,
		float64(motion.Gyro[2]) * gyroScale,
		-float64(motion.Gyro[1]) * gyroScale,
	}
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(clampInt16(math.Round(accel[i]))))
	}
	for i := 0; i < 3; i++ {
		binary.LittleEndian.PutUint16(dst[6+i*2:], uint16(clampInt16(math.Round(gyro[i]))))
	}
}

func clampUnit(v float64) float64 {
	return math.Max(-1, math.Min(1, v))
}
