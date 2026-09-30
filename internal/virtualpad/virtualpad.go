// Package virtualpad exposes virtual Xbox 360 controllers to the OS:
// ViGEmBus on Windows, uinput on Linux, and a no-op backend elsewhere.
package virtualpad

import (
	"fmt"
	"sync"

	"github.com/angelispatrick/pad2go/internal/mapping"
)

// RumbleFunc receives force-feedback from games: large (low-frequency) and
// small (high-frequency) motor strengths, 0-255.
type RumbleFunc func(large, small uint8)

// Pad is one virtual Xbox 360 controller.
type Pad interface {
	Update(mapping.XboxState) error
	Close() error
}

// Backend creates virtual pads.
type Backend interface {
	Name() string
	NewPad(onRumble RumbleFunc) (Pad, error)
	Close() error
}

// Open returns the backend named by kind ("auto", "vigem", "uinput", "none").
func Open(kind string) (Backend, error) {
	switch kind {
	case "none":
		return Null{}, nil
	case "auto":
		if platformName == "" {
			// No virtual gamepads here (macOS): run motion-only (DSU).
			return Null{}, nil
		}
		b, err := openPlatform()
		if err != nil {
			return nil, fmt.Errorf("virtual gamepad output unavailable (%w); set output: none to run motion-only", err)
		}
		return b, nil
	case "vigem", "uinput":
		if platformName != kind {
			return nil, fmt.Errorf("output %q is not available on this platform", kind)
		}
		return openPlatform()
	}
	return nil, fmt.Errorf("unknown output %q", kind)
}

// Null discards all output; useful with the DSU server or for debugging.
type Null struct{}

func (Null) Name() string                   { return "none" }
func (Null) NewPad(RumbleFunc) (Pad, error) { return nullPad{}, nil }
func (Null) Close() error                   { return nil }

type nullPad struct{}

func (nullPad) Update(mapping.XboxState) error { return nil }
func (nullPad) Close() error                   { return nil }

// Recorder is an in-memory backend for tests.
type Recorder struct {
	mu   sync.Mutex
	Pads []*RecordedPad
}

// RecordedPad stores the last state and exposes its rumble callback.
type RecordedPad struct {
	mu      sync.Mutex
	Last    mapping.XboxState
	Updates int
	Closed  bool
	Rumble  RumbleFunc
}

func (r *Recorder) Name() string { return "recorder" }
func (r *Recorder) Close() error { return nil }
func (r *Recorder) NewPad(fn RumbleFunc) (Pad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := &RecordedPad{Rumble: fn}
	r.Pads = append(r.Pads, p)
	return p, nil
}

// Snapshot returns the pads created so far.
func (r *Recorder) Snapshot() []*RecordedPad {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*RecordedPad(nil), r.Pads...)
}

func (p *RecordedPad) Update(s mapping.XboxState) error {
	p.mu.Lock()
	p.Last, p.Updates = s, p.Updates+1
	p.mu.Unlock()
	return nil
}

func (p *RecordedPad) Close() error {
	p.mu.Lock()
	p.Closed = true
	p.mu.Unlock()
	return nil
}

// State returns the last state and closed flag.
func (p *RecordedPad) State() (mapping.XboxState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Last, p.Closed
}

// Check is one system prerequisite for the virtual gamepad output.
type Check struct {
	Name   string
	OK     bool
	Detail string
	// Help is a stable key the UI uses to pick instructions ("output").
	Help string
}

// Diagnose checks this platform's virtual gamepad prerequisites. It returns
// nothing where no virtual output exists.
func Diagnose() []Check { return diagnose() }
