package controller

import (
	"bytes"
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/angelispatrick/switch2go/internal/controller/controllertest"
	"github.com/angelispatrick/switch2go/internal/protocol"
)

func initialize(t *testing.T, pid uint16, opt Options) (*Controller, *controllertest.Sim) {
	t.Helper()
	sim := controllertest.New("AA:BB:CC:DD:EE:FF", pid)
	c := New(sim, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opt.AdvertisedPID = pid
	if err := c.Initialize(ctx, opt); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c, sim
}

// collect registers an input handler and returns a getter for what arrived.
func collect(c *Controller) func() []protocol.Input {
	var mu sync.Mutex
	var got []protocol.Input
	c.OnInput(func(in protocol.Input) {
		mu.Lock()
		got = append(got, in)
		mu.Unlock()
	})
	return func() []protocol.Input {
		mu.Lock()
		defer mu.Unlock()
		return append([]protocol.Input(nil), got...)
	}
}

func TestInitializeJoyConRight(t *testing.T) {
	c, sim := initialize(t, protocol.JoyCon2RightPID, Options{})
	if c.Kind() != protocol.KindJoyConRight || c.Info.SerialNumber != "SERIAL00000001" || c.Name() != "Joy-Con 2 (Right)" {
		t.Fatalf("kind %v info %+v", c.Kind(), c.Info)
	}
	// Right Joy-Con uses stick 1 (factory, since user data is erased) as its right stick.
	if c.leftCal != nil || c.rightCal == nil || !c.rightCal.Valid || c.rightCal.CenterX != 2000 {
		t.Fatalf("calibration L=%v R=%+v", c.leftCal, c.rightCal)
	}
	cmds := sim.Commands()
	if len(cmds) < len(protocol.SW2InitSequence) {
		t.Fatalf("only %d commands sent", len(cmds))
	}
	for i, ic := range protocol.SW2InitSequence {
		if cmds[i] != [2]byte{ic.Cmd, ic.Sub} {
			t.Fatalf("command %d = %x, want %02x:%02x", i, cmds[i], ic.Cmd, ic.Sub)
		}
	}
	found := false
	for _, w := range sim.Writes() {
		if bytes.Equal(w.Data, protocol.SetInputMode30) {
			found = true
		}
	}
	if !found {
		t.Fatal("Joy-Cons must be switched to input format 0x30")
	}
}

func TestInitializeProSkips0101(t *testing.T) {
	c, sim := initialize(t, protocol.ProController2PID, Options{})
	if c.leftCal == nil || c.rightCal == nil {
		t.Fatal("Pro Controller should calibrate both sticks")
	}
	for _, cmd := range sim.Commands() {
		if cmd == [2]byte{0x01, 0x01} {
			t.Fatal("01:01 must be skipped for Pro Controller 2")
		}
	}
}

func TestInitializeToleratesSomeFailures(t *testing.T) {
	sim := controllertest.New("A", protocol.JoyCon2LeftPID)
	sim.Fail(0x16, 0x01)
	sim.Fail(0x10, 0x01)
	if err := New(sim, nil).Initialize(context.Background(), Options{AdvertisedPID: protocol.JoyCon2LeftPID}); err != nil {
		t.Fatalf("isolated failures should be tolerated: %v", err)
	}

	sim = controllertest.New("A", protocol.JoyCon2LeftPID)
	for _, ic := range protocol.SW2InitSequence[:3] {
		sim.Fail(ic.Cmd, ic.Sub)
	}
	if err := New(sim, nil).Initialize(context.Background(), Options{}); err == nil {
		t.Fatal("three consecutive failures should abort")
	}
}

func TestCommandTimeout(t *testing.T) {
	old := CommandTimeout
	CommandTimeout = 50 * time.Millisecond
	defer func() { CommandTimeout = old }()
	sim := controllertest.New("A", protocol.JoyCon2LeftPID)
	sim.Silent = true
	if _, err := New(sim, nil).command(context.Background(), 0x09, 0x07, nil); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestCalibratedInput(t *testing.T) {
	c, sim := initialize(t, protocol.ProController2PID, Options{
		Deadzone: func(k protocol.Kind) float64 { return 0.10 },
	})
	got := collect(c)
	sim.SendReport(controllertest.Report(0, controllertest.Center, controllertest.Center))
	// Left fully right; right stick 5% up (inside the 10% deadzone).
	sim.SendReport(controllertest.Report(protocol.BtnA, [2]int{3500, 2000}, [2]int{2000, 2075}))
	in := got()
	if len(in) != 2 {
		t.Fatalf("got %d inputs", len(in))
	}
	if in[1].Buttons != protocol.BtnA || in[1].Left != (protocol.Stick{X: 1, Y: 0}) || in[1].Right != (protocol.Stick{}) {
		t.Fatalf("input %+v", in[1])
	}
	if in[1].BatteryVoltage != 3.9 || in[1].AnalogTriggers {
		t.Fatalf("input %+v", in[1])
	}
}

func TestJoyConGain(t *testing.T) {
	c, sim := initialize(t, protocol.JoyCon2RightPID, Options{})
	got := collect(c)
	// Half deflection on a Joy-Con reads 5% hotter than on a Pro Controller.
	sim.SendReport(controllertest.Report(0, controllertest.Center, [2]int{2750, 2000}))
	if x := got()[0].Right.X; math.Abs(x-0.525) > 1e-9 {
		t.Fatalf("right X %v", x)
	}
}

func TestSettleGate(t *testing.T) {
	c, sim := initialize(t, protocol.JoyCon2RightPID, Options{})
	got := collect(c)
	sim.SendReport(controllertest.Report(protocol.BtnA, controllertest.Center, controllertest.Center)) // held at connect: dropped
	sim.SendReport(controllertest.Report(0, controllertest.Center, controllertest.Center))             // neutral: settles
	sim.SendReport(controllertest.Report(protocol.BtnB, controllertest.Center, controllertest.Center))
	in := got()
	if len(in) != 2 || in[0].Buttons != 0 || in[1].Buttons != protocol.BtnB {
		t.Fatalf("got %+v", in)
	}
}

func TestLEDsPairAndRumble(t *testing.T) {
	c, sim := initialize(t, protocol.JoyCon2LeftPID, Options{})
	ctx := context.Background()
	if err := c.SetPlayerLEDs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if w := sim.LastWrite(protocol.CommandWriteUUID); !bytes.Equal(w, []byte{0x09, 0x91, 0x01, 0x07, 0x00, 0x04, 0x00, 0x00, 0x03, 0, 0, 0}) {
		t.Fatalf("LED command %x", w)
	}

	before := len(sim.Commands())
	if err := c.Pair(ctx, 0xAABBCCDDEEFF); err != nil {
		t.Fatal(err)
	}
	cmds := sim.Commands()[before:]
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
	pkt := sim.LastWrite(protocol.VibrationWriteJoyConLUUID)
	if len(pkt) != 17 || pkt[1] != 0x50 {
		t.Fatalf("rumble packet %x", pkt)
	}
	// Joy-Con amplitude limiting applies before encoding.
	limited := protocol.Vibration{LFAmp: 614, HFAmp: 409, LFFreq: 0xe1, HFFreq: 0x1e1}.Bytes()
	if !bytes.Equal(pkt[2:7], limited[:]) {
		t.Fatalf("frame %x want %x", pkt[2:7], limited)
	}
	_ = c.Rumble(protocol.SilentVibration())
	if pkt := sim.LastWrite(protocol.VibrationWriteJoyConLUUID); pkt[1] != 0x51 {
		t.Fatalf("sequence should increment: %x", pkt[1])
	}
}

func TestGameCube(t *testing.T) {
	c, sim := initialize(t, protocol.NSOGameCubeControllerPID, Options{})
	if err := c.Rumble(protocol.Vibration{LFAmp: 100}); err != nil {
		t.Fatal(err)
	}
	if w := sim.LastWrite(protocol.CommandWriteUUID); !bytes.Equal(w, protocol.GameCubeRumblePayload(true)) {
		t.Fatalf("rumble should use the command channel: %x", w)
	}
	got := collect(c)
	sim.SendReport(make([]byte, 64)) // neutral frame passes the settle gate
	raw := make([]byte, 64)
	raw[12], raw[13] = 36, 190 // triggers: released, at the bump
	sim.SendReport(raw)
	in := got()[1:]
	if len(in) != 1 || !in[0].AnalogTriggers || in[0].LeftTrigger != 0 || in[0].RightTrigger != 255 {
		t.Fatalf("input %+v", in)
	}
}

func TestGameCubeFallsBackToFixedCalibration(t *testing.T) {
	sim := controllertest.New("A", protocol.NSOGameCubeControllerPID)
	delete(sim.Memory, protocol.AddrCalibrationJoystick1)
	delete(sim.Memory, protocol.AddrUserCalibrationJoystick2)
	c := New(sim, nil)
	if err := c.Initialize(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	fixed := protocol.FixedStickCalibration()
	if *c.leftCal != fixed || *c.rightCal != fixed {
		t.Fatalf("L=%+v R=%+v", c.leftCal, c.rightCal)
	}
}

func TestClose(t *testing.T) {
	c, sim := initialize(t, protocol.JoyCon2LeftPID, Options{})
	_ = c.Close()
	if !sim.Closed() {
		t.Fatal("transport not disconnected")
	}
}
