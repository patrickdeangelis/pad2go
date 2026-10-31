package dualsense

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestBluetoothDeviceInputAndOutput(t *testing.T) {
	ft := newFakeTransport()
	d := NewBluetoothDevice(ft, "test", nil)
	defer d.Close()
	inputs := make(chan protocol.Input, 1)
	d.OnInput(func(in protocol.Input) { inputs <- in })
	var report [bluetoothReportLen]byte
	report[0], report[1] = 0x31, 0x10
	copy(report[2:8], []byte{255, 128, 0, 128, 55, 190})
	report[9], report[10], report[54] = 0x28, 0x22, 6
	binary.LittleEndian.PutUint32(report[74:], bluetoothCRC(0xa1, report[:74]))
	ft.reads <- report[:]
	select {
	case <-inputs:
	case <-time.After(time.Second):
		t.Fatal("Bluetooth device did not publish a valid input report")
	}
	if err := d.SetPlayerLEDs(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	out := ft.lastWrite(t)
	if len(out) != bluetoothReportLen || out[0] != 0x31 || out[46] != 4 || bluetoothCRC(0xa2, out[:74]) != binary.LittleEndian.Uint32(out[74:]) {
		t.Fatalf("invalid Bluetooth LED report: %x", out)
	}
	ft.mu.Lock()
	if len(ft.writes) != 2 || ft.writes[0][41] != 2 || ft.writes[0][44] != 2 || ft.writes[0][1] != 0 || out[1] != 0x10 || bluetoothCRC(0xa2, ft.writes[0][:74]) != binary.LittleEndian.Uint32(ft.writes[0][74:]) {
		ft.mu.Unlock()
		t.Fatal("expected a valid Bluetooth LED reset followed by player LEDs")
	}
	ft.mu.Unlock()
	if err := d.SetPlayerLEDs(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if len(ft.writes) != 3 || ft.writes[2][46] != 0x0a {
		t.Fatal("repeated LED update reset the controller or used the wrong pattern")
	}
}

func TestBluetoothReadFailureClosesTransport(t *testing.T) {
	ft := newFakeTransport()
	d := NewBluetoothDevice(ft, "test", nil)
	ft.unplug()
	select {
	case <-d.Done():
	case <-time.After(time.Second):
		t.Fatal("device did not stop after read failure")
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if len(ft.events) != 1 || ft.events[0] != "close" {
		t.Fatalf("transport was not closed after read failure: %v", ft.events)
	}
}

func TestBluetoothInputFramingAndCRC(t *testing.T) {
	var report [bluetoothReportLen]byte
	report[0], report[1] = 0x31, 0x10
	copy(report[2:8], []byte{255, 128, 0, 128, 55, 190})
	report[9], report[10], report[54] = 0x28, 0x22, 6
	// Independently calculated IEEE CRC-32 over 0xa1 followed by bytes 0..73.
	binary.LittleEndian.PutUint32(report[74:], 0xe7e43f21)
	in, ok := ParseBluetooth(report[:])
	if !ok || in.Buttons != protocol.BtnB|protocol.BtnR|protocol.BtnPlus || in.Left.X != 1 || in.Right.X != -1 || in.LeftTrigger != 55 || in.RightTrigger != 190 || in.BatteryBand != protocol.BatteryHigh {
		t.Fatalf("Bluetooth report decoded as %+v, ok=%v", in, ok)
	}
	report[9] ^= 1
	if _, ok := ParseBluetooth(report[:]); ok {
		t.Fatal("accepted corrupted Bluetooth report")
	}
	if _, ok := ParseBluetooth(report[:77]); ok {
		t.Fatal("accepted truncated Bluetooth report")
	}
}

func TestBluetoothOutputFramingAndCRC(t *testing.T) {
	report := bluetoothOutputReport(201, 29, 4, true, 3)
	if report[0] != 0x31 || report[1] != 0x30 || report[2] != 0x10 || report[3] != 3 || report[4] != 0x10 || report[5] != 29 || report[6] != 201 || report[46] != 4 {
		t.Fatalf("incorrect Bluetooth output framing: %x", report)
	}
	if got := binary.LittleEndian.Uint32(report[74:]); got != 0x091a425f {
		t.Fatalf("CRC = %#x; want independently calculated 0x091a425f", got)
	}
}
