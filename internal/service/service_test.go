package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/angelispatrick/pad2go/internal/config"
	"github.com/angelispatrick/pad2go/internal/controller"
	"github.com/angelispatrick/pad2go/internal/controller/controllertest"
	"github.com/angelispatrick/pad2go/internal/lifecycle"
	"github.com/angelispatrick/pad2go/internal/protocol"
	"github.com/angelispatrick/pad2go/internal/virtualpad"
)

// radio advertises one Pro Controller 2 bonded to this host whenever it
// isn't connected.
type radio struct {
	mu        sync.Mutex
	connected bool
	connects  int
}

func (r *radio) HostMAC() (uint64, error) { return 0xAABBCCDDEEFF, nil }

func (r *radio) Scan(ctx context.Context, accept func(lifecycle.Advert) bool) (lifecycle.Advert, error) {
	a := lifecycle.Advert{Addr: "P", Adv: protocol.Advertisement{VendorID: protocol.NintendoVendorID, ProductID: protocol.ProController2PID, ReconnectMAC: 0xAABBCCDDEEFF}}
	for {
		r.mu.Lock()
		free := !r.connected
		r.mu.Unlock()
		if free && accept(a) {
			return a, nil
		}
		select {
		case <-ctx.Done():
			return lifecycle.Advert{}, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (r *radio) Connect(_ context.Context, a lifecycle.Advert, _ func()) (controller.Transport, error) {
	sim := controllertest.New(a.Addr, a.Adv.ProductID)
	sim.OnClosed(func() {
		r.mu.Lock()
		r.connected = false
		r.mu.Unlock()
	})
	r.mu.Lock()
	r.connected = true
	r.connects++
	r.mu.Unlock()
	return sim, nil
}

func start(t *testing.T, opt Options) *Service {
	t.Helper()
	s := build(t, opt)
	run(t, s)
	return s
}

func build(t *testing.T, opt Options) *Service {
	t.Helper()
	if opt.ConfigPath == "" {
		opt.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	}
	if opt.OpenBackend == nil {
		opt.OpenBackend = func(string) (virtualpad.Backend, error) { return &virtualpad.Recorder{}, nil }
	}
	opt.Log = slog.New(slog.DiscardHandler)
	opt.Backoff, opt.ConnectTimeout = 5*time.Millisecond, 5*time.Second
	s, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// run supervises s until the test ends.
func run(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	c := config.Default()
	c.JoyConHoldMode["X"] = config.HoldHorizontal
	c.HomeMapping = "A"
	next := config.Default()
	SettingsFrom(c).Apply(next)
	if !reflect.DeepEqual(SettingsFrom(c), SettingsFrom(next)) {
		t.Fatalf("round trip changed settings:\n%+v\n%+v", SettingsFrom(c), SettingsFrom(next))
	}
}

func TestConnectToastSaveAndRestart(t *testing.T) {
	r := &radio{}
	s := build(t, Options{Radio: func() (lifecycle.Radio, error) { return r, nil }})
	events, cancel := s.Subscribe() // before Run, so the connect toast can't be missed
	defer cancel()
	run(t, s)

	eventually(t, "player 1", func() bool { return len(s.Snapshot().Players) == 1 })
	snap := s.Snapshot()
	if snap.Players[0].Player != 1 || snap.Players[0].Members[0].Kind != "pro" || snap.Discovery.Step != 4 || !strings.Contains(snap.Discovery.Text, "Jogador 1") {
		t.Fatalf("snapshot %+v", snap)
	}
	var toast *Toast
	eventually(t, "connect toast", func() bool {
		select {
		case e := <-events:
			if e.Type == "toast" {
				toast = e.Toast
			}
		default:
		}
		return toast != nil
	})
	if !toast.Connected || toast.Player != 1 || toast.Name != "Pro Controller 2" {
		t.Fatalf("toast %+v", toast)
	}

	set := snap.Config
	set.Gyro, set.Port, set.Vibration = true, 0, 3
	if err := s.SaveSettings(set); err == nil {
		t.Fatal("invalid port must be rejected")
	}
	set.Port = 26761
	set.Host = "127.0.0.1"
	if err := s.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().Pending || s.Snapshot().DSU.Running {
		t.Fatal("saved settings apply only after restart")
	}
	s.Restart()
	eventually(t, "restart applies settings", func() bool {
		sn := s.Snapshot()
		return !sn.Pending && !sn.Restarting && sn.DSU.Running && len(sn.Players) == 1
	})
	r.mu.Lock()
	connects := r.connects
	r.mu.Unlock()
	if connects != 2 {
		t.Fatalf("restart should reconnect the controller once more, connects=%d", connects)
	}
	data, _ := os.ReadFile(s.opt.ConfigPath)
	if !strings.Contains(string(data), "vibration_strength_xbox: 3") || !strings.Contains(string(data), "gyro_passthrough_mode: Cemuhook") {
		t.Fatalf("config not written:\n%s", data)
	}
}

func TestFaults(t *testing.T) {
	s := start(t, Options{
		Radio:       func() (lifecycle.Radio, error) { return &radio{}, nil },
		OpenBackend: func(string) (virtualpad.Backend, error) { return nil, errors.New("ViGEmBus not installed") },
	})
	eventually(t, "output fault", func() bool { f := s.Snapshot().Fault; return f != nil && f.Kind == "output" })

	s = start(t, Options{Radio: func() (lifecycle.Radio, error) { return nil, errors.New("adapter off") }})
	eventually(t, "bluetooth fault", func() bool { f := s.Snapshot().Fault; return f != nil && f.Kind == "bluetooth" })
	if d := s.Diagnostics(); d[0].Name != "Bluetooth" || d[0].OK {
		t.Fatalf("diagnostics %+v", d)
	}
}

func TestPlayerActionsWithoutRuntime(t *testing.T) {
	s, err := New(Options{ConfigPath: filepath.Join(t.TempDir(), "c.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if s.Disconnect(1) == nil || s.TestRumble(1) == nil {
		t.Fatal("actions need a running app")
	}
	if _, _, err := s.Watch(1); err == nil {
		t.Fatal("watch needs a running app")
	}
}

func TestDiscoveryResultExpires(t *testing.T) {
	s := start(t, Options{Radio: func() (lifecycle.Radio, error) { return &radio{}, nil }})
	eventually(t, "ready", func() bool { return s.Snapshot().Discovery.Step == 4 })
	s.mu.Lock()
	s.discovery.at = time.Now().Add(-terminalHold - time.Second)
	s.mu.Unlock()
	if d := s.Snapshot().Discovery; d.Step != 0 || d.Text != "Procurando controles…" {
		t.Fatalf("an old result should give way to the live search state: %+v", d)
	}
}
