package app

import (
	"context"
	"encoding/binary"
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
	if err := a.AddDevice(ctx, newDev("P", protocol.ProController2PID)); err != ErrFull {
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

	// Subscribe to all pads (DSUC pad-data request, flags 0).
	payload := binary.LittleEndian.AppendUint32(nil, 0x100002)
	payload = append(payload, make([]byte, 8)...)
	req := append([]byte("DSUC\xe9\x03"), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint16(req[6:], uint16(len(payload)))
	req = append(req, payload...)
	binary.LittleEndian.PutUint32(req[8:], crc32.ChecksumIEEE(req))
	cli.Write(req)

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

	l.send(protocol.Input{BatteryVoltage: 3.75})
	players := a.Players()
	if len(players) != 2 || players[0].Number != 1 || len(players[0].Members) != 2 || players[1].Number != 2 {
		t.Fatalf("players %+v", players)
	}
	if players[0].Members[0].Battery != 50 || players[0].Members[1].Battery != -1 {
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
	if _, _, err := a.Watch(3); err != ErrNoPlayer {
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
