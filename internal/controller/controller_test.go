package controller

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/angelispatrick/switch2connect-go/internal/protocol"
)

// fakeDevice simulates a Switch 2 controller's GATT side.
type fakeDevice struct {
	pid    uint16
	memory map[uint32][]byte
	// failCmds makes these commands answer with an error status.
	failCmds map[[2]byte]bool

	mu     sync.Mutex
	subs   map[string]func([]byte)
	writes []write
	closed bool
}

type write struct {
	uuid string
	data []byte
}

func packStick(x, y int) []byte {
	v := x | y<<12
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

func newFakeDevice(pid uint16) *fakeDevice {
	info := make([]byte, 0x40)
	copy(info[2:], "SERIAL00000001")
	binary.LittleEndian.PutUint16(info[18:], protocol.NintendoVendorID)
	binary.LittleEndian.PutUint16(info[20:], pid)
	cal := append(append(packStick(2000, 2000), packStick(1500, 1500)...), packStick(1500, 1500)...)
	cal = append(cal, 0, 0)
	return &fakeDevice{
		pid: pid,
		memory: map[uint32][]byte{
			protocol.AddrControllerInfo:           info,
			protocol.AddrUserCalibrationJoystick1: {0xFF, 0xFF, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0},
			protocol.AddrCalibrationJoystick1:     cal,
			protocol.AddrUserCalibrationJoystick2: cal,
		},
		failCmds: map[[2]byte]bool{},
		subs:     map[string]func([]byte){},
	}
}

func (f *fakeDevice) Address() string { return "AA:BB:CC:DD:EE:FF" }

func (f *fakeDevice) Subscribe(uuid string, fn func([]byte)) error {
	f.mu.Lock()
	f.subs[uuid] = fn
	f.mu.Unlock()
	return nil
}

func (f *fakeDevice) Disconnect() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeDevice) notify(uuid string, data []byte) {
	f.mu.Lock()
	fn := f.subs[uuid]
	f.mu.Unlock()
	if fn != nil {
		fn(data)
	}
}

func (f *fakeDevice) Write(uuid string, data []byte, _ bool) error {
	f.mu.Lock()
	f.writes = append(f.writes, write{uuid, append([]byte(nil), data...)})
	f.mu.Unlock()
	if uuid != protocol.CommandWriteUUID || len(data) < 8 || data[1] != 0x91 {
		return nil // raw writes (input mode, rumble) get no response
	}
	cmd, sub := data[0], data[3]
	resp := []byte{cmd, 0x01, 0, 0, 0, 0, 0, 0}
	if f.failCmds[[2]byte{cmd, sub}] {
		resp[1] = 0x04
	}
	if cmd == protocol.CmdMemory && sub == protocol.SubMemoryRead {
		arg := data[8:]
		length, addr := arg[0], binary.LittleEndian.Uint32(arg[4:8])
		mem := make([]byte, int(length))
		copy(mem, f.memory[addr])
		resp = append(resp, arg[:8]...)
		resp = append(resp, mem...)
	}
	go f.notify(protocol.CommandResponseUUID, resp)
	return nil
}

func (f *fakeDevice) commands() [][2]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][2]byte
	for _, w := range f.writes {
		if w.uuid == protocol.CommandWriteUUID && len(w.data) >= 8 && w.data[1] == 0x91 {
			out = append(out, [2]byte{w.data[0], w.data[3]})
		}
	}
	return out
}

func (f *fakeDevice) lastWrite(uuid string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.writes) - 1; i >= 0; i-- {
		if f.writes[i].uuid == uuid {
			return f.writes[i].data
		}
	}
	return nil
}

func initialize(t *testing.T, pid uint16) (*Controller, *fakeDevice) {
	t.Helper()
	dev := newFakeDevice(pid)
	c := New(dev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx, Options{AdvertisedPID: pid}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c, dev
}

func TestInitializeJoyConRight(t *testing.T) {
	c, dev := initialize(t, protocol.JoyCon2RightPID)
	if c.Info.ProductID != protocol.JoyCon2RightPID || c.Info.SerialNumber != "SERIAL00000001" {
		t.Fatalf("info %+v", c.Info)
	}
	if !c.IsJoyConRight() || c.Name() != "Joy-Con 2 (Right)" {
		t.Fatal("model detection")
	}
	// Right Joy-Con uses stick 1 (factory, since user data is erased) as its right stick.
	if c.LeftCal != nil || c.RightCal == nil || !c.RightCal.Valid || c.RightCal.CenterX != 2000 {
		t.Fatalf("calibration L=%v R=%+v", c.LeftCal, c.RightCal)
	}
	cmds := dev.commands()
	if len(cmds) < len(protocol.SW2InitSequence) {
		t.Fatalf("only %d commands sent", len(cmds))
	}
	for i, ic := range protocol.SW2InitSequence {
		if cmds[i] != [2]byte{ic.Cmd, ic.Sub} {
			t.Fatalf("command %d = %x, want %02x:%02x", i, cmds[i], ic.Cmd, ic.Sub)
		}
	}
	// Joy-Cons are switched to input format 0x30.
	found := false
	dev.mu.Lock()
	for _, w := range dev.writes {
		if bytes.Equal(w.data, protocol.SetInputMode30) {
			found = true
		}
	}
	dev.mu.Unlock()
	if !found {
		t.Fatal("input mode 0x30 not set")
	}
}

func TestInitializeProUsesBothSticks(t *testing.T) {
	c, dev := initialize(t, protocol.ProController2PID)
	if c.LeftCal == nil || c.RightCal == nil || !c.LeftCal.Valid || !c.RightCal.Valid {
		t.Fatalf("L=%+v R=%+v", c.LeftCal, c.RightCal)
	}
	for _, cmd := range dev.commands() {
		if cmd == [2]byte{0x01, 0x01} {
			t.Fatal("01:01 must be skipped for Pro Controller 2")
		}
	}
}

func TestInitializeToleratesSomeFailures(t *testing.T) {
	dev := newFakeDevice(protocol.JoyCon2LeftPID)
	dev.failCmds[[2]byte{0x16, 0x01}] = true
	dev.failCmds[[2]byte{0x10, 0x01}] = true
	c := New(dev, nil)
	if err := c.Initialize(context.Background(), Options{AdvertisedPID: protocol.JoyCon2LeftPID}); err != nil {
		t.Fatalf("isolated failures should be tolerated: %v", err)
	}

	dev = newFakeDevice(protocol.JoyCon2LeftPID)
	for _, ic := range protocol.SW2InitSequence[:3] {
		dev.failCmds[[2]byte{ic.Cmd, ic.Sub}] = true
	}
	c = New(dev, nil)
	if err := c.Initialize(context.Background(), Options{}); err == nil {
		t.Fatal("three consecutive failures should abort")
	}
}

func TestCommandTimeout(t *testing.T) {
	old := CommandTimeout
	CommandTimeout = 50 * time.Millisecond
	defer func() { CommandTimeout = old }()
	dev := newFakeDevice(protocol.JoyCon2LeftPID)
	c := New(silentTransport{dev}, nil)
	if _, err := c.Command(context.Background(), 0x09, 0x07, nil); err == nil {
		t.Fatal("expected timeout")
	}
}

type silentTransport struct{ *fakeDevice }

func (silentTransport) Write(string, []byte, bool) error { return nil }

func TestInputSettleGateAndDelivery(t *testing.T) {
	c, dev := initialize(t, protocol.JoyCon2RightPID)
	var got []protocol.Report
	var mu sync.Mutex
	c.OnInput(func(r protocol.Report) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})
	report := func(buttons uint32) []byte {
		b := make([]byte, 64)
		binary.LittleEndian.PutUint32(b[4:], buttons)
		return b
	}
	dev.notify(protocol.InputReportUUID, report(protocol.BtnA)) // held at connect: dropped
	dev.notify(protocol.InputReportUUID, report(0))             // neutral: settles
	dev.notify(protocol.InputReportUUID, report(protocol.BtnB))
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Buttons != 0 || got[1].Buttons != protocol.BtnB {
		t.Fatalf("got %+v", got)
	}
}

func TestLEDsPairAndRumble(t *testing.T) {
	c, dev := initialize(t, protocol.JoyCon2LeftPID)
	ctx := context.Background()
	if err := c.SetPlayerLEDs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if w := dev.lastWrite(protocol.CommandWriteUUID); !bytes.Equal(w, []byte{0x09, 0x91, 0x01, 0x07, 0x00, 0x04, 0x00, 0x00, 0x03, 0, 0, 0}) {
		t.Fatalf("LED command %x", w)
	}

	before := len(dev.commands())
	if err := c.Pair(ctx, 0xAABBCCDDEEFF); err != nil {
		t.Fatal(err)
	}
	cmds := dev.commands()[before:]
	want := [][2]byte{{0x15, 0x01}, {0x15, 0x04}, {0x15, 0x02}, {0x15, 0x03}}
	if len(cmds) != 4 {
		t.Fatalf("pair commands %x", cmds)
	}
	for i := range want {
		if cmds[i] != want[i] {
			t.Fatalf("pair step %d = %x want %x", i, cmds[i], want[i])
		}
	}

	if err := c.Rumble(protocol.Vibration{LFAmp: 900, HFAmp: 600, LFFreq: 0xe1, HFFreq: 0x1e1}); err != nil {
		t.Fatal(err)
	}
	pkt := dev.lastWrite(protocol.VibrationWriteJoyConLUUID)
	if len(pkt) != 17 || pkt[1] != 0x50 {
		t.Fatalf("rumble packet %x", pkt)
	}
	// Joy-Con amplitude limiting applies before encoding.
	limited := protocol.Vibration{LFAmp: 614, HFAmp: 409, LFFreq: 0xe1, HFFreq: 0x1e1}.Bytes()
	if !bytes.Equal(pkt[2:7], limited[:]) {
		t.Fatalf("frame %x want %x", pkt[2:7], limited)
	}
	_ = c.Rumble(protocol.SilentVibration())
	if pkt := dev.lastWrite(protocol.VibrationWriteJoyConLUUID); pkt[1] != 0x51 {
		t.Fatalf("sequence should increment: %x", pkt[1])
	}
}

func TestGameCubeRumbleUsesCommandChannel(t *testing.T) {
	c, dev := initialize(t, protocol.NSOGameCubeControllerPID)
	if err := c.Rumble(protocol.Vibration{LFAmp: 100}); err != nil {
		t.Fatal(err)
	}
	if w := dev.lastWrite(protocol.CommandWriteUUID); !bytes.Equal(w, protocol.GameCubeRumblePayload(true)) {
		t.Fatalf("got %x", w)
	}
}

func TestGameCubeFallsBackToFixedCalibration(t *testing.T) {
	dev := newFakeDevice(protocol.NSOGameCubeControllerPID)
	delete(dev.memory, protocol.AddrCalibrationJoystick1)
	delete(dev.memory, protocol.AddrUserCalibrationJoystick2)
	c := New(dev, nil)
	if err := c.Initialize(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	fixed := protocol.FixedStickCalibration()
	if *c.LeftCal != fixed || *c.RightCal != fixed {
		t.Fatalf("L=%+v R=%+v", c.LeftCal, c.RightCal)
	}
}

func TestClose(t *testing.T) {
	c, dev := initialize(t, protocol.JoyCon2LeftPID)
	_ = c.Close()
	if !dev.closed {
		t.Fatal("transport not disconnected")
	}
}
