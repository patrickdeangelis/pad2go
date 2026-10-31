// Pool opens virtual-pad backends lazily by output kind. A slot may choose
// Xbox 360, DualSense or Switch Pro independently, so the app needs more than
// one backend alive at once.
package virtualpad

import (
	"fmt"
	"runtime"
	"sync"
)

// Output kinds used by the app and the UI. "auto" is resolved per slot.
const (
	OutputAuto      = "auto"
	OutputXbox      = "xbox"
	OutputDualSense = "dualsense"
	OutputSwitch    = "switch"
	OutputNone      = "none"
)

// Pool caches one backend per platform output key.
type Pool struct {
	open func(string) (Backend, error)

	mu       sync.Mutex
	backends map[string]Backend
	closed   bool
}

// NewPool creates a pool. open receives the platform key ("vigem", "uinput",
// "dualsense", "switch" or "none").
func NewPool(open func(string) (Backend, error)) *Pool {
	return &Pool{open: open, backends: map[string]Backend{}}
}

func backendKey(kind string) (string, error) {
	switch kind {
	case OutputXbox:
		switch runtime.GOOS {
		case "windows":
			return "vigem", nil
		case "linux":
			return "uinput", nil
		default:
			return "", fmt.Errorf("Xbox 360 output is not available on %s", runtime.GOOS)
		}
	case OutputDualSense:
		return "dualsense", nil
	case OutputSwitch:
		return "switch", nil
	case OutputNone:
		return "none", nil
	default:
		return "", fmt.Errorf("unknown output %q", kind)
	}
}

func (p *Pool) backend(kind string) (Backend, error) {
	key, err := backendKey(kind)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("output pool is closed")
	}
	if b, ok := p.backends[key]; ok {
		return b, nil
	}
	b, err := p.open(key)
	if err != nil {
		return nil, err
	}
	p.backends[key] = b
	return b, nil
}

// Pad creates a pad of kind. The caller owns the returned Pad.
func (p *Pool) Pad(kind string, onRumble RumbleFunc) (Pad, error) {
	b, err := p.backend(kind)
	if err != nil {
		return nil, err
	}
	return b.NewPad(onRumble)
}

// Available reports whether kind can be opened.
func (p *Pool) Available(kind string) error {
	_, err := p.backend(kind)
	return err
}

// Name is the UI label for kind.
func Name(kind string) string {
	switch kind {
	case OutputXbox:
		return "Xbox 360"
	case OutputDualSense:
		return "DualSense"
	case OutputSwitch:
		return "Switch Pro"
	case OutputNone:
		return "Somente movimento"
	default:
		return "Automático"
	}
}

// Close closes every opened backend.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	backends := make([]Backend, 0, len(p.backends))
	for _, b := range p.backends {
		backends = append(backends, b)
	}
	p.backends = map[string]Backend{}
	p.mu.Unlock()
	var first error
	for _, b := range backends {
		if err := b.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
