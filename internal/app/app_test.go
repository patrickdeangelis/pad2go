package app

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

type fakeDev struct {
	addr string
	kind protocol.Kind

	mu      sync.Mutex
	input   func(protocol.Input)
	leds    []int
	rumbles []protocol.Vibration
	closed  bool
}

type lightingDev struct {
	*fakeDev
	color                   [3]byte
	brightness              byte
	off                     bool
	called                  bool
	leftPreset, rightPreset string
}

func (d *lightingDev) SetTriggerPresets(left, right string) error {
	d.leftPreset, d.rightPreset = left, right
	return nil
}

func (d *lightingDev) SetLighting(color [3]byte, brightness byte, off bool) error {
	d.color, d.brightness, d.off, d.called = color, brightness, off, true
	return nil
}

type framePad struct {
	mu     sync.Mutex
	frames []mapping.Frame
}

func (p *framePad) Update(mapping.XboxState) error { return nil }
func (p *framePad) UpdateFrame(f mapping.Frame) error {
	p.mu.Lock()
	p.frames = append(p.frames, f)
	p.mu.Unlock()
	return nil
}
func (p *framePad) Close() error { return nil }

type frameBackend struct{ pad *framePad }

func (b *frameBackend) Name() string { return "frame" }
func (b *frameBackend) NewPad(virtualpad.RumbleFunc) (virtualpad.Pad, error) {
	if b.pad == nil {
		b.pad = &framePad{}
	}
	return b.pad, nil
}
func (b *frameBackend) Close() error { return nil }

func TestDualSenseFramePadReceivesFullFrame(t *testing.T) {
	backend := &frameBackend{}
	a := New(config.Default(), backend, nil, nil)
	d := newDev("ds", 0)
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	touch := [2]protocol.TouchContact{{Active: true, ID: 1, X: 100, Y: 200}}
	d.send(protocol.Input{
		Buttons: protocol.BtnB, Left: protocol.Stick{X: 0.25},
		LeftTrigger: 123, AnalogTriggers: true, Touch: touch,
	})
	backend.pad.mu.Lock()
	defer backend.pad.mu.Unlock()
	if len(backend.pad.frames) == 0 {
		t.Fatal("FramePad did not receive an update")
	}
	f := backend.pad.frames[len(backend.pad.frames)-1]
	if f.Player.Buttons != protocol.BtnB || f.Player.LeftTrigger != 123 ||
		!f.Player.Touch[0].Active || f.Player.Touch[0].X != 100 {
		t.Fatalf("frame %+v", f.Player)
	}
}

func TestDualSenseLightingAppliedOnAttach(t *testing.T) {
	a, _ := newApp(t, func(c *config.Config) {
		c.DualSenseLighting = true
		c.DualSenseColor = "#7f80ff"
		c.DualSenseBrightness = 25
		c.DualSensePlayerLED = false
		c.DualSenseTriggerLeft = "resistance"
		c.DualSenseTriggerRight = "barrier"
	})
	d := &lightingDev{fakeDev: newDev("ds", 0)}
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !d.called || d.color != [3]byte{127, 128, 255} || d.brightness != 25 || !d.off {
		t.Fatalf("lighting %+v", d)
	}
	if d.leftPreset != "resistance" || d.rightPreset != "barrier" {
		t.Fatalf("trigger presets = %q %q", d.leftPreset, d.rightPreset)
	}
}

func newDev(addr string, pid uint16) *fakeDev {
	return &fakeDev{addr: addr, kind: protocol.KindOf(pid)}
}

func (d *fakeDev) Address() string     { return d.addr }
func (d *fakeDev) Kind() protocol.Kind { return d.kind }
func (d *fakeDev) Name() string        { return d.kind.String() }
func (d *fakeDev) OnInput(fn func(protocol.Input)) {
	d.mu.Lock()
	d.input = fn
	d.mu.Unlock()
}
func (d *fakeDev) SetPlayerLEDs(_ context.Context, p int) error {
	d.mu.Lock()
	d.leds = append(d.leds, p)
	d.mu.Unlock()
	return nil
}
func (d *fakeDev) Rumble(v protocol.Vibration) error {
	d.mu.Lock()
	d.rumbles = append(d.rumbles, v)
	d.mu.Unlock()
	return nil
}
func (d *fakeDev) Close() error { d.closed = true; return nil }

func (d *fakeDev) send(in protocol.Input) {
	d.mu.Lock()
	fn := d.input
	d.mu.Unlock()
	if fn != nil {
		fn(in)
	}
}

func centered(buttons uint32) protocol.Input { return protocol.Input{Buttons: buttons} }

func newApp(t *testing.T, mut func(*config.Config)) (*App, *virtualpad.Recorder) {
	t.Helper()
	cfg := config.Default()
	if mut != nil {
		mut(cfg)
	}
	rec := &virtualpad.Recorder{}
	return New(cfg, rec, nil, nil), rec
}

func TestSingleProController(t *testing.T) {
	a, rec := newApp(t, nil)
	d := newDev("P1", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	r := centered(protocol.BtnB | protocol.BtnZR)
	r.Left = protocol.Stick{X: 1}
	d.send(r)
	pads := rec.Snapshot()
	if len(pads) != 1 {
		t.Fatalf("%d pads", len(pads))
	}
	st, _ := pads[0].State()
	if st.Buttons != mapping.XBA || st.RightTrigger != 255 || st.LX != 32767 || st.LY != 0 {
		t.Fatalf("state %+v", st)
	}
	if len(d.leds) != 1 || d.leds[0] != 1 {
		t.Fatalf("leds %v", d.leds)
	}
}

func TestJoyConsMergeIntoOnePad(t *testing.T) {
	a, rec := newApp(t, nil)
	l, r := newDev("L", protocol.JoyCon2LeftPID), newDev("R", protocol.JoyCon2RightPID)
	ctx := context.Background()
	if err := a.AddDevice(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := a.AddDevice(ctx, r); err != nil {
		t.Fatal(err)
	}
	pads := rec.Snapshot()
	if len(pads) != 1 {
		t.Fatalf("expected one merged pad, got %d", len(pads))
	}
	lr := centered(protocol.BtnUp | protocol.BtnL)
	lr.Left = protocol.Stick{Y: 1}
	l.send(lr)
	r.send(centered(protocol.BtnA))
	st, _ := pads[0].State()
	if st.Buttons != mapping.XBUp|mapping.XBLB|mapping.XBB || st.LY != 32767 {
		t.Fatalf("merged state %+v", st)
	}
	// Both Joy-Cons show player 1.
	if len(l.leds) == 0 || len(r.leds) == 0 || r.leds[len(r.leds)-1] != 1 {
		t.Fatalf("leds L=%v R=%v", l.leds, r.leds)
	}

	// Removing one side leaves a working single Joy-Con pad.
	a.RemoveDevice("L")
	r.send(centered(protocol.BtnA))
	st, closed := pads[0].State()
	if closed || st.Buttons != mapping.XBB {
		t.Fatalf("after removal %+v closed=%v", st, closed)
	}
	a.RemoveDevice("R")
	if _, closed := pads[0].State(); !closed {
		t.Fatal("empty slot should close its pad")
	}
}

func TestNoCombineAndSlotLimit(t *testing.T) {
	a, rec := newApp(t, func(c *config.Config) { c.CombineJoyCons = false; c.MaxControllers = 2 })
	ctx := context.Background()
	_ = a.AddDevice(ctx, newDev("L", protocol.JoyCon2LeftPID))
	_ = a.AddDevice(ctx, newDev("R", protocol.JoyCon2RightPID))
	if len(rec.Snapshot()) != 2 {
		t.Fatal("combine disabled should give two pads")
	}
	if !a.Full() {
		t.Fatal("should be full")
	}
	if err := a.AddDevice(ctx, newDev("P", protocol.ProController2PID)); !errors.Is(err, ErrFull) {
		t.Fatalf("err %v", err)
	}
	if !a.Connected("L") || a.Connected("P") {
		t.Fatal("Connected")
	}
}

func TestSingleJoyConHorizontal(t *testing.T) {
	a, rec := newApp(t, func(c *config.Config) { c.JoyConHoldMode["R"] = config.HoldHorizontal })
	r := newDev("R", protocol.JoyCon2RightPID)
	_ = a.AddDevice(context.Background(), r)
	rep := centered(protocol.BtnX)
	rep.Right = protocol.Stick{Y: 1} // push "up" on the Joy-Con
	r.send(rep)
	st, _ := rec.Snapshot()[0].State()
	// Sideways, X sits on the right (Xbox B position) and "up" points right.
	if st.Buttons != mapping.XBB || st.LX != 32767 || st.RX != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestRemapHomeToA(t *testing.T) {
	a, rec := newApp(t, func(c *config.Config) { c.HomeMapping = "A" })
	d := newDev("P", protocol.ProController2PID)
	_ = a.AddDevice(context.Background(), d)
	d.send(centered(protocol.BtnHome))
	if st, _ := rec.Snapshot()[0].State(); st.Buttons != mapping.XBB {
		t.Fatalf("%+v", st)
	}
}

func TestRumbleRouting(t *testing.T) {
	a, rec := newApp(t, nil)
	d := newDev("P", protocol.ProController2PID)
	_ = a.AddDevice(context.Background(), d)
	rec.Snapshot()[0].Rumble(255, 0)
	time.Sleep(5 * RumbleInterval)
	rec.Snapshot()[0].Rumble(0, 0)
	time.Sleep(3 * RumbleInterval)
	d.mu.Lock()
	rumbles := append([]protocol.Vibration(nil), d.rumbles...)
	d.mu.Unlock()
	if len(rumbles) < 3 {
		t.Fatalf("active rumble should be re-sent, got %d writes", len(rumbles))
	}
	if rumbles[0].LFAmp != 796 {
		t.Fatalf("first frame %+v", rumbles[0])
	}
	if last := rumbles[len(rumbles)-1]; last.LFAmp != 0 || last.HFAmp != 0 {
		t.Fatalf("should end silent: %+v", last)
	}
	n := len(rumbles)
	time.Sleep(3 * RumbleInterval)
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rumbles) != n {
		t.Fatal("idle rumbler should not keep writing")
	}
}

func TestCloseDisconnectsAll(t *testing.T) {
	a, rec := newApp(t, nil)
	d := newDev("P", protocol.ProController2PID)
	_ = a.AddDevice(context.Background(), d)
	a.Close()
	if !d.closed {
		t.Fatal("device not closed")
	}
	if _, closed := rec.Snapshot()[0].State(); !closed {
		t.Fatal("pad not closed")
	}
}

func TestAddressMAC(t *testing.T) {
	if got := addressMAC("AA:BB:CC:DD:EE:FF"); got != [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF} {
		t.Fatalf("%x", got)
	}
	u := addressMAC("6F3C1C8E-1111-2222-3333-444455556666")
	if u != addressMAC("6F3C1C8E-1111-2222-3333-444455556666") || u[0]&0x02 == 0 {
		t.Fatalf("uuid-derived MAC %x", u)
	}
}

func TestDSUPublishesOrientedMotion(t *testing.T) {
	srv, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	cfg := config.Default()
	cfg.JoyConHoldMode["R"] = config.HoldHorizontal
	a := New(cfg, &virtualpad.Recorder{}, srv, nil)
	r := newDev("R", protocol.JoyCon2RightPID)
	_ = a.AddDevice(context.Background(), r)

	subscribeAll(cli)

	buf := make([]byte, 256)
	for range 50 {
		r.send(protocol.Input{Buttons: protocol.BtnX, Gyro: [3]int16{100, 0, 0}})
		cli.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		n, err := cli.Read(buf)
		if err != nil {
			continue
		}
		data := buf[16:n]
		if data[6] != dsu.ModelJoyCon {
			t.Fatalf("model %d", data[6])
		}
		// X on a sideways right Joy-Con is the right-position button (Switch A → DSU circle).
		if data[21] != 0x20 {
			t.Fatalf("buttons %02x", data[21])
		}
		pitch := math.Float32frombits(binary.LittleEndian.Uint32(data[72:]))
		if pitch != 0 {
			t.Fatalf("gyro should be rotated for a sideways Joy-Con, pitch %v", pitch)
		}
		return
	}
	t.Fatal("no DSU packet received")
}

// subscribeAll sends a DSUC pad-data request for all pads (flags 0).
func subscribeAll(cli *net.UDPConn) {
	payload := binary.LittleEndian.AppendUint32(nil, 0x100002)
	payload = append(payload, make([]byte, 8)...)
	req := append([]byte("DSUC\xe9\x03"), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint16(req[6:], uint16(len(payload)))
	req = append(req, payload...)
	binary.LittleEndian.PutUint32(req[8:], crc32.ChecksumIEEE(req))
	cli.Write(req)
}

func TestDSUPublishesJoyConPairAsOnePad(t *testing.T) {
	srv, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	a := New(config.Default(), &virtualpad.Recorder{}, srv, nil)
	l, r := newDev("AA:00:00:00:00:01", protocol.JoyCon2LeftPID), newDev("AA:00:00:00:00:02", protocol.JoyCon2RightPID)
	_ = a.AddDevice(context.Background(), l)
	_ = a.AddDevice(context.Background(), r)
	subscribeAll(cli)

	buf := make([]byte, 256)
	for range 100 {
		l.send(protocol.Input{Buttons: protocol.BtnUp, Left: protocol.Stick{X: 1}})
		r.send(protocol.Input{Buttons: protocol.BtnPlus | protocol.BtnHome, Right: protocol.Stick{X: -1}, Gyro: [3]int16{100, 0, 0}})
		cli.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		n, err := cli.Read(buf)
		if err != nil {
			continue
		}
		data := buf[16:n]
		if data[4] != 0 || data[6] != dsu.ModelDS4 || data[8] != 0xAA || data[13] != 0x01 {
			t.Fatalf("the pair should be DSU pad 0, a full pad named after the left Joy-Con: % x", data[4:14])
		}
		if data[20]&0x10 == 0 || data[22] != 1 {
			continue // the right Joy-Con hasn't reported since the left one did
		}
		if data[20] != 0x10|0x08 || data[24] != 255 || data[26] != 0 {
			t.Fatalf("merged state: buttons %02x home %d sticks LX %d RX %d", data[20], data[22], data[24], data[26])
		}
		if pitch := math.Float32frombits(binary.LittleEndian.Uint32(data[72:])); pitch == 0 {
			t.Fatal("the pair should carry the right Joy-Con's motion")
		}
		return
	}
	t.Fatal("no merged DSU packet received")
}

func TestPlayersWatchRumbleDisconnect(t *testing.T) {
	a, _ := newApp(t, nil)
	ctx := context.Background()
	l, r, p := newDev("L", protocol.JoyCon2LeftPID), newDev("R", protocol.JoyCon2RightPID), newDev("P", protocol.ProController2PID)
	for _, d := range []*fakeDev{l, r, p} {
		if err := a.AddDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	changes := 0
	a.OnChange(func() { changes++ })

	l.send(protocol.Input{BatteryVoltage: 3.41})
	players := a.Players()
	if len(players) != 2 || players[0].Number != 1 || len(players[0].Members) != 2 || players[1].Number != 2 {
		t.Fatalf("players %+v", players)
	}
	if m := players[0].Members; m[0].Volts != 3.41 || m[0].Battery() != protocol.BatteryHigh || m[1].Battery() != protocol.BatteryUnknown {
		t.Fatalf("batteries %+v", players[0].Members)
	}

	samples, cancel, err := a.Watch(2)
	if err != nil {
		t.Fatal(err)
	}
	p.send(protocol.Input{Buttons: protocol.BtnB, Gyro: [3]int16{0, 0, -1000}})
	select {
	case s := <-samples:
		if s.Xbox.Buttons != mapping.XBA || s.Gyro[1] == 0 {
			t.Fatalf("sample %+v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("no sample")
	}
	cancel()
	if _, _, err := a.Watch(3); !errors.Is(err, ErrNoPlayer) {
		t.Fatalf("watch empty slot: %v", err)
	}

	if err := a.TestRumble(2, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * RumbleInterval)
	p.mu.Lock()
	n := len(p.rumbles)
	last := p.rumbles[n-1]
	p.mu.Unlock()
	if n < 2 || last.LFAmp != 0 {
		t.Fatalf("test rumble should buzz then stop: %d writes, last %+v", n, last)
	}

	if err := a.Disconnect(1); err != nil {
		t.Fatal(err)
	}
	if !l.closed || !r.closed || a.Connected("L") {
		t.Fatal("pair not disconnected")
	}
	if got := a.Players(); len(got) != 1 || got[0].Number != 2 {
		t.Fatalf("player 2 must keep its number: %+v", got)
	}
	if changes < 2 {
		t.Fatalf("OnChange called %d times", changes)
	}
}
func dual(t *testing.T, a *App, addr string) *fakeDev {
	t.Helper()
	d := newDev(addr, 0)
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}
func playerOf(a *App, addr string) int {
	for _, p := range a.Players() {
		for _, m := range p.Members {
			if m.Addr == addr {
				return p.Number
			}
		}
	}
	return 0
}
func TestDualSenseAttachesLowestFreeSlot(t *testing.T) {
	a, _ := newApp(t, nil)
	dual(t, a, "d1")
	if got := playerOf(a, "d1"); got != 1 {
		t.Fatalf("slot=%d", got)
	}
	a, _ = newApp(t, nil)
	for _, n := range []string{"p1", "p2"} {
		if err := a.AddDevice(context.Background(), newDev(n, protocol.ProController2PID)); err != nil {
			t.Fatal(err)
		}
	}
	dual(t, a, "d3")
	if got := playerOf(a, "d3"); got != 3 {
		t.Fatalf("slot=%d", got)
	}
}
func TestDualSenseDetachFreesSlot(t *testing.T) {
	a, _ := newApp(t, nil)
	d := dual(t, a, "d")
	if err := a.Disconnect(1); err != nil {
		t.Fatal(err)
	}
	if a.Connected("d") || !d.closed {
		t.Fatal("dual not detached")
	}
}
func TestDualSenseRumbleTest(t *testing.T) {
	a, _ := newApp(t, nil)
	d := dual(t, a, "d")
	if err := a.TestRumble(1, 15*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rumbles) < 2 || d.rumbles[0].LFAmp == 0 || d.rumbles[0].HFAmp == 0 || d.rumbles[len(d.rumbles)-1].LFAmp != 0 {
		t.Fatalf("rumbles=%+v", d.rumbles)
	}
}
func TestDualSenseCalibratedMotionToDSU(t *testing.T) {
	srv, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	cli, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	a := New(config.Default(), &virtualpad.Recorder{}, srv, nil)
	d := newDev("d", 0)
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	subscribeAll(cli)
	buf := make([]byte, 256)
	cli.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	for {
		if _, err := cli.Read(buf); err != nil {
			break
		}
	}
	// A missing calibration must not suppress buttons, sticks, or touch.
	d.send(protocol.Input{BatteryBand: protocol.BatteryHigh, Buttons: protocol.BtnB, Gyro: [3]int16{300, 200, 100}})
	cli.SetReadDeadline(time.Now().Add(time.Second))
	n, err := cli.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 || buf[16+21]&0x40 == 0 {
		t.Fatalf("uncalibrated DSU buttons: %x", buf[:n])
	}
	for _, offset := range []int{60, 64, 68, 72, 76, 80} {
		if math.Float32frombits(binary.LittleEndian.Uint32(buf[16+offset:])) != 0 {
			t.Fatal("uncalibrated DSU motion must be zero")
		}
	}
	d.send(protocol.Input{DualSenseMotionCalibrated: true, DualSenseAccelG: [3]float32{1, 2, 3}, DualSenseGyroDPS: [3]float32{10, 20, 30}, AnalogTriggers: true, LeftTrigger: 33, RightTrigger: 99, Touch: [2]protocol.TouchContact{{Active: true, ID: 5, X: 100, Y: 200}}})
	cli.SetReadDeadline(time.Now().Add(time.Second))
	n, err = cli.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 || buf[22] != dsu.ModelDS4 {
		t.Fatalf("DSU packet size=%d model=%d", n, buf[22])
	}
	if buf[16+38] != 99 || buf[16+39] != 33 || buf[16+40] != 1 || buf[16+41] != 5 || binary.LittleEndian.Uint16(buf[16+42:]) != 100 {
		t.Fatalf("DSU trigger/touch data %x", buf[54:68])
	}
	f := func(offset int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(buf[16+offset:])) }
	accel := [3]float32{f(60), f(64), f(68)}
	gyro := [3]float32{f(72), f(76), f(80)}
	if accel != [3]float32{-1, -2, -3} || gyro != [3]float32{10, -20, -30} {
		t.Fatalf("accel=%v gyro=%v", accel, gyro)
	}
}
func TestDualSenseSlotsFullRetries(t *testing.T) {
	a, _ := newApp(t, func(c *config.Config) { c.MaxControllers = 1 })
	if err := a.AddDevice(context.Background(), newDev("p", protocol.ProController2PID)); err != nil {
		t.Fatal(err)
	}
	d := newDev("d", 0)
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v", err)
	}
	_ = a.Disconnect(1)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}
func TestDualSenseReplugKeepsSlot(t *testing.T) {
	a, _ := newApp(t, nil)
	if err := a.AddDevice(context.Background(), newDev("p1", protocol.ProController2PID)); err != nil {
		t.Fatal(err)
	}
	dual(t, a, "d")
	if playerOf(a, "d") != 2 {
		t.Fatal("not in slot 2")
	}
	_ = a.Disconnect(2)
	d := dual(t, a, "d")
	if playerOf(a, "d") != 2 {
		t.Fatal("slot not remembered")
	}
	_ = d.Close()
	_ = a.Disconnect(2)
	if err := a.AddDevice(context.Background(), newDev("p2", protocol.ProController2PID)); err != nil {
		t.Fatal(err)
	}
	dual(t, a, "d")
	if playerOf(a, "d") != 3 {
		t.Fatalf("occupied remembered slot should fall back to lowest free, got %d", playerOf(a, "d"))
	}
}
func TestDualSenseNeverMerged(t *testing.T) {
	a, _ := newApp(t, func(c *config.Config) { c.CombineJoyCons = true })
	if err := a.AddDevice(context.Background(), newDev("p", protocol.ProController2PID)); err != nil {
		t.Fatal(err)
	}
	dual(t, a, "d")
	if playerOf(a, "d") == playerOf(a, "p") {
		t.Fatal("merged with Pro")
	}
	a, _ = newApp(t, func(c *config.Config) { c.CombineJoyCons = true })
	if err := a.AddDevice(context.Background(), newDev("left", protocol.JoyCon2LeftPID)); err != nil {
		t.Fatal(err)
	}
	d := dual(t, a, "d2")
	if playerOf(a, "d2") == playerOf(a, "left") {
		t.Fatal("merged with Joy-Con")
	}
	_ = d.Close()
}
