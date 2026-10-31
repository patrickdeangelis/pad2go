package main

import (
	"bytes"
	"fmt"
	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/hid"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type dumpBackend struct {
	infos []hid.Info
	tr    *dumpTransport
}

func (b *dumpBackend) List() ([]hid.Info, error)                  { return b.infos, nil }
func (b *dumpBackend) Open(hid.Info) (dualsense.Transport, error) { return b.tr, nil }

type dumpTransport struct{}

func (dumpTransport) Read(b []byte) (int, error) { copy(b, []byte{1, 2, 3}); return 3, nil }
func (dumpTransport) Write([]byte) error         { return nil }
func (dumpTransport) Close() error               { return nil }
func TestDumpHidWritesReports(t *testing.T) {
	old := dumpHIDDuration
	dumpHIDDuration = 10 * time.Millisecond
	t.Cleanup(func() { dumpHIDDuration = old })
	p := filepath.Join(t.TempDir(), "capture.hex")
	var out, stderr bytes.Buffer
	b := &dumpBackend{infos: []hid.Info{{Path: "pad", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}}, tr: &dumpTransport{}}
	if code := dumpHID([]string{p}, b, &out, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	line := regexp.MustCompile(`^\d+ 010203$`)
	for _, l := range lines {
		if !line.MatchString(l) {
			t.Fatalf("line %q is not `<ms> <hex>`", l)
		}
	}
	if want := fmt.Sprintf("recorded %d reports to %s\n", len(lines), p); out.String() != want {
		t.Fatalf("output=%q want %q", out.String(), want)
	}
}
func TestDumpHidNoDevice(t *testing.T) {
	var out, stderr bytes.Buffer
	b := &dumpBackend{}
	if code := dumpHID(nil, b, &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "no DualSense found") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
