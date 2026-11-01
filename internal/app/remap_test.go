package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

func TestCompileOutputRemaps(t *testing.T) {
	remaps := compileOutputRemaps(
		map[string]string{"A": "B", "HOME": "None", "CAPT": "HOME"},
		map[string]string{"A": "None", "CAPT": "Default"},
	)
	// Per-output A=None overrides the global A=B, and CAPT=Default leaves the
	// inherited CAPT=HOME. HOME=None clears Home even when CAPT maps onto it.
	got := applyButtonRemaps(protocol.BtnA|protocol.BtnHome|protocol.BtnCapture, remaps)
	if got != protocol.BtnHome {
		t.Fatalf("buttons %v, want [HOME]", protocol.ButtonNames(got))
	}
}

func TestCompileOutputRemapsEmpty(t *testing.T) {
	if got := compileOutputRemaps(nil, nil); got != nil {
		t.Fatalf("empty remaps compiled to %+v", got)
	}
}

func TestOutputRemapAppliedToXbox(t *testing.T) {
	a, recs := outputPoolApp(t, "xbox", nil)
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{"global": {"B": "A"}})
	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	// Switch B is the bottom button and maps to Xbox A by position. The remap
	// turns it into protocol A first, which maps to Xbox B.
	d.send(centered(protocol.BtnB))
	pads := xboxRecorder(recs).Snapshot()
	if len(pads) != 1 {
		t.Fatalf("%d pads", len(pads))
	}
	if st, _ := pads[0].State(); st.Buttons != mapping.XBB {
		t.Fatalf("buttons %04x, want %04x", st.Buttons, mapping.XBB)
	}
}

func TestOutputRemapToTrigger(t *testing.T) {
	a, recs := outputPoolApp(t, "xbox", nil)
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{"global": {"B": "ZL"}})
	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnB))
	st, _ := xboxRecorder(recs).Snapshot()[0].State()
	if st.Buttons != 0 || st.LeftTrigger != 255 {
		t.Fatalf("state %+v", st)
	}
}

func TestOutputRemapPerOutputOverridesGlobal(t *testing.T) {
	a, recs := outputPoolApp(t, "xbox", nil)
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{
		"global": {"B": "A"},
		"xbox":   {"B": "None"},
	})
	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnB))
	if st, _ := xboxRecorder(recs).Snapshot()[0].State(); st.Buttons != 0 {
		t.Fatalf("buttons %04x, want none", st.Buttons)
	}
}

func TestOutputRemapFollowsEffectiveOutput(t *testing.T) {
	a, recs := outputPoolApp(t, "auto", nil)
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{
		"dualsense": {"B": "X"},
	})
	pro := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), pro); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetPlayerOutput(1, "dualsense"); err != nil {
		t.Fatal(err)
	}
	pro.send(centered(protocol.BtnB))
	if st, _ := recs["dualsense"].Snapshot()[0].State(); st.Buttons != mapping.XBY {
		t.Fatalf("buttons %04x, want %04x", st.Buttons, mapping.XBY)
	}
}

func TestOutputRemapFramePadUsesSelectedOutput(t *testing.T) {
	backend := &frameBackend{}
	cfg := config.Default()
	cfg.Output = "dualsense"
	a := New(cfg, virtualpad.Null{}, nil, nil)
	a.SetPool(virtualpad.NewPool(func(string) (virtualpad.Backend, error) { return backend, nil }))
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{
		"dualsense": {"B": "X"},
		"switch":    {"B": "A"},
	})

	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnB))
	backend.pad.mu.Lock()
	f := backend.pad.frames[len(backend.pad.frames)-1]
	backend.pad.mu.Unlock()
	if f.Player.Buttons != protocol.BtnX || f.Xbox.Buttons != mapping.XBY {
		t.Fatalf("dualsense frame player=%v xbox=%04x", protocol.ButtonNames(f.Player.Buttons), f.Xbox.Buttons)
	}

	if _, err := a.SetPlayerOutput(1, "switch"); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnB))
	backend.pad.mu.Lock()
	f = backend.pad.frames[len(backend.pad.frames)-1]
	backend.pad.mu.Unlock()
	if f.Player.Buttons != protocol.BtnA || f.Xbox.Buttons != mapping.XBB {
		t.Fatalf("switch frame player=%v xbox=%04x", protocol.ButtonNames(f.Player.Buttons), f.Xbox.Buttons)
	}
}

func TestOutputRemapUsesRequestedOutputWithoutBackend(t *testing.T) {
	cfg := config.Default()
	cfg.Output = "auto"
	a := New(cfg, virtualpad.Null{}, nil, nil)
	a.SetPool(virtualpad.NewPool(func(string) (virtualpad.Backend, error) {
		return nil, errors.New("no driver")
	}))
	defer a.Close()
	// Auto on a DualSense asks for the DualSense output even when the backend
	// is missing, so the remap still reaches the input test and DSU.
	a.SetOutputRemaps(map[string]map[string]string{"dualsense": {"B": "X", "HOME": "CAPT"}})
	d := newDev("ds", 0)
	d.kind = protocol.KindDualSense
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	samples, cancel, err := a.Watch(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	d.send(centered(protocol.BtnB | protocol.BtnHome))
	select {
	case sample := <-samples:
		if sample.Xbox.Buttons != mapping.XBY|mapping.XBBack {
			t.Fatalf("buttons %04x, want %04x", sample.Xbox.Buttons, mapping.XBY|mapping.XBBack)
		}
	case <-time.After(time.Second):
		t.Fatal("no sample")
	}
}

func TestOutputRemapSkippedWithoutVirtualOutput(t *testing.T) {
	a, recs := outputPoolApp(t, "none", nil)
	defer a.Close()
	a.SetOutputRemaps(map[string]map[string]string{"global": {"B": "A"}})
	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnB))
	if st, _ := recs["none"].Snapshot()[0].State(); st.Buttons != mapping.XBA {
		t.Fatalf("buttons %04x, want %04x", st.Buttons, mapping.XBA)
	}
}

func TestOutputRemapKeepsDefaultMapping(t *testing.T) {
	a, recs := outputPoolApp(t, "xbox", nil)
	defer a.Close()
	d := newDev("pro", protocol.ProController2PID)
	if err := a.AddDevice(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	d.send(centered(protocol.BtnA | protocol.BtnZL))
	st, _ := xboxRecorder(recs).Snapshot()[0].State()
	if st.Buttons != mapping.XBB || st.LeftTrigger != 255 {
		t.Fatalf("state %+v", st)
	}
}
