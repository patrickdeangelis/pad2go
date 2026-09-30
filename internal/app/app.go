// Package app wires connected controllers to virtual pads: it assigns player
// slots, merges Joy-Con pairs, converts input, routes rumble back to the
// controllers and publishes motion to the DSU server.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/angelispatrick/switch2go/internal/config"
	"github.com/angelispatrick/switch2go/internal/controller"
	"github.com/angelispatrick/switch2go/internal/dsu"
	"github.com/angelispatrick/switch2go/internal/mapping"
	"github.com/angelispatrick/switch2go/internal/protocol"
	"github.com/angelispatrick/switch2go/internal/virtualpad"
)

// RumbleInterval is how often an active rumble is re-sent (~60 Hz).
const RumbleInterval = 16667 * time.Microsecond

// Device is what the app needs from a connected controller: calibrated
// input out, LEDs and rumble in. *controller.Controller satisfies it; tests
// use fakes.
type Device interface {
	Address() string
	Kind() protocol.Kind
	Name() string
	OnInput(func(protocol.Input))
	SetPlayerLEDs(ctx context.Context, player int) error
	Rumble(protocol.Vibration) error
	Close() error
}

var _ Device = (*controller.Controller)(nil)

// App manages player slots.
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	backend  virtualpad.Backend
	dsu      *dsu.Server
	remapper *mapping.Remapper
	layout   mapping.Layout

	mu    sync.Mutex
	slots []*slot
}

// New creates an app. dsuServer may be nil.
func New(cfg *config.Config, backend virtualpad.Backend, dsuServer *dsu.Server, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	remapper, warnings := mapping.NewRemapper(mapping.RemapSettings{
		Home: cfg.HomeMapping, Capt: cfg.CaptMapping, C: cfg.CMapping,
		GL: cfg.GLMapping, GR: cfg.GRMapping,
		SLL: cfg.SLLMapping, SRL: cfg.SRLMapping, SLR: cfg.SLRMapping, SRR: cfg.SRRMapping,
	})
	for _, w := range warnings {
		log.Warn(w)
	}
	return &App{
		cfg: cfg, log: log, backend: backend, dsu: dsuServer,
		remapper: remapper, layout: mapping.ParseLayout(cfg.ABXYMode),
		slots: make([]*slot, cfg.MaxControllers),
	}
}

// ErrFull is returned when every player slot is taken.
var ErrFull = errors.New("all player slots are in use")

// Full reports whether no new controller can be added.
func (a *App) Full() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if s == nil {
			return false
		}
		if a.cfg.CombineJoyCons && s.singleJoyCon() {
			return false
		}
	}
	return true
}

// Connected reports whether a device with addr is attached.
func (a *App) Connected(addr string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if s != nil && s.find(addr) != nil {
			return true
		}
	}
	return false
}

// AddDevice attaches a connected, initialized controller to a player slot.
func (a *App) AddDevice(ctx context.Context, d Device) error {
	a.mu.Lock()
	m := &member{dev: d, hold: a.cfg.HoldMode(d.Address())}
	var target *slot
	if a.cfg.CombineJoyCons && d.Kind().IsJoyCon() {
		for _, s := range a.slots {
			if s != nil && s.singleJoyCon() && s.members[0].dev.Kind() != d.Kind() {
				target = s
				break
			}
		}
	}
	if target == nil {
		idx := slices.Index(a.slots, nil)
		if idx < 0 {
			a.mu.Unlock()
			return ErrFull
		}
		s := &slot{app: a, player: idx + 1, states: map[*member]protocol.Input{}}
		pad, err := a.backend.NewPad(s.onRumble)
		if err != nil {
			a.mu.Unlock()
			return fmt.Errorf("create virtual pad: %w", err)
		}
		s.pad = pad
		a.slots[idx] = s
		target = s
	}
	m.rumbler = newRumbler(d, a.log)
	target.mu.Lock()
	target.members = append(target.members, m)
	target.mu.Unlock()
	player, members := target.player, target.snapshot()
	a.mu.Unlock()

	d.OnInput(func(in protocol.Input) { target.onInput(m, in) })
	for _, mm := range members {
		if err := mm.dev.SetPlayerLEDs(ctx, player); err != nil {
			a.log.Warn("set player LEDs failed", "addr", mm.dev.Address(), "err", err)
		}
	}
	names := make([]string, len(members))
	for i, mm := range members {
		names[i] = mm.dev.Name()
	}
	a.log.Info("controller assigned", "player", player, "controllers", names)
	return nil
}

// RemoveDevice detaches a controller (e.g. after it disconnects).
func (a *App) RemoveDevice(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, s := range a.slots {
		if s == nil {
			continue
		}
		m := s.find(addr)
		if m == nil {
			continue
		}
		m.dev.OnInput(nil)
		m.rumbler.stop()
		s.mu.Lock()
		s.members = slices.DeleteFunc(s.members, func(x *member) bool { return x == m })
		delete(s.states, m)
		empty := len(s.members) == 0
		s.mu.Unlock()
		if empty {
			if err := s.pad.Close(); err != nil {
				a.log.Warn("close virtual pad", "err", err)
			}
			a.slots[i] = nil
		}
		a.log.Info("controller removed", "addr", addr, "player", s.player)
		return
	}
}

// Close disconnects every controller and closes every virtual pad.
func (a *App) Close() {
	a.mu.Lock()
	var devs []Device
	for _, s := range a.slots {
		if s != nil {
			for _, m := range s.snapshot() {
				devs = append(devs, m.dev)
			}
		}
	}
	a.mu.Unlock()
	for _, d := range devs {
		a.RemoveDevice(d.Address())
		_ = d.Close()
	}
}

type member struct {
	dev     Device
	hold    string
	rumbler *rumbler
}

type slot struct {
	app    *App
	player int
	pad    virtualpad.Pad

	mu      sync.Mutex
	members []*member
	states  map[*member]protocol.Input
}

func (s *slot) snapshot() []*member {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.members)
}

func (s *slot) find(addr string) *member {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.members {
		if m.dev.Address() == addr {
			return m
		}
	}
	return nil
}

func (s *slot) singleJoyCon() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.members) == 1 && s.members[0].dev.Kind().IsJoyCon()
}

func (s *slot) onInput(m *member, in protocol.Input) {
	a := s.app
	kept, extra := a.remapper.Apply(in.Buttons)
	st := in
	st.Buttons = kept

	s.mu.Lock()
	var out protocol.Input
	kind := m.dev.Kind()
	switch len(s.members) {
	case 1:
		if kind.IsJoyCon() {
			side := mapping.LeftJoyCon
			if kind == protocol.KindJoyConRight {
				side = mapping.RightJoyCon
			}
			st = mapping.OrientSingleJoyCon(side, m.hold == config.HoldHorizontal, st)
		}
		st.Buttons |= extra
		out = st
	default:
		st.Buttons |= extra
		s.states[m] = st
		var left, right protocol.Input
		for mm, ms := range s.states {
			if mm.dev.Kind() == protocol.KindJoyConLeft {
				left = ms
			} else {
				right = ms
			}
		}
		out = mapping.Merge(left, right)
	}
	s.mu.Unlock()

	if err := s.pad.Update(mapping.ToXbox(out, a.layout, kind == protocol.KindGameCube)); err != nil {
		a.log.Debug("virtual pad update failed", "err", err)
	}
	if a.dsu != nil {
		a.publishMotion(m, in, st)
	}
}

func (a *App) publishMotion(m *member, in, st protocol.Input) {
	kind := m.dev.Kind()
	side := 0
	if kind == protocol.KindJoyConRight {
		side = 1
	}
	accel, gyro := dsu.Motion(in.Accel, in.Gyro, kind.ProLike(), side, m.hold == config.HoldHorizontal, a.cfg.CemuhookSensitivity)
	model := byte(dsu.ModelDS4)
	if !kind.ProLike() {
		model = dsu.ModelJoyCon
	}
	a.dsu.Publish(dsu.Pad{
		MAC: addressMAC(m.dev.Address()), Model: model,
		Battery: dsu.BatteryLevel(protocol.BatteryPercent(in.BatteryVoltage)),
		Buttons: st.Buttons, LX: st.Left.X, LY: st.Left.Y, RX: st.Right.X, RY: st.Right.Y,
		Accel: accel, Gyro: gyro,
	})
}

// onRumble receives force feedback from the virtual pad.
func (s *slot) onRumble(large, small uint8) {
	a := s.app
	v := protocol.FromMotors(large, small, a.cfg.VibrationStrength)
	apply := func() {
		for _, m := range s.snapshot() {
			m.rumbler.set(v)
		}
	}
	if a.cfg.RumbleDelayMs > 0 {
		time.AfterFunc(time.Duration(a.cfg.RumbleDelayMs)*time.Millisecond, apply)
		return
	}
	apply()
}

// rumbler keeps an active vibration alive by re-sending it at RumbleInterval
// and sends a single silent frame when it stops.
type rumbler struct {
	dev  Device
	log  *slog.Logger
	mu   sync.Mutex
	cur  protocol.Vibration
	wake chan struct{}
	done chan struct{}
	once sync.Once
}

func newRumbler(d Device, log *slog.Logger) *rumbler {
	r := &rumbler{dev: d, log: log, cur: protocol.SilentVibration(), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go r.loop()
	return r
}

func (r *rumbler) set(v protocol.Vibration) {
	r.mu.Lock()
	r.cur = v
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *rumbler) stop() { r.once.Do(func() { close(r.done) }) }

func active(v protocol.Vibration) bool { return v.LFAmp > 0 || v.HFAmp > 0 }

func (r *rumbler) loop() {
	ticker := time.NewTicker(RumbleInterval)
	defer ticker.Stop()
	wasActive := false
	for {
		select {
		case <-r.done:
			return
		case <-r.wake:
		case <-ticker.C:
			if !wasActive {
				continue
			}
		}
		r.mu.Lock()
		v := r.cur
		r.mu.Unlock()
		if !active(v) && !wasActive {
			continue
		}
		if err := r.dev.Rumble(v); err != nil {
			r.log.Debug("rumble write failed", "addr", r.dev.Address(), "err", err)
		}
		wasActive = active(v)
	}
}

// addressMAC derives a stable 6-byte ID from an address string: the MAC
// itself when it parses, otherwise a hash (macOS exposes UUIDs, not MACs).
func addressMAC(addr string) [6]byte {
	var out [6]byte
	if v, err := config.ParseMAC(addr); err == nil {
		for i := range 6 {
			out[i] = byte(v >> (8 * (5 - i)))
		}
		return out
	}
	var h uint64 = 14695981039346656037
	for _, c := range []byte(addr) {
		h ^= uint64(c)
		h *= 1099511628211
	}
	for i := range 6 {
		out[i] = byte(h >> (8 * i))
	}
	out[0] = out[0]&0xFE | 0x02 // locally administered, unicast
	return out
}
