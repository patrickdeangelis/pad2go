package service

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/hid"
	"github.com/patrickdeangelis/pad2go/internal/lifecycle"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

var (
	errNoCgo   = fmt.Errorf("%w: rebuild with cgo", virtualpad.ErrNoCgo)
	errNoUHID  = fmt.Errorf("DualSense virtual output unavailable: %w: WinUHid: not found", virtualpad.ErrDriverMissing)
	errNoVigem = errors.New("ViGEmBus: device not found")
)

func TestOutputReasons(t *testing.T) {
	both := map[string]error{virtualpad.OutputXbox: errNoCgo, virtualpad.OutputDualSense: errNoUHID, virtualpad.OutputSwitch: errNoUHID}
	tests := []struct {
		name string
		kind string
		errs map[string]error
		goos string
		want []string
	}{
		{"auto, both missing: Xbox first", virtualpad.OutputAuto, both, "windows", []string{
			"Xbox 360: este build não inclui o ViGEm (precisa de um build com cgo e do ViGEmBus)",
			"DualSense/Switch Pro: driver WinUHid não encontrado",
		}},
		{"dualsense default names only WinUHid", virtualpad.OutputDualSense, both, "windows", []string{"DualSense/Switch Pro: driver WinUHid não encontrado"}},
		{"switch default uses the Switch error", virtualpad.OutputSwitch, map[string]error{virtualpad.OutputSwitch: errNoUHID}, "windows", []string{"DualSense/Switch Pro: driver WinUHid não encontrado"}},
		{"xbox default, driver missing", virtualpad.OutputXbox, map[string]error{virtualpad.OutputXbox: errNoVigem}, "windows", []string{"Xbox 360: ViGEmBus não encontrado"}},
		{"linux names device nodes", virtualpad.OutputAuto, map[string]error{virtualpad.OutputXbox: errNoVigem, virtualpad.OutputDualSense: errNoUHID}, "linux", []string{
			"Xbox 360: /dev/uinput indisponível",
			"DualSense/Switch Pro: /dev/uhid indisponível",
		}},
		{"available backend adds no line", virtualpad.OutputAuto, map[string]error{virtualpad.OutputDualSense: errNoUHID}, "windows", []string{"DualSense/Switch Pro: driver WinUHid não encontrado"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outputReasons(tt.kind, tt.errs, tt.goos); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("reasons %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutputIssue(t *testing.T) {
	errs := map[string]error{virtualpad.OutputDualSense: errNoUHID, virtualpad.OutputSwitch: errNoUHID, virtualpad.OutputXbox: errNoCgo}
	tests := []struct {
		requested string
		errs      map[string]error
		goos      string
		want      string
	}{
		{virtualpad.OutputDualSense, errs, "windows", "DualSense indisponível · WinUHid não encontrado"},
		{virtualpad.OutputSwitch, errs, "windows", "Switch Pro indisponível · WinUHid não encontrado"},
		{virtualpad.OutputXbox, errs, "windows", "Xbox 360 indisponível · build sem ViGEm"},
		{virtualpad.OutputXbox, map[string]error{virtualpad.OutputXbox: errNoVigem}, "windows", "Xbox 360 indisponível · ViGEmBus não encontrado"},
		{virtualpad.OutputXbox, map[string]error{virtualpad.OutputXbox: errNoVigem}, "linux", "Xbox 360 indisponível · uinput não encontrado"},
		{virtualpad.OutputNone, errs, "windows", ""},
		{virtualpad.OutputDualSense, map[string]error{virtualpad.OutputXbox: errNoCgo}, "windows", ""},
	}
	for _, tt := range tests {
		if got := outputIssue(tt.requested, tt.errs, tt.goos); got != tt.want {
			t.Errorf("outputIssue(%s, %s) = %q, want %q", tt.requested, tt.goos, got, tt.want)
		}
	}
}

func TestDiagnosticDetail(t *testing.T) {
	if got := diagnosticDetail(errNoUHID, "windows"); got != "driver WinUHid não encontrado" {
		t.Fatalf("windows detail %q", got)
	}
	if got := diagnosticDetail(errNoUHID, "linux"); got != "/dev/uhid indisponível" {
		t.Fatalf("linux detail %q", got)
	}
	if got := diagnosticDetail(errNoVigem, "windows"); got != errNoVigem.Error() {
		t.Fatalf("other error detail %q", got)
	}
}

func dualSenseHID() *hidTestBackend {
	return &hidTestBackend{infos: []hid.Info{{Path: "mac", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}}}
}

func TestSnapshotNamesMissingOutput(t *testing.T) {
	s := start(t, Options{
		HID: dualSenseHID(),
		OpenBackend: func(kind string) (virtualpad.Backend, error) {
			if kind == virtualpad.OutputDualSense || kind == virtualpad.OutputSwitch {
				return nil, errNoUHID
			}
			return nil, errNoCgo
		},
		Radio: func() (lifecycle.Radio, error) { return &radio{}, nil },
	})
	eventually(t, "DualSense and Pro Controller attached", func() bool { return len(s.Snapshot().Players) == 2 })
	v := s.Snapshot()
	want := []string{
		"Xbox 360: este build não inclui o ViGEm (precisa de um build com cgo e do ViGEmBus)",
		"DualSense/Switch Pro: driver WinUHid não encontrado",
	}
	if runtime.GOOS != "windows" {
		want[1] = "DualSense/Switch Pro: /dev/uhid indisponível"
	}
	if v.Fault == nil || v.Fault.Kind != "output" || !reflect.DeepEqual(v.Fault.Reasons, want) {
		t.Fatalf("fault %+v", v.Fault)
	}
	for _, p := range v.Players {
		switch p.Members[0].Kind {
		case "ds":
			if p.OutputIssue != "DualSense indisponível · WinUHid não encontrado" || p.DefaultOutput != "DualSense" {
				t.Fatalf("DualSense issue %q default %q", p.OutputIssue, p.DefaultOutput)
			}
		default:
			if p.OutputIssue != "Xbox 360 indisponível · build sem ViGEm" || p.DefaultOutput != "Xbox 360" {
				t.Fatalf("Pro Controller issue %q default %q", p.OutputIssue, p.DefaultOutput)
			}
		}
	}
}

func TestSnapshotDefaultOutputAndNoIssue(t *testing.T) {
	s := start(t, Options{HID: dualSenseHID(), Radio: func() (lifecycle.Radio, error) { return nil, errors.New("adapter off") }})
	eventually(t, "player attached", func() bool { return len(s.Snapshot().Players) == 1 })
	if p := s.Snapshot().Players[0]; p.OutputIssue != "" || p.DefaultOutput != "DualSense" {
		t.Fatalf("auto DualSense: issue %q default %q", p.OutputIssue, p.DefaultOutput)
	}
	if err := s.SetPlayerOutput(1, "xbox"); err != nil {
		t.Fatal(err)
	}
	// An override does not change what "(padrão)" names.
	if p := s.Snapshot().Players[0]; p.DefaultOutput != "DualSense" {
		t.Fatalf("override changed default name to %q", p.DefaultOutput)
	}
	if err := s.SetDefaultOutput("switch", false); err != nil {
		t.Fatal(err)
	}
	if p := s.Snapshot().Players[0]; p.DefaultOutput != "Switch Pro" {
		t.Fatalf("switch default name %q", p.DefaultOutput)
	}
	if err := s.SetDefaultOutput("none", false); err != nil {
		t.Fatal(err)
	}
	if p := s.Snapshot().Players[0]; p.DefaultOutput != "Somente movimento" {
		t.Fatalf("none default name %q", p.DefaultOutput)
	}
}

func TestActiveRemapsAreTheRunningOnes(t *testing.T) {
	s := start(t, Options{HID: dualSenseHID(), Radio: func() (lifecycle.Radio, error) { return nil, errors.New("adapter off") }})
	eventually(t, "player attached", func() bool { return len(s.Snapshot().Players) == 1 })
	set := s.Snapshot().Config
	set.OutputRemaps = map[string]map[string]string{"global": {"B": "X"}}
	if err := s.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	// Saved but not restarted: the running remaps are still none.
	if got := s.Snapshot().Players[0].ActiveRemaps; len(got) != 0 {
		t.Fatalf("pending remap reported as active: %+v", got)
	}
	s.Restart()
	want := []ActiveRemap{{Source: "B", Target: "X"}}
	eventually(t, "global remap active", func() bool {
		v := s.Snapshot()
		return len(v.Players) == 1 && reflect.DeepEqual(v.Players[0].ActiveRemaps, want)
	})
	// The DualSense scope overrides the global entry for the same source.
	set = s.Snapshot().Config
	set.OutputRemaps = map[string]map[string]string{"global": {"B": "X", "HOME": "None"}, "dualsense": {"B": "Y"}}
	if err := s.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	s.Restart()
	want = []ActiveRemap{{Source: "B", Target: "Y"}, {Source: "HOME", Target: "None"}}
	eventually(t, "per-output remap active", func() bool {
		v := s.Snapshot()
		return len(v.Players) == 1 && reflect.DeepEqual(v.Players[0].ActiveRemaps, want)
	})
}
