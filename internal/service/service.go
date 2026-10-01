// Package service supervises a running pad2go: it loads the configuration,
// builds the virtual gamepad output, DSU server, app and connection
// lifecycle, rebuilds them on restart, and exposes a snapshot and events for
// the user interface.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/lifecycle"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

// Options configures a Service.
type Options struct {
	ConfigPath string
	// Radio returns the Bluetooth radio. It is retried on every restart until
	// it succeeds.
	Radio func() (lifecycle.Radio, error)
	// OpenBackend opens the virtual gamepad output (virtualpad.Open).
	OpenBackend func(kind string) (virtualpad.Backend, error)
	Version     string
	Log         *slog.Logger
	// Backoff and ConnectTimeout are passed to the lifecycle manager.
	Backoff, ConnectTimeout time.Duration
}

// Service is a supervised pad2go runtime.
type Service struct {
	opt Options
	log *slog.Logger

	mu         sync.Mutex
	saved      *config.Config // on disk
	applied    *config.Config // running
	radio      lifecycle.Radio
	app        *app.App
	dsu        *dsu.Server
	backend    string // "none" for motion-only output
	fault      *Fault
	dsuErr     string
	discovery  Discovery
	scanStage  lifecycle.Stage // latest Scanning / Full
	restarting bool
	cancelRun  context.CancelFunc
	players    []app.Player // last snapshot, for connect/disconnect toasts

	subMu sync.Mutex
	subs  map[chan Event]struct{}
}

// Fault is a condition that stops controllers from working.
type Fault struct {
	Kind    string `json:"kind"` // "bluetooth", "output" or "config"
	Message string `json:"message"`
}

// Discovery is the connection progress shown in the UI.
type Discovery struct {
	Step    int    `json:"step"` // 0 searching … 4 ready; -1 idle
	Text    string `json:"text"`
	Foreign bool   `json:"foreign"` // last controller was ignored: bonded to another PC
	Failure bool   `json:"failure"`
	// Bonded: the last failure was a controller bonded to another host
	// (usually the console); the UI shows how to put it in sync mode.
	Bonded bool `json:"bonded"`
	at     time.Time
}

// Event is pushed to subscribers.
type Event struct {
	Type  string `json:"type"` // "state" or "toast"
	Toast *Toast `json:"toast,omitempty"`
}

// Toast announces a controller connecting or disconnecting.
type Toast struct {
	Name      string `json:"name"`
	Player    int    `json:"player"`
	Connected bool   `json:"connected"`
}

// New loads the configuration. A missing file yields the defaults.
func New(opt Options) (*Service, error) {
	cfg, err := config.Load(opt.ConfigPath)
	if err != nil {
		return nil, err
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	return &Service{opt: opt, log: opt.Log, saved: cfg, applied: cfg,
		discovery: Discovery{Step: -1}, subs: map[chan Event]struct{}{}}, nil
}

// UIPort is the configured web interface port (0 = disabled).
func (s *Service) UIPort() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved.UIPort
}

// Platform is the user-facing OS name.
func Platform() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	}
	return "Linux"
}

// Run supervises the runtime until ctx is done, rebuilding it on Restart.
func (s *Service) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		runCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.cancelRun = cancel
		cfg := s.saved
		s.applied = cfg
		s.mu.Unlock()
		s.runOnce(runCtx, cfg)
		cancel()
	}
	return ctx.Err()
}

// Restart reloads the saved configuration and rebuilds the runtime. Connected
// controllers are disconnected; they reconnect with a button press.
func (s *Service) Restart() {
	s.mu.Lock()
	cancel := s.cancelRun
	s.restarting = true
	s.mu.Unlock()
	s.notify()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) setFault(f *Fault) {
	s.mu.Lock()
	s.fault = f
	s.mu.Unlock()
	s.notify()
}

func (s *Service) runOnce(ctx context.Context, cfg *config.Config) {
	defer func() {
		s.mu.Lock()
		s.app, s.dsu = nil, nil
		s.discovery = Discovery{Step: -1}
		s.mu.Unlock()
		s.notify()
	}()
	s.mu.Lock()
	s.fault, s.dsuErr = nil, ""
	s.mu.Unlock()

	backend, err := s.opt.OpenBackend(cfg.Output)
	if err != nil {
		s.log.Error("virtual gamepad output unavailable", "err", err)
		s.setFault(&Fault{Kind: "output", Message: err.Error()})
		s.started()
		<-ctx.Done()
		return
	}
	defer backend.Close()

	var server *dsu.Server
	if cfg.CemuhookEnabled() {
		addr := net.JoinHostPort(cfg.CemuhookHost, strconv.Itoa(cfg.CemuhookPort))
		if server, err = dsu.Listen(addr, s.log); err != nil {
			s.log.Error("DSU server failed to start", "err", err)
			s.mu.Lock()
			s.dsuErr = err.Error()
			s.mu.Unlock()
		} else {
			defer server.Close()
			s.log.Info("CemuHook/DSU motion server listening", "addr", server.Addr())
		}
	}

	a := app.New(cfg, backend, server, s.log)
	a.OnChange(s.appChanged)
	defer a.Close()
	s.mu.Lock()
	s.app, s.dsu, s.backend = a, server, backend.Name()
	if _, ok := backend.(virtualpad.Null); ok {
		s.backend = "none"
	}
	s.mu.Unlock()

	radio, err := s.radioOnce()
	if err != nil {
		s.log.Error("Bluetooth unavailable", "err", err)
		s.setFault(&Fault{Kind: "bluetooth", Message: err.Error()})
		s.started()
		<-ctx.Done()
		return
	}
	s.started()
	m := &lifecycle.Manager{Radio: radio, App: a, Config: cfg, Log: s.log,
		Backoff: s.opt.Backoff, ConnectTimeout: s.opt.ConnectTimeout, OnEvent: s.lifecycleEvent}
	_ = m.Run(ctx)
}

// started marks the runtime as rebuilt (clearing "restarting").
func (s *Service) started() {
	s.mu.Lock()
	s.restarting = false
	s.mu.Unlock()
	s.notify()
}

func (s *Service) radioOnce() (lifecycle.Radio, error) {
	s.mu.Lock()
	r := s.radio
	s.mu.Unlock()
	if r != nil {
		return r, nil
	}
	if s.opt.Radio == nil {
		return nil, errors.New("no Bluetooth radio configured")
	}
	r, err := s.opt.Radio()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.radio = r
	s.mu.Unlock()
	return r, nil
}

// terminalHold keeps a result (ready, ignored, failed) visible this long
// before "searching" replaces it.
const terminalHold = 6 * time.Second

func (s *Service) lifecycleEvent(e lifecycle.Event) {
	s.mu.Lock()
	d := &s.discovery
	now := time.Now()
	recent := !d.at.IsZero() && now.Sub(d.at) < terminalHold
	set := func(step int, text string) { *d = Discovery{Step: step, Text: text} }
	switch e.Stage {
	case lifecycle.Scanning, lifecycle.Full:
		s.scanStage = e.Stage
		if !recent {
			*d = idleDiscovery(e.Stage)
		}
	case lifecycle.Found:
		set(1, e.Model+" encontrado.")
	case lifecycle.Connecting:
		set(2, "Conectando "+e.Model+"…")
	case lifecycle.Pairing:
		set(3, "Pareando "+e.Model+" com este PC…")
	case lifecycle.Ready:
		text := e.Model + " pronto."
		if s.app != nil {
			for _, p := range s.app.Players() {
				for _, m := range p.Members {
					if m.Addr == e.Addr {
						text = fmt.Sprintf("%s pronto · Jogador %d.", e.Model, p.Number)
					}
				}
			}
		}
		if e.Unpaired {
			text += " Sem o endereço Bluetooth do PC, o pareamento não é salvo."
		}
		set(4, text)
		d.at = now
	case lifecycle.Ignored:
		*d = Discovery{Step: 1, Text: e.Model + " ignorado: está pareado com outro aparelho.", Foreign: true, at: now}
	case lifecycle.Failed:
		msg := "Falha ao conectar"
		if e.Model != "" {
			msg += " " + e.Model
		}
		if errors.Is(e.Err, app.ErrFull) {
			msg = "Todos os jogadores estão ocupados"
		}
		if errors.Is(e.Err, lifecycle.ErrBondedElsewhere) {
			*d = Discovery{Step: 2, Text: e.Model + " está pareado com outro aparelho, provavelmente o Switch 2. Desligue o console e segure SYNC até as luzes correrem.", Failure: true, Bonded: true, at: now}
			break
		}
		*d = Discovery{Step: 2, Text: msg + ". Aproxime o controle e pressione um botão ou segure SYNC.", Failure: true, at: now}
	case lifecycle.Dropped:
		// Disconnect toasts come from the app's player changes.
	}
	s.mu.Unlock()
	s.notify()
}

func idleDiscovery(stage lifecycle.Stage) Discovery {
	if stage == lifecycle.Full {
		return Discovery{Step: -1, Text: "Todos os jogadores estão ocupados. Desconecte um jogador ou aumente o limite."}
	}
	return Discovery{Step: 0, Text: "Procurando controles…"}
}

// appChanged diffs the player snapshot to announce connects and disconnects.
func (s *Service) appChanged() {
	s.mu.Lock()
	a := s.app
	prev := s.players
	s.mu.Unlock()
	if a == nil {
		return
	}
	cur := a.Players()
	s.mu.Lock()
	s.players = cur
	s.mu.Unlock()
	addrs := func(ps []app.Player) map[string]toastInfo {
		m := map[string]toastInfo{}
		for _, p := range ps {
			for _, mm := range p.Members {
				m[mm.Addr] = toastInfo{mm.Name, p.Number}
			}
		}
		return m
	}
	before, after := addrs(prev), addrs(cur)
	for addr, t := range after {
		if _, ok := before[addr]; !ok {
			s.publish(Event{Type: "toast", Toast: &Toast{Name: t.name, Player: t.player, Connected: true}})
		}
	}
	for addr, t := range before {
		if _, ok := after[addr]; !ok {
			s.publish(Event{Type: "toast", Toast: &Toast{Name: t.name, Player: t.player, Connected: false}})
		}
	}
	s.notify()
}

type toastInfo struct {
	name   string
	player int
}

// Subscribe returns a channel of events until cancel is called. State
// events coalesce; slow subscribers miss toasts rather than block.
func (s *Service) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	s.subMu.Lock()
	s.subs[ch] = struct{}{}
	s.subMu.Unlock()
	return ch, func() {
		s.subMu.Lock()
		delete(s.subs, ch)
		s.subMu.Unlock()
	}
}

func (s *Service) publish(e Event) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (s *Service) notify() { s.publish(Event{Type: "state"}) }

// SaveSettings validates the settings and writes them to the configuration
// file. They take effect on Restart.
func (s *Service) SaveSettings(set Settings) error {
	s.mu.Lock()
	next := *s.saved
	s.mu.Unlock()
	set.Apply(&next)
	if err := next.Validate(); err != nil {
		return err
	}
	if err := next.Save(s.opt.ConfigPath); err != nil {
		return err
	}
	s.mu.Lock()
	s.saved = &next
	s.mu.Unlock()
	s.notify()
	return nil
}

// Snapshot is the UI's view of the runtime.
type Snapshot struct {
	Platform   string       `json:"platform"`
	Version    string       `json:"version"`
	Output     OutputView   `json:"output"`
	Fault      *Fault       `json:"fault"`
	Players    []PlayerView `json:"players"`
	Discovery  Discovery    `json:"discovery"`
	DSU        DSUView      `json:"dsu"`
	Config     Settings     `json:"config"`
	Pending    bool         `json:"pending"`
	Restarting bool         `json:"restarting"`
	// Bluetooth is "on", "off" (failed) or "idle" (not started yet).
	Bluetooth string `json:"bluetooth"`
}

// OutputView describes the virtual gamepad output.
type OutputView struct {
	Name       string `json:"name"`
	Detail     string `json:"detail"`
	MotionOnly bool   `json:"motionOnly"`
}

// PlayerView is one occupied player slot.
type PlayerView struct {
	Player  int          `json:"player"`
	Name    string       `json:"name"`
	Members []MemberView `json:"members"`
}

// MemberView is one controller.
type MemberView struct {
	Addr    string  `json:"addr"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`    // left, right, pro, gc
	Battery string  `json:"battery"` // "high", "medium", "low" or "" when unknown
	Volts   float64 `json:"volts"`   // last reported pack voltage, 0 when unknown
}

// DSUView is the motion server status.
type DSUView struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Address string `json:"address"`
	Clients int    `json:"clients"`
	Error   string `json:"error,omitempty"`
}

func kindName(k protocol.Kind) string {
	switch k {
	case protocol.KindJoyConLeft:
		return "left"
	case protocol.KindJoyConRight:
		return "right"
	case protocol.KindGameCube:
		return "gc"
	}
	return "pro"
}

// Snapshot returns the current state.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	platform := Platform()
	snap := Snapshot{
		Platform: platform, Version: s.opt.Version, Fault: s.fault, Discovery: s.discovery,
		Config: SettingsFrom(s.saved), Pending: !reflect.DeepEqual(s.saved, s.applied),
		Restarting: s.restarting, Players: []PlayerView{},
	}
	snap.Bluetooth = "idle"
	switch {
	case s.fault != nil && s.fault.Kind == "bluetooth":
		snap.Bluetooth = "off"
	case s.radio != nil:
		snap.Bluetooth = "on"
	}
	// A result (ready, failed) shows for terminalHold, then the live search
	// state returns. An ignored foreign controller stays until acted on.
	if d := s.discovery; !d.at.IsZero() && !d.Foreign && time.Since(d.at) > terminalHold && s.app != nil {
		snap.Discovery = idleDiscovery(s.scanStage)
	}
	motionOnly := s.backend == "none" || platform == "macOS"
	snap.Output = OutputView{Name: "Xbox 360 virtual", MotionOnly: motionOnly}
	switch {
	case motionOnly:
		snap.Output.Name = "Somente movimento"
		snap.Output.Detail = "DSU desativado"
		if s.applied.CemuhookEnabled() {
			snap.Output.Detail = "DSU ativo"
		}
	case platform == "Windows":
		snap.Output.Detail = "Windows · ViGEmBus"
	default:
		snap.Output.Detail = platform + " · uinput"
	}
	snap.DSU = DSUView{
		Enabled: s.applied.CemuhookEnabled(), Running: s.dsu != nil, Error: s.dsuErr,
		Address: net.JoinHostPort(s.applied.CemuhookHost, strconv.Itoa(s.applied.CemuhookPort)),
	}
	if s.dsu != nil {
		snap.DSU.Clients = s.dsu.Clients()
	}
	if s.app != nil {
		for _, p := range s.app.Players() {
			v := PlayerView{Player: p.Number}
			for _, m := range p.Members {
				v.Members = append(v.Members, MemberView{Addr: m.Addr, Name: m.Name, Kind: kindName(m.Kind), Battery: m.Battery().String(), Volts: m.Volts})
			}
			v.Name = p.Members[0].Name
			if len(p.Members) == 2 {
				v.Name = "Joy-Con 2 · par unido"
			}
			snap.Players = append(snap.Players, v)
		}
	}
	return snap
}

// Check is one system diagnostic.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Help   string `json:"help,omitempty"` // bluetooth, output, mac
}

// Diagnostics checks Bluetooth and the virtual output prerequisites.
func (s *Service) Diagnostics() []Check {
	s.mu.Lock()
	fault, radio, hostMAC := s.fault, s.radio, s.saved.HostMAC
	s.mu.Unlock()
	bt := Check{Name: "Bluetooth", OK: radio != nil, Detail: "ativo", Help: "bluetooth"}
	if fault != nil && fault.Kind == "bluetooth" {
		bt.Detail = "indisponível"
	} else if radio == nil {
		bt.OK, bt.Detail = true, "não iniciado"
	}
	checks := []Check{bt}
	for _, c := range virtualpad.Diagnose() {
		checks = append(checks, Check{Name: c.Name, OK: c.OK, Detail: c.Detail, Help: c.Help})
	}
	if Platform() == "macOS" {
		checks = append(checks,
			Check{Name: "Movimento DSU", OK: true, Detail: "compatível"},
			Check{Name: "Endereço Bluetooth do PC", OK: hostMAC != "", Detail: map[bool]string{true: "informado", false: "não informado"}[hostMAC != ""], Help: "mac"})
	}
	return checks
}

// Watch streams input samples for a player (see app.App.Watch).
func (s *Service) Watch(player int) (<-chan app.Sample, func(), error) {
	a := s.currentApp()
	if a == nil {
		return nil, nil, app.ErrNoPlayer
	}
	return a.Watch(player)
}

// TestRumble vibrates a player's controllers briefly.
func (s *Service) TestRumble(player int) error {
	a := s.currentApp()
	if a == nil {
		return app.ErrNoPlayer
	}
	return a.TestRumble(player, 400*time.Millisecond)
}

// Disconnect disconnects a player's controllers.
func (s *Service) Disconnect(player int) error {
	a := s.currentApp()
	if a == nil {
		return app.ErrNoPlayer
	}
	return a.Disconnect(player)
}

func (s *Service) currentApp() *app.App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app
}
