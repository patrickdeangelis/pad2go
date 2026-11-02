package service

import (
	"runtime"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

// The DualSense driver check is listed for every default that can produce a
// virtual pad, so the UI shows a missing WinUHid even when the default is Xbox
// 360 (players may still pick DualSense). Motion-only and macOS list nothing.
func TestDiagnosticsListsDualSenseDriverForVirtualDefaults(t *testing.T) {
	for _, def := range []string{"auto", "xbox", "dualsense", "none"} {
		t.Run(def, func(t *testing.T) {
			s := build(t, Options{OpenBackend: func(kind string) (virtualpad.Backend, error) {
				if kind == virtualpad.OutputDualSense {
					return nil, errNoUHID
				}
				return &virtualpad.Recorder{}, nil
			}})
			if err := s.SetDefaultOutput(def, false); err != nil {
				t.Fatal(err)
			}
			var found *Check
			for _, c := range s.Diagnostics() {
				if c.Name == "DualSense virtual" {
					c := c
					found = &c
				}
			}
			if runtime.GOOS == "darwin" || def == "none" {
				if found != nil {
					t.Fatalf("%s default lists a DualSense virtual check: %+v", def, *found)
				}
				return
			}
			want := "driver WinUHid não encontrado"
			if runtime.GOOS != "windows" {
				want = "/dev/uhid indisponível"
			}
			if found == nil || found.OK || found.Detail != want || found.Help != "output" {
				t.Fatalf("default %s: DualSense check %+v", def, found)
			}
		})
	}
}
