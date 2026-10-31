package virtualpad

import (
	"errors"
	"runtime"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
)

type poolTestBackend struct {
	name   string
	pads   int
	closed bool
}

func (b *poolTestBackend) Name() string { return b.name }
func (b *poolTestBackend) NewPad(RumbleFunc) (Pad, error) {
	b.pads++
	return poolTestPad{}, nil
}
func (b *poolTestBackend) Close() error {
	b.closed = true
	return nil
}

type poolTestPad struct{}

func (poolTestPad) Update(mapping.XboxState) error { return nil }
func (poolTestPad) Close() error                   { return nil }

func xboxKey() string {
	if runtime.GOOS == "windows" {
		return "vigem"
	}
	return "uinput"
}

func TestPoolCachesBackendAndNames(t *testing.T) {
	rec := &poolTestBackend{name: "xbox"}
	opened := []string{}
	pool := NewPool(func(kind string) (Backend, error) {
		opened = append(opened, kind)
		return rec, nil
	})
	for i := 0; i < 2; i++ {
		pad, err := pool.Pad(OutputXbox, nil)
		if err != nil || pad == nil {
			t.Fatalf("Pad %d: %v", i, err)
		}
	}
	if len(opened) != 1 || opened[0] != xboxKey() {
		t.Fatalf("opened %v, want one %q", opened, xboxKey())
	}
	if rec.pads != 2 {
		t.Fatalf("pads %d, want 2", rec.pads)
	}
	if got := Name(OutputXbox); got != "Xbox 360" {
		t.Fatalf("Name xbox = %q", got)
	}
	if got := Name(OutputDualSense); got != "DualSense" {
		t.Fatalf("Name dualsense = %q", got)
	}
	if got := Name(OutputSwitch); got != "Switch Pro" {
		t.Fatalf("Name switch = %q", got)
	}
	if got := Name(OutputNone); got != "Somente movimento" {
		t.Fatalf("Name none = %q", got)
	}
}

func TestPoolErrorsAndClose(t *testing.T) {
	if _, err := NewPool(func(string) (Backend, error) { return nil, errors.New("boom") }).Pad(OutputDualSense, nil); err == nil {
		t.Fatal("Pad with a failing opener returned nil error")
	}
	if _, err := NewPool(func(string) (Backend, error) { return &poolTestBackend{}, nil }).Pad("bogus", nil); err == nil {
		t.Fatal("unknown kind was accepted")
	}
	rec := &poolTestBackend{}
	pool := NewPool(func(string) (Backend, error) { return rec, nil })
	if err := pool.Available(OutputDualSense); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if !rec.closed {
		t.Fatal("Close did not close the backend")
	}
	if _, err := pool.Pad(OutputDualSense, nil); err == nil {
		t.Fatal("Pad after Close returned nil error")
	}
}
