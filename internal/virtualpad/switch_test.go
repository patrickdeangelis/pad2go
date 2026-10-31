package virtualpad

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestEncodeSwitchButtonsSticksAndMotion(t *testing.T) {
	frame := mapping.Frame{
		Player: protocol.Input{
			Buttons: protocol.BtnA | protocol.BtnB | protocol.BtnPlus | protocol.BtnHome |
				protocol.BtnC | protocol.BtnUp | protocol.BtnL | protocol.BtnZL,
			Left:  protocol.Stick{X: 1, Y: 1},
			Right: protocol.Stick{},
		},
		PlayerMotion: mapping.Motion{
			Accel: [3]float32{1, 0, 0},
			Gyro:  [3]float32{0, 0, 90},
		},
	}

	report := encodeSwitch(frame)
	if len(report) != 64 || report[0] != 0x30 {
		t.Fatalf("report header = %d bytes, ID 0x%02x", len(report), report[0])
	}
	if report[2] != 0x8E || report[12] != 0x08 {
		t.Fatalf("status bytes = % x", []byte{report[2], report[12]})
	}
	if got := [3]byte{report[3], report[4], report[5]}; got != [3]byte{0x0C, 0x52, 0xC2} {
		t.Fatalf("buttons = % x, want 0c 52 c2", got)
	}

	// Full right/up maps to center 2048 + SDL's 0.7-scaled range (1433).
	if got := report[6:9]; !bytes.Equal(got, []byte{0x99, 0x9D, 0xD9}) {
		t.Fatalf("left stick = % x, want 99 9d d9", got)
	}
	if got := report[9:12]; !bytes.Equal(got, []byte{0x00, 0x08, 0x80}) {
		t.Fatalf("right stick = % x, want neutral", got)
	}

	// Accel X = -1 g and roll = 90 dps, expressed in the Pro Controller's
	// raw axes (roll is the second gyro axis there).
	if got := int16(binary.LittleEndian.Uint16(report[13:15])); got != -4096 {
		t.Fatalf("accel X = %d, want -4096", got)
	}
	if got := int16(binary.LittleEndian.Uint16(report[21:23])); got != 1286 {
		t.Fatalf("gyro Y = %d, want 1286", got)
	}
	for sample := 1; sample < 3; sample++ {
		if !bytes.Equal(report[13:25], report[13+sample*12:25+sample*12]) {
			t.Fatalf("IMU sample %d differs from sample 0", sample)
		}
	}
}

func TestEncodeSwitchTriggersSetDigitalBits(t *testing.T) {
	report := encodeSwitch(mapping.Frame{Player: protocol.Input{
		LeftTrigger: 200, RightTrigger: 255,
	}})
	if report[5]&0x80 == 0 {
		t.Fatalf("ZL bit missing from byte 5: 0x%02x", report[5])
	}
	if report[3]&0x80 == 0 {
		t.Fatalf("ZR bit missing from byte 3: 0x%02x", report[3])
	}
}

func TestSwitchPoolKind(t *testing.T) {
	if got := Name(OutputSwitch); got != "Switch Pro" {
		t.Fatalf("Name(switch) = %q", got)
	}
	key, err := backendKey(OutputSwitch)
	if err != nil || key != "switch" {
		t.Fatalf("backendKey(switch) = %q, %v", key, err)
	}
}
