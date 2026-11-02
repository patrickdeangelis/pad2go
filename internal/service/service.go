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
	"maps"
	"net"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/hid"
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
	HID   hid.Backend
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

	mu              sync.Mutex
	saved           *config.Config // on disk
	applied         *config.Config // running
	radio           lifecycle.Radio
	app             *app.App
	dsu             *dsu.Server
	backend         string // "none" for motion-only output
	defaultOutput   string
	outputAvailable map[string]bool
	outputErrors    map[string]error
	fault           *Fault
	dsuErr          string
	discovery       Discovery
	scanStage       lifecycle.Stage // latest Scanning / Full
	restarting      bool
	cancelRun       context.CancelFunc
	players         []app.Player // last snapshot, for connect/disconnect toasts

	subMu sync.Mutex
	subs  map[chan Event]struct{}
}

// Fault is a condition that stops controllers from working.
type Fault struct {
	Kind    string   `json:"kind"` // "bluetooth", "output" or "config"
	Message string   `json:"message"`
	Reasons []string `json:"reasons,omitempty"`
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

func outputKind(kind string) string {
	switch kind {
	case "vigem", "uinput", "xbox":
		return virtualpad.OutputXbox
	case virtualpad.OutputDualSense:
		return virtualpad.OutputDualSense
	case virtualpad.OutputSwitch:
		return virtualpad.OutputSwitch
	case virtualpad.OutputNone:
		return virtualpad.OutputNone
	default:
		return virtualpad.OutputAuto
	}
}

func checkDefaultOutput(pool *virtualpad.Pool, kind string) error {
	switch kind {
	case virtualpad.OutputNone:
		return nil
	case virtualpad.OutputXbox:
		return pool.Available(virtualpad.OutputXbox)
	case virtualpad.OutputDualSense:
		return pool.Available(virtualpad.OutputDualSense)
	case virtualpad.OutputSwitch:
		return pool.Available(virtualpad.OutputSwitch)
	default:
		xboxErr := pool.Available(virtualpad.OutputXbox)
		dsErr := pool.Available(virtualpad.OutputDualSense)
		if xboxErr == nil || dsErr == nil {
			return nil
		}
		return fmt.Errorf("no virtual output available: Xbox 360: %w; DualSense/Switch: %w", xboxErr, dsErr)
	}
}

// outputReasons is one Portuguese line per backend the default output needs
// and cannot open, Xbox first. goos is runtime.GOOS (a parameter for tests).
func outputReasons(kind string, errs map[string]error, goos string) []string {
	var out []string
	add := func(k, msg string) {
		if errs[k] != nil {
			out = append(out, msg)
		}
	}
	if kind == virtualpad.OutputAuto || kind == virtualpad.OutputXbox {
		if errors.Is(errs[virtualpad.OutputXbox], virtualpad.ErrNoCgo) {
			add(virtualpad.OutputXbox, "Xbox 360: este build não inclui o ViGEm (precisa de um build com cgo e do ViGEmBus)")
		} else if goos == "windows" {
			add(virtualpad.OutputXbox, "Xbox 360: ViGEmBus não encontrado")
		} else {
			add(virtualpad.OutputXbox, "Xbox 360: /dev/uinput indisponível")
		}
	}
	if kind == virtualpad.OutputAuto || kind == virtualpad.OutputDualSense || kind == virtualpad.OutputSwitch {
		hid := virtualpad.OutputDualSense
		if kind == virtualpad.OutputSwitch {
			hid = virtualpad.OutputSwitch
		}
		if goos == "windows" {
			add(hid, "DualSense/Switch Pro: driver WinUHid não encontrado")
		} else {
			add(hid, "DualSense/Switch Pro: /dev/uhid indisponível")
		}
	}
	return out
}

// outputIssue names why a player's requested output is unavailable, or ""
// when it is available or motion-only. goos is runtime.GOOS.
func outputIssue(requested string, errs map[string]error, goos string) string {
	err := errs[requested]
	if err == nil {
		return ""
	}
	switch requested {
	case virtualpad.OutputDualSense:
		return "DualSense indisponível · WinUHid não encontrado"
	case virtualpad.OutputSwitch:
		return "Switch Pro indisponível · WinUHid não encontrado"
	case virtualpad.OutputXbox:
		switch {
		case errors.Is(err, virtualpad.ErrNoCgo):
			return "Xbox 360 indisponível · build sem ViGEm"
		case goos == "linux":
			return "Xbox 360 indisponível · uinput não encontrado"
		default:
			return "Xbox 360 indisponível · ViGEmBus não encontrado"
		}
	}
	return ""
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

	open := s.opt.OpenBackend
	if open == nil {
		open = virtualpad.Open
	}
	pool := virtualpad.NewPool(open)
	defer pool.Close()
	errs := map[string]error{}
	for _, k := range []string{virtualpad.OutputXbox, virtualpad.OutputDualSense, virtualpad.OutputSwitch} {
		errs[k] = pool.Available(k)
	}
	available := map[string]bool{virtualpad.OutputXbox: errs[virtualpad.OutputXbox] == nil, virtualpad.OutputDualSense: errs[virtualpad.OutputDualSense] == nil, virtualpad.OutputSwitch: errs[virtualpad.OutputSwitch] == nil}
	defaultKind := outputKind(cfg.Output)
	if err := checkDefaultOutput(pool, defaultKind); err != nil {
		s.log.Error("virtual gamepad output unavailable; per-player output may be motion-only", "err", err)
		s.setFault(&Fault{Kind: "output", Message: err.Error(), Reasons: outputReasons(defaultKind, errs, runtime.GOOS)})
	}
	s.mu.Lock()
	s.defaultOutput, s.outputAvailable, s.outputErrors = defaultKind, available, errs
	s.mu.Unlock()

	var err error
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

	a := app.New(cfg, virtualpad.Null{}, server, s.log)
	a.SetPool(pool)
	a.SetOutputs(cfg.Output, cfg.OutputOverrides)
	a.SetOutputRemaps(cfg.OutputRemaps)
	a.OnChange(s.appChanged)
	defer a.Close()
	s.mu.Lock()
	s.app, s.dsu, s.backend = a, server, defaultKind
	s.mu.Unlock()

	hidBackend := s.opt.HID
	if hidBackend == nil {
		hidBackend = hid.DefaultBackend()
	}
	go (&hid.Scanner{Backend: hidBackend, App: a, Log: s.log, Deadzone: cfg.Deadzone("dualsense"), HasDeadzone: true}).Run(ctx)

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
		if errors.Is(e.Err, lifecycle.ErrDropped) {
			*d = Discovery{Step: 2, Text: msg + ": o controle desconectou no meio da conexão. Se ele estiver ligado por cabo USB, desconecte o cabo e segure SYNC de novo.", Failure: true, at: now}
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

// SetDefaultOutput changes the default virtual output. apply recreates the
// pads of players that follow the default; manual overrides are kept.
func (s *Service) SetDefaultOutput(kind string, apply bool) error {
	kind = outputKind(kind)
	s.mu.Lock()
	next := *s.saved
	next.OutputOverrides = maps.Clone(s.saved.OutputOverrides)
	a := s.app
	s.mu.Unlock()
	next.Output = kind
	if err := next.Validate(); err != nil {
		return err
	}
	if err := next.Save(s.opt.ConfigPath); err != nil {
		return err
	}
	if a != nil {
		if err := a.SetDefaultOutput(kind, apply); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.saved, s.applied = &next, &next
	s.defaultOutput, s.backend = kind, kind
	s.mu.Unlock()
	s.notify()
	return nil
}

// SetPlayerOutput sets or clears one player slot's output override and
// persists it by controller address.
func (s *Service) SetPlayerOutput(player int, kind string) error {
	switch kind {
	case virtualpad.OutputAuto, virtualpad.OutputXbox, virtualpad.OutputDualSense, virtualpad.OutputSwitch, virtualpad.OutputNone:
	default:
		return fmt.Errorf("unknown output %q", kind)
	}
	s.mu.Lock()
	a := s.app
	s.mu.Unlock()
	if a == nil {
		return app.ErrNoPlayer
	}
	addr, err := a.SetPlayerOutput(player, kind)
	if err != nil {
		return err
	}
	s.mu.Lock()
	next := *s.saved
	next.OutputOverrides = maps.Clone(s.saved.OutputOverrides)
	s.mu.Unlock()
	if next.OutputOverrides == nil {
		next.OutputOverrides = map[string]string{}
	}
	if kind == virtualpad.OutputAuto {
		delete(next.OutputOverrides, addr)
	} else {
		next.OutputOverrides[addr] = kind
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := next.Save(s.opt.ConfigPath); err != nil {
		return err
	}
	s.mu.Lock()
	s.saved, s.applied = &next, &next
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
	Name       string          `json:"name"`
	Detail     string          `json:"detail"`
	MotionOnly bool            `json:"motionOnly"`
	Default    string          `json:"default"`
	Mixed      bool            `json:"mixed"`
	Available  map[string]bool `json:"available,omitempty"`
}

// PlayerView is one occupied player slot.
type PlayerView struct {
	Player             int           `json:"player"`
	Name               string        `json:"name"`
	Output             string        `json:"output"`
	OutputKind         string        `json:"outputKind"`
	OutputOverride     string        `json:"outputOverride,omitempty"`
	OutputOverrideKind string        `json:"outputOverrideKind,omitempty"`
	Members            []MemberView  `json:"members"`
	OutputIssue        string        `json:"outputIssue"`
	DefaultOutput      string        `json:"defaultOutput"`
	ActiveRemaps       []ActiveRemap `json:"activeRemaps,omitempty"`
}

// ActiveRemap is one output remap running on a player, as protocol button names.
type ActiveRemap struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// MemberView is one controller.
type MemberView struct {
	Addr           string                   `json:"addr"`
	Name           string                   `json:"name"`
	Kind           string                   `json:"kind"` // left, right, pro, gc
	Transport      string                   `json:"transport"`
	Battery        string                   `json:"battery"` // "high", "medium", "low" or "" when unknown
	Volts          float64                  `json:"volts"`   // last reported pack voltage, 0 when unknown
	BatteryPercent *int                     `json:"batteryPercent,omitempty"`
	BatteryStatus  string                   `json:"batteryStatus,omitempty"`
	Diagnostics    *protocol.HIDDiagnostics `json:"diagnostics,omitempty"`
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
	case protocol.KindDualSense:
		return "ds"
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
	defaultKind := s.defaultOutput
	motionOnly := defaultKind == virtualpad.OutputNone || platform == "macOS"
	snap.Output = OutputView{
		Name: virtualpad.Name(defaultKind), Default: defaultKind, MotionOnly: motionOnly,
		Available: maps.Clone(s.outputAvailable),
	}
	switch defaultKind {
	case virtualpad.OutputDualSense:
		if platform == "Windows" {
			snap.Output.Detail = "DualSense · WinUHid"
		} else {
			snap.Output.Detail = "DualSense · /dev/uhid"
		}
	case virtualpad.OutputSwitch:
		if platform == "Windows" {
			snap.Output.Detail = "Switch Pro · WinUHid"
		} else {
			snap.Output.Detail = "Switch Pro · /dev/uhid"
		}
	case virtualpad.OutputXbox:
		snap.Output.Detail = "Xbox 360 · " + platform
	case virtualpad.OutputNone:
		snap.Output.Detail = "DSU desativado"
		if s.applied.CemuhookEnabled() {
			snap.Output.Detail = "DSU ativo"
		}
	default:
		snap.Output.Detail = "DualSense → DualSense · outros → Xbox 360"
	}
	if defaultKind != virtualpad.OutputNone && len(s.outputAvailable) > 0 &&
		!s.outputAvailable[virtualpad.OutputXbox] && !s.outputAvailable[virtualpad.OutputDualSense] {
		snap.Output.Detail = "Nenhuma saída virtual disponível"
		snap.Output.MotionOnly = true
	}
	snap.DSU = DSUView{
		Enabled: s.applied.CemuhookEnabled(), Running: s.dsu != nil, Error: s.dsuErr,
		Address: net.JoinHostPort(s.applied.CemuhookHost, strconv.Itoa(s.applied.CemuhookPort)),
	}
	if s.dsu != nil {
		snap.DSU.Clients = s.dsu.Clients()
	}
	if s.app != nil {
		outputs := map[string]bool{}
		for _, p := range s.app.Players() {
			v := PlayerView{Player: p.Number, Output: p.Output, OutputKind: p.OutputKind, OutputOverride: p.OutputOverride, OutputOverrideKind: p.OutputOverrideKind}
			outputs[p.Output] = true
			for _, m := range p.Members {
				view := MemberView{Addr: m.Addr, Name: m.Name, Kind: kindName(m.Kind), Transport: m.Transport, Battery: m.Battery().String(), Volts: m.Volts, Diagnostics: m.Diagnostics}
				if m.BatteryDetail != 0 {
					raw := byte(m.BatteryDetail - 1)
					switch raw >> 4 {
					case 0, 1, 2:
						percent := min(int(raw&15)*10+5, 100)
						if raw>>4 == 2 {
							percent = 100
						}
						view.BatteryPercent = &percent
						view.BatteryStatus = []string{"discharging", "charging", "full"}[raw>>4]
					case 10, 11, 15:
						view.BatteryStatus = "error"
					default:
						view.BatteryStatus = "unknown"
					}
				}
				v.Members = append(v.Members, view)
			}
			v.Name = p.Members[0].Name
			if len(p.Members) == 2 {
				v.Name = "Joy-Con 2 · par unido"
			}
			v.OutputIssue = outputIssue(p.Requested, s.outputErrors, runtime.GOOS)
			v.DefaultOutput = virtualpad.Name(p.DefaultResolved)
			for _, r := range p.ActiveRemaps {
				v.ActiveRemaps = append(v.ActiveRemaps, ActiveRemap{Source: r.Source, Target: r.Target})
			}
			snap.Players = append(snap.Players, v)
		}
		if len(outputs) > 1 {
			snap.Output.Mixed = true
		}
		if len(outputs) == 1 {
			if _, allNone := outputs[virtualpad.Name(virtualpad.OutputNone)]; allNone {
				snap.Output.Name = "Somente movimento"
				detail := "Os players não têm saída virtual"
				switch defaultKind {
				case virtualpad.OutputDualSense:
					if !s.outputAvailable[virtualpad.OutputDualSense] {
						detail = "DualSense indisponível; WinUHid não encontrado"
					}
				case virtualpad.OutputSwitch:
					if !s.outputAvailable[virtualpad.OutputSwitch] {
						detail = "Switch Pro indisponível; WinUHid não encontrado"
					}
				case virtualpad.OutputXbox:
					if !s.outputAvailable[virtualpad.OutputXbox] {
						detail = "Xbox 360 indisponível; ViGEmBus/uinput não encontrado"
					}
				}
				snap.Output.Detail = detail
				snap.Output.MotionOnly = true
			}
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

// diagnosticDetail is the Portuguese detail for a DualSense/Switch Pro output
// that failed to open; other errors keep their message.
func diagnosticDetail(err error, goos string) string {
	if errors.Is(err, virtualpad.ErrDriverMissing) {
		if goos == "windows" {
			return "driver WinUHid não encontrado"
		}
		return "/dev/uhid indisponível"
	}
	return err.Error()
}

// Diagnostics checks Bluetooth and the virtual output prerequisites.
func (s *Service) Diagnostics() []Check {
	s.mu.Lock()
	fault, radio, hostMAC, output := s.fault, s.radio, s.saved.HostMAC, s.saved.Output
	s.mu.Unlock()
	bt := Check{Name: "Bluetooth", OK: radio != nil, Detail: "ativo", Help: "bluetooth"}
	if fault != nil && fault.Kind == "bluetooth" {
		bt.Detail = "indisponível"
	} else if radio == nil {
		bt.OK, bt.Detail = true, "não iniciado"
	}
	checks := []Check{bt}
	open := s.opt.OpenBackend
	if open == nil {
		open = virtualpad.Open
	}
	hidCheck := func(kind string) Check {
		name := "DualSense virtual"
		if kind == virtualpad.OutputSwitch {
			name = "Switch Pro virtual"
		}
		check := Check{Name: name, OK: true, Detail: "disponível", Help: "output"}
		if backend, err := open(kind); err != nil {
			check.OK, check.Detail = false, diagnosticDetail(err, runtime.GOOS)
		} else {
			_ = backend.Close()
		}
		return check
	}
	switch kind := outputKind(output); kind {
	case virtualpad.OutputDualSense, virtualpad.OutputSwitch:
		checks = append(checks, hidCheck(kind))
	default:
		for _, c := range virtualpad.Diagnose() {
			checks = append(checks, Check{Name: c.Name, OK: c.OK, Detail: c.Detail, Help: c.Help})
		}
		// Any virtual pad output can end up needing the DualSense driver: the
		// default may be Automática (DualSense falls back to Xbox 360 when it
		// is missing) or a player may pick DualSense on its own.
		if kind != virtualpad.OutputNone && Platform() != "macOS" {
			checks = append(checks, hidCheck(virtualpad.OutputDualSense))
		}
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
