package virtualpad

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad"
)

func TestEncodeDualSenseRoundTrip(t *testing.T) {
	touch := [2]protocol.TouchContact{
		{Active: true, ID: 3, X: 0x345, Y: 0x678},
		{Active: false, ID: 4},
	}
	frame := mapping.Frame{
		Player: protocol.Input{
			Buttons: protocol.BtnB | protocol.BtnA | protocol.BtnL | protocol.BtnRStick |
				protocol.BtnPlus | protocol.BtnHome | protocol.BtnPSCClick | protocol.BtnUp | protocol.BtnRight,
			Left:           protocol.Stick{X: 0.5, Y: 0.5},
			Right:          protocol.Stick{X: -0.5, Y: -0.25},
			LeftTrigger:    200,
			RightTrigger:   100,
			AnalogTriggers: true,
			Touch:          touch,
		},
		PlayerMotion: mapping.Motion{
			Gyro:  [3]float32{100, 200, -300},
			Accel: [3]float32{1, 0.5, -0.25},
		},
	}
	report := encodeDualSense(frame)
	if len(report) != 64 || report[0] != 0x01 {
		t.Fatalf("report length %d id %#x", len(report), report[0])
	}
	in, ok := dualsense.Parse(report)
	if !ok {
		t.Fatal("DualSense parser rejected the encoded report")
	}
	for _, button := range []uint32{
		protocol.BtnB, protocol.BtnA, protocol.BtnL, protocol.BtnRStick,
		protocol.BtnPlus, protocol.BtnHome, protocol.BtnPSCClick,
		protocol.BtnUp, protocol.BtnRight,
	} {
		if in.Buttons&button == 0 {
			t.Errorf("button %#x missing from parsed report %#x", button, in.Buttons)
		}
	}
	if math.Abs(in.Left.X-0.5) > 0.01 || math.Abs(in.Left.Y-0.5) > 0.01 {
		t.Errorf("left stick %+v, want 0.5/0.5", in.Left)
	}
	if math.Abs(in.Right.X+0.5) > 0.01 || math.Abs(in.Right.Y+0.25) > 0.01 {
		t.Errorf("right stick %+v, want -0.5/-0.25", in.Right)
	}
	if in.LeftTrigger != 200 || in.RightTrigger != 100 || !in.AnalogTriggers {
		t.Errorf("triggers %d/%d analog=%t", in.LeftTrigger, in.RightTrigger, in.AnalogTriggers)
	}
	if !in.Touch[0].Active || in.Touch[0].ID != 3 || in.Touch[0].X != 0x345 || in.Touch[0].Y != 0x678 {
		t.Errorf("touch 0 %+v", in.Touch[0])
	}
	if in.Touch[1].Active {
		t.Errorf("touch 1 %+v, want inactive", in.Touch[1])
	}
	if in.BatteryBand != protocol.BatteryHigh {
		t.Errorf("battery band %v, want high", in.BatteryBand)
	}
	wantGyro := [3]int16{50, -100, 150} // 2 dps per unit, DS4 yaw/roll sign undone
	for i, want := range wantGyro {
		if got := int16(binary.LittleEndian.Uint16(report[16+i*2:])); got != want {
			t.Errorf("gyro[%d] = %d, want %d", i, got, want)
		}
	}
	wantAccel := [3]int16{-8192, -4096, 2048}
	for i, want := range wantAccel {
		if got := int16(binary.LittleEndian.Uint16(report[22+i*2:])); got != want {
			t.Errorf("accel[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestEncodeDualSenseDigitalTriggers(t *testing.T) {
	report := encodeDualSense(mapping.Frame{Player: protocol.Input{
		Buttons: protocol.BtnZL | protocol.BtnZR,
	}})
	if report[5] != 255 || report[6] != 255 {
		t.Fatalf("digital triggers = %d/%d, want 255/255", report[5], report[6])
	}
}

func TestDualSenseBackendPad(t *testing.T) {
	dev := &fakeHIDDevice{feedback: make(chan hidpad.Feedback, 1)}
	old := openDualSensePad
	openDualSensePad = func() (hidpad.Device, error) { return dev, nil }
	defer func() { openDualSensePad = old }()

	rumbled := make(chan [2]uint8, 1)
	pad, err := (dualsenseBackend{}).NewPad(func(left, right uint8) {
		rumbled <- [2]uint8{left, right}
	})
	if err != nil {
		t.Fatal(err)
	}
	frame := mapping.Frame{Player: protocol.Input{
		Buttons: protocol.BtnB, Left: protocol.Stick{X: 0.5}, AnalogTriggers: true, LeftTrigger: 42,
	}}
	fp, ok := pad.(FramePad)
	if !ok {
		t.Fatal("DualSense pad does not implement FramePad")
	}
	if err := fp.UpdateFrame(frame); err != nil {
		t.Fatal(err)
	}
	if len(dev.reports) != 1 || len(dev.reports[0]) != 64 {
		t.Fatalf("reports %d", len(dev.reports))
	}
	if parsed, ok := dualsense.Parse(dev.reports[0]); !ok || parsed.Buttons&protocol.BtnB == 0 {
		t.Fatalf("submitted report was rejected: %+v", parsed)
	}
	dev.feedback <- hidpad.Feedback{Rumble: &hidpad.Rumble{Left: 9, Right: 4}}
	select {
	case got := <-rumbled:
		if got != [2]uint8{9, 4} {
			t.Fatalf("rumble %v, want 9/4", got)
		}
	case <-time.After(time.Second):
		t.Fatal("feedback was not forwarded to the rumble callback")
	}
	if err := pad.Close(); err != nil {
		t.Fatal(err)
	}
	if !dev.closed {
		t.Fatal("Close did not close the HID device")
	}
}

type fakeHIDDevice struct {
	reports  [][]byte
	feedback chan hidpad.Feedback
	closed   bool
}

func (f *fakeHIDDevice) ID() string                          { return "pad2go-5e7" }
func (f *fakeHIDDevice) Done() <-chan struct{}               { return nil }
func (f *fakeHIDDevice) Feedback() <-chan hidpad.Feedback    { return f.feedback }
func (f *fakeHIDDevice) Control() <-chan hidpad.ControlEvent { return nil }
func (f *fakeHIDDevice) SubmitReport(report []byte) error {
	f.reports = append(f.reports, append([]byte(nil), report...))
	return nil
}
func (f *fakeHIDDevice) Close() error {
	if !f.closed {
		f.closed = true
		close(f.feedback)
	}
	return nil
}

func TestXboxFrameAdapter(t *testing.T) {
	frame := xboxFrame(mapping.XboxState{
		Buttons: mapping.XBA | mapping.XBUp | mapping.XBStart,
		LX:      16384, LY: -8192,
		LeftTrigger: 77,
	})
	if frame.Player.Buttons&protocol.BtnB == 0 || frame.Player.Buttons&protocol.BtnUp == 0 ||
		frame.Player.Buttons&protocol.BtnPlus == 0 {
		t.Fatalf("buttons %#x", frame.Player.Buttons)
	}
	if math.Abs(frame.Player.Left.X-0.5) > 0.01 || math.Abs(frame.Player.Left.Y+0.25) > 0.01 {
		t.Fatalf("stick %+v", frame.Player.Left)
	}
	report := encodeDualSense(frame)
	if report[5] != 77 {
		t.Fatalf("left trigger %d, want 77", report[5])
	}
}
