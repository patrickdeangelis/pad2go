// Package lifecycle owns the connection lifecycle of controllers: which
// advertisements to accept, connecting, initializing, pairing, attaching to
// a player slot, and detaching when the link drops at any stage.
//
// The Bluetooth radio sits behind the Radio port: the tinygo BLE adapter in
// production (internal/ble) and an in-memory radio in tests.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/controller"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// Advert is a supported controller seen while scanning.
type Advert struct {
	Addr string
	Adv  protocol.Advertisement
	RSSI int16
}

// Radio is the Bluetooth port.
type Radio interface {
	// Scan blocks until accept approves an advertisement from a supported
	// controller, and returns it. accept may be called from another goroutine.
	Scan(ctx context.Context, accept func(Advert) bool) (Advert, error)
	// Connect opens a GATT link. onDisconnect runs once when the link drops,
	// possibly before Connect returns.
	Connect(ctx context.Context, a Advert, onDisconnect func()) (controller.Transport, error)
	// HostMAC is this adapter's Bluetooth address, used for pairing.
	HostMAC() (uint64, error)
}

// Manager connects controllers and attaches them to the app until its
// context is cancelled.
type Manager struct {
	Radio  Radio
	App    *app.App
	Config *config.Config
	Log    *slog.Logger
	// Backoff is the wait while every slot is full or after a scan error
	// (default 1 s).
	Backoff time.Duration
	// ConnectTimeout bounds connecting, initializing and pairing (default 30 s).
	ConnectTimeout time.Duration
	// OnEvent, if set, receives progress events (from the Run goroutine or a
	// disconnect callback). It must not block.
	OnEvent func(Event)
}

// Stage is a step of the connection lifecycle.
type Stage int

const (
	Scanning   Stage = iota // looking for controllers
	Full                    // every player slot is taken; not scanning
	Found                   // an advert was accepted
	Connecting              // GATT link + initialization
	Pairing                 // bonding to this host
	Ready                   // attached to a player slot
	Ignored                 // bonded to another host and not accepted
	Failed                  // connect, initialize, pair or attach failed
	Dropped                 // the link dropped (before or after attach)
)

// Event reports lifecycle progress for one controller (Addr empty for
// Scanning and Full).
type Event struct {
	Stage Stage
	Addr  string
	Model string
	Err   error
	// Unpaired is set on Ready when a controller in sync mode could not be
	// paired because the host MAC is unknown.
	Unpaired bool
}

func (m *Manager) emit(e Event) {
	if m.OnEvent != nil {
		m.OnEvent(e)
	}
}

// ErrDropped is returned when the link drops before the controller is attached.
var ErrDropped = errors.New("controller disconnected before it was attached")

// Run scans and connects until ctx is done. It always returns ctx.Err().
func (m *Manager) Run(ctx context.Context) error {
	if m.Log == nil {
		m.Log = slog.Default()
	}
	if m.Backoff == 0 {
		m.Backoff = time.Second
	}
	if m.ConnectTimeout == 0 {
		m.ConnectTimeout = 30 * time.Second
	}
	host, hostKnown := m.hostMAC()

	var reported sync.Map
	accept := func(a Advert) bool {
		if m.App.Connected(a.Addr) {
			return false
		}
		if a.Adv.Pairing() || !hostKnown || a.Adv.ReconnectMAC == host || m.Config.AcceptForeignControllers {
			return true
		}
		if _, dup := reported.LoadOrStore(a.Addr, true); !dup {
			m.emit(Event{Stage: Ignored, Addr: a.Addr, Model: protocol.ControllerNames[a.Adv.ProductID]})
			m.Log.Info("ignoring controller paired to another host (hold SYNC to pair, or set accept_foreign_controllers)",
				"addr", a.Addr, "model", protocol.ControllerNames[a.Adv.ProductID])
		}
		return false
	}

	m.Log.Info("press a button on a paired controller, or hold SYNC on an unpaired one")
	var last Stage = -1
	stage := func(s Stage) {
		if s != last {
			last = s
			m.emit(Event{Stage: s})
		}
	}
	for ctx.Err() == nil {
		if m.App.Full() {
			stage(Full)
			sleep(ctx, m.Backoff)
			continue
		}
		stage(Scanning)
		a, err := m.Radio.Scan(ctx, accept)
		if err != nil {
			if ctx.Err() == nil {
				m.Log.Warn("scan failed; retrying", "err", err)
				sleep(ctx, m.Backoff)
			}
			continue
		}
		last = -1
		if err := m.connect(ctx, a, host, hostKnown); err != nil {
			m.emit(Event{Stage: Failed, Addr: a.Addr, Model: protocol.ControllerNames[a.Adv.ProductID], Err: err})
			m.Log.Warn("connection failed; press a button or hold SYNC to retry", "addr", a.Addr, "err", err)
		}
	}
	return ctx.Err()
}

// hostMAC prefers the configured address over the adapter's own.
func (m *Manager) hostMAC() (uint64, bool) {
	if m.Config.HostMAC != "" {
		if v, err := config.ParseMAC(m.Config.HostMAC); err == nil {
			m.Log.Info("host Bluetooth address (configured)", "mac", FormatMAC(v))
			return v, true
		}
	}
	v, err := m.Radio.HostMAC()
	if err != nil {
		m.Log.Warn("host Bluetooth address unknown; controllers will not be paired for button-press reconnect", "err", err)
		return 0, false
	}
	m.Log.Info("host Bluetooth address", "mac", FormatMAC(v))
	return v, true
}

// link tracks one connection so a drop at any stage is handled exactly once:
// before attach it cancels the attach; after attach it detaches.
type link struct {
	mu       sync.Mutex
	dropped  bool
	attached bool
}

func (m *Manager) connect(ctx context.Context, a Advert, host uint64, hostKnown bool) error {
	model := protocol.ControllerNames[a.Adv.ProductID]
	m.emit(Event{Stage: Found, Addr: a.Addr, Model: model})
	m.Log.Info("connecting", "addr", a.Addr, "model", model, "pairing", a.Adv.Pairing())
	cctx, cancel := context.WithTimeout(ctx, m.ConnectTimeout)
	defer cancel()

	l := &link{}
	t, err := m.Radio.Connect(cctx, a, func() {
		l.mu.Lock()
		l.dropped = true
		attached := l.attached
		l.mu.Unlock()
		m.Log.Info("controller disconnected", "addr", a.Addr)
		m.emit(Event{Stage: Dropped, Addr: a.Addr, Model: model})
		if attached {
			m.App.RemoveDevice(a.Addr)
		}
	})
	if err != nil {
		return err
	}
	m.emit(Event{Stage: Connecting, Addr: a.Addr, Model: model})

	c := controller.New(t, m.Log)
	err = c.Initialize(cctx, controller.Options{
		AdvertisedPID:        a.Adv.ProductID,
		GCTriggerMode:        m.Config.GCTriggerMode,
		GCTriggerCalibration: m.Config.GCTriggerCalibration[a.Addr],
		Deadzone:             func(k protocol.Kind) float64 { return m.Config.Deadzone(k.DeadzoneFamily()) },
	})
	if err == nil && a.Adv.Pairing() && hostKnown {
		m.emit(Event{Stage: Pairing, Addr: a.Addr, Model: model})
		if err = c.Pair(cctx, host); err == nil {
			m.Log.Info("paired; the controller will now reconnect with a button press", "addr", a.Addr)
		} else {
			err = fmt.Errorf("pair: %w", err)
		}
	}
	if err != nil {
		_ = c.Close()
		return err
	}

	l.mu.Lock()
	if l.dropped {
		l.mu.Unlock()
		_ = c.Close()
		return ErrDropped
	}
	err = m.App.AddDevice(context.WithoutCancel(cctx), c)
	l.attached = err == nil
	l.mu.Unlock()
	if err != nil {
		_ = c.Close()
		return err
	}
	m.emit(Event{Stage: Ready, Addr: a.Addr, Model: model, Unpaired: a.Adv.Pairing() && !hostKnown})
	if m.Config.ConnectHaptics {
		go c.ConnectHaptics(context.WithoutCancel(ctx))
	}
	return nil
}

// FormatMAC prints a pairing-protocol MAC value as AA:BB:CC:DD:EE:FF.
func FormatMAC(v uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
