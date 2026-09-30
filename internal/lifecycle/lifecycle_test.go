package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/angelispatrick/pad2go/internal/app"
	"github.com/angelispatrick/pad2go/internal/config"
	"github.com/angelispatrick/pad2go/internal/controller"
	"github.com/angelispatrick/pad2go/internal/controller/controllertest"
	"github.com/angelispatrick/pad2go/internal/protocol"
	"github.com/angelispatrick/pad2go/internal/virtualpad"
)

const hostMAC = 0xAABBCCDDEEFF

// memRadio is an in-memory Radio: controllers advertise until connected,
// and every Connect hands out a fresh simulated controller.
type memRadio struct {
	host    uint64
	hostErr error

	mu       sync.Mutex
	adverts  []Advert
	sims     map[string][]*controllertest.Sim
	drops    map[string]func()
	scans    int
	onSim    func(addr string, n int, sim *controllertest.Sim) // n = connection number
	dropping map[string]int                                    // drop the link during the nth connection
}

func newRadio() *memRadio {
	return &memRadio{host: hostMAC, sims: map[string][]*controllertest.Sim{}, drops: map[string]func(){}, dropping: map[string]int{}}
}

func (r *memRadio) advertise(addr string, pid uint16, reconnect uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adverts = append(r.adverts, Advert{Addr: addr, Adv: protocol.Advertisement{
		VendorID: protocol.NintendoVendorID, ProductID: pid, ReconnectMAC: reconnect}})
}

func (r *memRadio) HostMAC() (uint64, error) { return r.host, r.hostErr }

func (r *memRadio) Scan(ctx context.Context, accept func(Advert) bool) (Advert, error) {
	for {
		r.mu.Lock()
		r.scans++
		adverts := append([]Advert(nil), r.adverts...)
		r.mu.Unlock()
		for _, a := range adverts {
			if accept(a) {
				return a, nil
			}
		}
		select {
		case <-ctx.Done():
			return Advert{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (r *memRadio) Connect(_ context.Context, a Advert, onDisconnect func()) (controller.Transport, error) {
	sim := controllertest.New(a.Addr, a.Adv.ProductID)
	r.mu.Lock()
	r.sims[a.Addr] = append(r.sims[a.Addr], sim)
	n := len(r.sims[a.Addr])
	r.drops[a.Addr] = onDisconnect
	hook, dropNow := r.onSim, r.dropping[a.Addr] == n
	r.mu.Unlock()
	if hook != nil {
		hook(a.Addr, n, sim)
	}
	if dropNow {
		onDisconnect() // the link drops before initialization finishes
	}
	return sim, nil
}

// drop simulates the controller going away after it was connected.
func (r *memRadio) drop(addr string) {
	r.mu.Lock()
	fn := r.drops[addr]
	r.mu.Unlock()
	fn()
}

func (r *memRadio) simsFor(addr string) []*controllertest.Sim {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*controllertest.Sim(nil), r.sims[addr]...)
}

func (r *memRadio) scanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scans
}

type harness struct {
	radio *memRadio
	app   *app.App
	pads  *virtualpad.Recorder
}

func start(t *testing.T, radio *memRadio, mut func(*config.Config)) harness {
	t.Helper()
	cfg := config.Default()
	cfg.ConnectHaptics = false
	if mut != nil {
		mut(cfg)
	}
	log := slog.New(slog.DiscardHandler)
	pads := &virtualpad.Recorder{}
	a := app.New(cfg, pads, nil, log)
	m := &Manager{Radio: radio, App: a, Config: cfg, Log: log, Backoff: 5 * time.Millisecond, ConnectTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- m.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v", err)
		}
		a.Close()
	})
	return harness{radio, a, pads}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func pairCommand(sim *controllertest.Sim) []byte {
	for _, w := range sim.Writes() {
		if w.UUID == protocol.CommandWriteUUID && len(w.Data) > 8 && w.Data[0] == protocol.CmdPair && w.Data[3] == protocol.SubPairSetMAC {
			return w.Data[8:]
		}
	}
	return nil
}

func TestPairsControllerInSyncMode(t *testing.T) {
	h := start(t, newRadio(), nil)
	h.radio.advertise("L", protocol.JoyCon2LeftPID, 0)
	eventually(t, "attach", func() bool { return h.app.Connected("L") })
	if got := pairCommand(h.radio.simsFor("L")[0]); !bytes.Equal(got, protocol.PairSetMACData(hostMAC)) {
		t.Fatalf("pair set-MAC data %x", got)
	}
	if len(h.pads.Snapshot()) != 1 {
		t.Fatal("expected one player slot")
	}
}

func TestConfiguredHostMACWins(t *testing.T) {
	h := start(t, newRadio(), func(c *config.Config) { c.HostMAC = "11:22:33:44:55:66" })
	h.radio.advertise("L", protocol.JoyCon2LeftPID, 0)
	eventually(t, "attach", func() bool { return h.app.Connected("L") })
	if got := pairCommand(h.radio.simsFor("L")[0]); !bytes.Equal(got, protocol.PairSetMACData(0x112233445566)) {
		t.Fatalf("pair set-MAC data %x", got)
	}
}

func TestReconnectsOwnControllerWithoutPairing(t *testing.T) {
	h := start(t, newRadio(), nil)
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "attach", func() bool { return h.app.Connected("P") })
	if pairCommand(h.radio.simsFor("P")[0]) != nil {
		t.Fatal("an already-paired controller must not be re-paired")
	}
}

func TestForeignControllers(t *testing.T) {
	h := start(t, newRadio(), nil)
	h.radio.advertise("F", protocol.ProController2PID, 0x010203040506)
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "own controller", func() bool { return h.app.Connected("P") })
	if h.app.Connected("F") || len(h.radio.simsFor("F")) != 0 {
		t.Fatal("a controller bonded to another host must be ignored")
	}

	h = start(t, newRadio(), func(c *config.Config) { c.AcceptForeignControllers = true })
	h.radio.advertise("F", protocol.ProController2PID, 0x010203040506)
	eventually(t, "foreign controller accepted", func() bool { return h.app.Connected("F") })
}

func TestUnknownHostConnectsWithoutPairing(t *testing.T) {
	radio := newRadio()
	radio.hostErr = errors.New("no adapter address")
	h := start(t, radio, nil)
	h.radio.advertise("L", protocol.JoyCon2LeftPID, 0)
	h.radio.advertise("F", protocol.ProController2PID, 0x010203040506)
	eventually(t, "both attached", func() bool { return h.app.Connected("L") && h.app.Connected("F") })
	if pairCommand(h.radio.simsFor("L")[0]) != nil {
		t.Fatal("cannot pair without knowing the host MAC")
	}
}

func TestDropBeforeAttachDoesNotLeakSlot(t *testing.T) {
	radio := newRadio()
	radio.dropping["P"] = 1 // first connection drops mid-initialization
	h := start(t, radio, func(c *config.Config) { c.MaxControllers = 1 })
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "second connection attaches", func() bool { return h.app.Connected("P") })
	sims := h.radio.simsFor("P")
	if len(sims) != 2 || !sims[0].Closed() || sims[1].Closed() {
		t.Fatalf("connections %d, first closed %v", len(sims), sims[0].Closed())
	}
	if n := len(h.pads.Snapshot()); n != 1 {
		t.Fatalf("the dropped connection must not create a player slot (%d pads)", n)
	}
}

func TestDropAfterAttachDetachesAndReconnects(t *testing.T) {
	h := start(t, newRadio(), nil)
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "attach", func() bool { return h.app.Connected("P") })
	h.radio.drop("P")
	if _, closed := h.pads.Snapshot()[0].State(); !closed {
		t.Fatal("dropping the only controller should close its player slot")
	}
	eventually(t, "reconnect", func() bool { return h.app.Connected("P") && len(h.radio.simsFor("P")) == 2 })
}

func TestInitFailureClosesLink(t *testing.T) {
	radio := newRadio()
	radio.onSim = func(addr string, n int, sim *controllertest.Sim) {
		if n == 1 {
			for _, ic := range protocol.SW2InitSequence[:3] {
				sim.Fail(ic.Cmd, ic.Sub)
			}
		}
	}
	h := start(t, radio, nil)
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "retry attaches", func() bool { return h.app.Connected("P") })
	if first := h.radio.simsFor("P")[0]; !first.Closed() {
		t.Fatal("a failed initialization must close the link")
	}
}

func TestFullStopsScanning(t *testing.T) {
	h := start(t, newRadio(), func(c *config.Config) { c.MaxControllers = 1 })
	h.radio.advertise("P", protocol.ProController2PID, hostMAC)
	eventually(t, "attach", func() bool { return h.app.Connected("P") })
	before := h.radio.scanCount()
	time.Sleep(50 * time.Millisecond)
	if after := h.radio.scanCount(); after > before+1 {
		t.Fatalf("scanned %d more times while every slot was full", after-before)
	}
}

func TestFormatMAC(t *testing.T) {
	if got := FormatMAC(0xAABBCCDDEEFF); got != "AA:BB:CC:DD:EE:FF" {
		t.Fatal(got)
	}
}
