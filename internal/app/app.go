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
	"sync/atomic"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/controller"
	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
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
	cfg     *config.Config
	log     *slog.Logger
	backend virtualpad.Backend
	dsu     *dsu.Server
	rules   *mapping.Rules

	mu    sync.Mutex
	slots []*slot

	onChange func()
}

// New creates an app. dsuServer may be nil.
func New(cfg *config.Config, backend virtualpad.Backend, dsuServer *dsu.Server, log *slog.Logger) *App {
	if log == nil {
		log = slog.Default()
	}
	rules, warnings := mapping.NewRules(cfg.ABXYMode, mapping.RemapSettings{
		Home: cfg.HomeMapping, Capt: cfg.CaptMapping, C: cfg.CMapping,
		GL: cfg.GLMapping, GR: cfg.GRMapping,
		SLL: cfg.SLLMapping, SRL: cfg.SRLMapping, SLR: cfg.SLRMapping, SRR: cfg.SRRMapping,
	})
	for _, w := range warnings {
		log.Warn(w)
	}
	return &App{
		cfg: cfg, log: log, backend: backend, dsu: dsuServer, rules: rules,
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
		if a.cfg.CombineJoyCons && (s.canJoin(protocol.KindJoyConLeft) || s.canJoin(protocol.KindJoyConRight)) {
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

// AddDevice attaches a connected, initialized controller to a player slot:
// a lone Joy-Con waiting for its other half if combining is on, otherwise
// the first free slot.
func (a *App) AddDevice(ctx context.Context, d Device) error {
	a.mu.Lock()
	kind := d.Kind()
	var target *slot
	if a.cfg.CombineJoyCons && kind.IsJoyCon() {
		for _, s := range a.slots {
			if s != nil && s.canJoin(kind) {
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
		s := &slot{app: a, player: idx + 1, pad: a.rules.NewPlayerPad()}
		out, err := a.backend.NewPad(s.onRumble)
		if err != nil {
			a.mu.Unlock()
			return fmt.Errorf("create virtual pad: %w", err)
		}
		s.out = out
		a.slots[idx] = s
		target = s
	}
	m := &member{dev: d, rumbler: newRumbler(d, a.log)}
	m.battery.Store(-1)
	target.mu.Lock()
	err := target.pad.Join(d.Address(), kind, a.cfg.HoldMode(d.Address()) == config.HoldHorizontal)
	if err == nil {
		target.members = append(target.members, m)
	}
	target.mu.Unlock()
	if err != nil {
		m.rumbler.stop()
		a.mu.Unlock()
		return err
	}
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
	a.changed()
	return nil
}

// RemoveDevice detaches a controller (e.g. after it disconnects).
func (a *App) RemoveDevice(addr string) {
	if a.removeDevice(addr) {
		a.changed()
	}
}

func (a *App) removeDevice(addr string) bool {
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
		empty := s.pad.Leave(addr)
		s.mu.Unlock()
		if empty {
			if err := s.out.Close(); err != nil {
				a.log.Warn("close virtual pad", "err", err)
			}
			a.slots[i] = nil
		}
		a.log.Info("controller removed", "addr", addr, "player", s.player)
		return true
	}
	return false
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
	rumbler *rumbler
	battery atomic.Int32 // percent, -1 until known
}

// slot is one player slot: the player pad state, the OS virtual pad it
// drives, and the controllers feeding it.
type slot struct {
	app    *App
	player int

	// mu serializes player pad updates and the virtual pad writes they
	// produce, so a Joy-Con pair's reports reach the OS in order.
	mu       sync.Mutex
	pad      *mapping.PlayerPad
	out      virtualpad.Pad
	members  []*member
	watchers map[chan Sample]struct{}
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

func (s *slot) canJoin(k protocol.Kind) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.members) > 0 && s.pad.CanJoin(k)
}

func (s *slot) onInput(m *member, in protocol.Input) {
	a := s.app
	s.mu.Lock()
	f, ok := s.pad.Update(m.dev.Address(), in)
	if ok {
		if err := s.out.Update(f.Xbox); err != nil {
			a.log.Debug("virtual pad update failed", "err", err)
		}
		for w := range s.watchers {
			select {
			case w <- Sample{Xbox: f.Xbox, Gyro: f.Motion.Gyro, AnalogTriggers: in.AnalogTriggers}:
			default: // slow watcher: drop the sample
			}
		}
	}
	s.mu.Unlock()
	if m.dev.Kind() != protocol.KindGameCube { // the GameCube pad reports no battery
		m.battery.Store(int32(protocol.BatteryPercent(in.BatteryVoltage)))
	}
	if ok && a.dsu != nil {
		a.publishMotion(m.dev, in, f)
	}
}

func (a *App) publishMotion(d Device, in protocol.Input, f mapping.Frame) {
	model := byte(dsu.ModelDS4)
	if !d.Kind().ProLike() {
		model = dsu.ModelJoyCon
	}
	gyro := f.Motion.Gyro
	gyro[1] *= dsu.YawScale(a.cfg.CemuhookSensitivity)
	c := f.Controller
	a.dsu.Publish(dsu.Pad{
		MAC: addressMAC(d.Address()), Model: model,
		Battery: dsu.BatteryLevel(protocol.BatteryPercent(in.BatteryVoltage)),
		Buttons: c.Buttons, LX: c.Left.X, LY: c.Left.Y, RX: c.Right.X, RY: c.Right.Y,
		Accel: f.Motion.Accel, Gyro: gyro,
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

// OnChange registers fn to run (outside the app's locks) whenever a
// controller is attached or removed.
func (a *App) OnChange(fn func()) {
	a.mu.Lock()
	a.onChange = fn
	a.mu.Unlock()
}

func (a *App) changed() {
	a.mu.Lock()
	fn := a.onChange
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Player is a snapshot of one player slot.
type Player struct {
	Number  int
	Members []Member
}

// Member is one controller in a player slot.
type Member struct {
	Addr    string
	Name    string
	Kind    protocol.Kind
	Battery int // percent; -1 when unknown
}

// Players returns the occupied player slots in order.
func (a *App) Players() []Player {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Player
	for _, s := range a.slots {
		if s == nil {
			continue
		}
		p := Player{Number: s.player}
		for _, m := range s.snapshot() {
			p.Members = append(p.Members, Member{
				Addr: m.dev.Address(), Name: m.dev.Name(), Kind: m.dev.Kind(), Battery: int(m.battery.Load()),
			})
		}
		out = append(out, p)
	}
	return out
}

// Sample is what one input report did to a player slot, for input tests.
type Sample struct {
	Xbox           mapping.XboxState
	Gyro           [3]float32 // deg/s of the reporting controller
	AnalogTriggers bool
}

// ErrNoPlayer is returned for an empty player slot.
var ErrNoPlayer = errors.New("no controller in this player slot")

func (a *App) slotFor(player int) *slot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if player < 1 || player > len(a.slots) {
		return nil
	}
	return a.slots[player-1]
}

// Watch streams the player's samples until cancel is called. Samples are
// dropped rather than queued when the reader falls behind.
func (a *App) Watch(player int) (samples <-chan Sample, cancel func(), err error) {
	s := a.slotFor(player)
	if s == nil {
		return nil, nil, ErrNoPlayer
	}
	ch := make(chan Sample, 8)
	s.mu.Lock()
	if s.watchers == nil {
		s.watchers = map[chan Sample]struct{}{}
	}
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.watchers, ch)
			s.mu.Unlock()
		})
	}, nil
}

// TestRumble vibrates the player's controllers for d at the configured
// strength.
func (a *App) TestRumble(player int, d time.Duration) error {
	s := a.slotFor(player)
	if s == nil {
		return ErrNoPlayer
	}
	v := protocol.FromMotors(200, 200, a.cfg.VibrationStrength)
	for _, m := range s.snapshot() {
		m.rumbler.set(v)
	}
	time.AfterFunc(d, func() {
		for _, m := range s.snapshot() {
			m.rumbler.set(protocol.SilentVibration())
		}
	})
	return nil
}

// Disconnect detaches and disconnects every controller in the player slot.
func (a *App) Disconnect(player int) error {
	s := a.slotFor(player)
	if s == nil {
		return ErrNoPlayer
	}
	for _, m := range s.snapshot() {
		a.RemoveDevice(m.dev.Address())
		_ = m.dev.Close()
	}
	return nil
}
