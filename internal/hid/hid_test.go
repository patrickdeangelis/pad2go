package hid

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

type testBackend struct {
	mu    sync.Mutex
	infos []Info
	opens int
	fail  int
	last  *testTransport
	macs  map[string]string
}

func (b *testBackend) List() ([]Info, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Info(nil), b.infos...), nil
}
func (b *testBackend) Open(info Info) (dualsense.Transport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.opens++
	if b.fail > 0 {
		b.fail--
		return nil, errors.New("busy")
	}
	tr := &testTransport{closed: make(chan struct{}), mac: b.macs[info.Path]}
	b.last = tr
	return tr, nil
}

type testTransport struct {
	once   sync.Once
	closed chan struct{}
	mac    string
}

func (t *testTransport) MAC() string { return t.mac }

func (t *testTransport) Read(b []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, io.EOF
	case <-time.After(time.Millisecond):
	}
	for i := range b {
		b[i] = 0
	}
	if len(b) > 0 {
		b[0] = 1
	}
	return 64, nil
}
func (t *testTransport) Write([]byte) error { return nil }
func (t *testTransport) Close() error       { t.once.Do(func() { close(t.closed) }); return nil }

type testApp struct {
	mu      sync.Mutex
	devices map[string]app.Device
	adds    int
	full    bool
}

func (a *testApp) AddDevice(_ context.Context, d app.Device) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.full {
		return app.ErrFull
	}
	a.adds++
	a.devices[d.Address()] = d
	return nil
}
func (a *testApp) Full() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.full }
func (a *testApp) AddCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.adds
}
func (a *testApp) Connected(s string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.devices[s]
	return ok
}
func (a *testApp) RemoveDevice(s string) { a.mu.Lock(); defer a.mu.Unlock(); delete(a.devices, s) }
func (a *testApp) Devices() []app.Device {
	a.mu.Lock()
	defer a.mu.Unlock()
	devices := make([]app.Device, 0, len(a.devices))
	for _, d := range a.devices {
		devices = append(devices, d)
	}
	return devices
}
func spinScan(t *testing.T, s *Scanner, n int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(time.Duration(n)*s.Interval + 30*time.Millisecond)
	cancel()
	<-done
}
func TestVirtualMACFilter(t *testing.T) {
	if !isVirtualMAC("02:50:32:aa:bb:cc") || !isVirtualMAC("02:50:32:AA:BB:CC") {
		t.Fatal("pad2go virtual MAC was not recognized")
	}
	if isVirtualMAC("02:11:22:33:44:55") || isVirtualMAC("") {
		t.Fatal("physical or empty MAC was treated as virtual")
	}
}

func TestScannerDefaultIntervalAndIDs(t *testing.T) {
	if DefaultInterval != time.Second {
		t.Fatalf("interval %v", DefaultInterval)
	}
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}, {Path: "other", VendorID: 1, ProductID: 2}}}
	a := &testApp{devices: map[string]app.Device{}}
	s := &Scanner{Backend: b, App: a, Interval: 5 * time.Millisecond}
	spinScan(t, s, 2)
	if b.opens != 1 {
		t.Fatalf("opens=%d", b.opens)
	}
	for _, d := range a.Devices() {
		_ = d.Close()
	}
}
func TestScannerNoDevicesFallback(t *testing.T) {
	var b Backend = stubBackend{}
	got, err := b.List()
	if err != nil || len(got) != 0 {
		t.Fatalf("List=%v, %v", got, err)
	}
	if _, err := b.Open(Info{}); err == nil {
		t.Fatal("the stub opened a device")
	}
	// A scanner over the stub runs and attaches nothing.
	a := &testApp{devices: map[string]app.Device{}}
	spinScan(t, &Scanner{Backend: b, App: a, Interval: 3 * time.Millisecond}, 3)
	if adds := a.AddCount(); adds != 0 {
		t.Fatalf("attached %d devices from the stub", adds)
	}
}
func TestScannerDoesNotReattachAttachedDevice(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}}
	a := &testApp{devices: map[string]app.Device{}}
	s := &Scanner{Backend: b, App: a, Interval: 3 * time.Millisecond}
	spinScan(t, s, 10)
	if adds := a.AddCount(); adds != 1 {
		t.Fatalf("adds=%d", adds)
	}
	for _, d := range a.Devices() {
		_ = d.Close()
	}
}

var _ = protocol.KindDualSense
var _ = slog.Default

func TestDualSenseDisconnectIgnoredUntilReplug(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scanner{Backend: b, App: a, Interval: 4 * time.Millisecond}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for a.AddCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	d := a.devices["ds"]
	a.mu.Unlock()
	if d == nil {
		t.Fatal("not attached")
	}
	a.RemoveDevice("ds")
	_ = d.Close()
	time.Sleep(25 * time.Millisecond)
	if adds := a.AddCount(); adds != 1 {
		t.Fatalf("reattached while listed: %d", adds)
	}
	b.mu.Lock()
	b.infos = nil
	b.mu.Unlock()
	time.Sleep(12 * time.Millisecond)
	b.mu.Lock()
	b.infos = []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}
	b.mu.Unlock()
	deadline = time.Now().Add(time.Second)
	for a.AddCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if adds := a.AddCount(); adds != 2 {
		t.Fatalf("adds after replug=%d", adds)
	}
}

func TestDisconnectedDualSenseReappearsWithNewPath(t *testing.T) {
	old := Info{Path: "old", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}
	newPath := Info{Path: "new", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}
	b := &testBackend{infos: []Info{old}, macs: map[string]string{"old": "same", "new": "same"}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Scanner{Backend: b, App: a, Interval: 5 * time.Millisecond}).Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "initial attach", func() bool { return a.Connected("same") })
	a.mu.Lock()
	first := a.devices["same"]
	a.mu.Unlock()
	a.RemoveDevice("same")
	_ = first.Close()
	// Let the scanner record the user's explicit disconnect while the old
	// path is still present, then replace it without an empty enumeration.
	time.Sleep(25 * time.Millisecond)
	b.mu.Lock()
	b.infos = []Info{newPath}
	b.mu.Unlock()
	waitFor(t, "reconnect with new path", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.devices["same"] != nil && a.devices["same"] != first
	})
}
func TestDualSenseOpenFailureRetries(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}, fail: 1}
	a := &testApp{devices: map[string]app.Device{}}
	var logs bytes.Buffer
	var logMu sync.Mutex
	h := slog.NewTextHandler(&lockedWriter{mu: &logMu, w: &logs}, nil)
	s := &Scanner{Backend: b, App: a, Interval: 4 * time.Millisecond, Log: slog.New(h)}
	spinScan(t, s, 5)
	if b.opens < 2 || a.AddCount() != 1 {
		t.Fatalf("opens=%d adds=%d", b.opens, a.AddCount())
	}
	logMu.Lock()
	out := logs.String()
	logMu.Unlock()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "open DualSense") {
		t.Fatalf("no warning logged for the failed open: %q", out)
	}
	for _, d := range a.Devices() {
		_ = d.Close()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestScannerRejectsBluetoothWithoutIdentity(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "bt", VendorID: 0x054C, ProductID: 0x0CE6, Bluetooth: true}}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(&Scanner{Backend: b, App: a, Interval: 5 * time.Millisecond}).Run(ctx)
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done
	b.mu.Lock()
	opens := b.opens
	b.mu.Unlock()
	if opens == 0 || a.Connected("bt") {
		t.Fatalf("unknown Bluetooth identity must not attach; opens=%d", opens)
	}
}

func TestScannerPrefersUSBForSameDualSense(t *testing.T) {
	bt := Info{Path: "bt", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID, Bluetooth: true}
	usb := Info{Path: "usb", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}
	b := &testBackend{infos: []Info{bt}, macs: map[string]string{"bt": "same", "usb": "same"}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Scanner{Backend: b, App: a, Interval: 5 * time.Millisecond}).Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "Bluetooth attach", func() bool { return a.Connected("same") })
	a.mu.Lock()
	original := a.devices["same"]
	a.mu.Unlock()
	b.mu.Lock()
	b.infos = []Info{bt, usb}
	b.mu.Unlock()
	waitFor(t, "USB replacement", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.devices["same"] != nil && a.devices["same"] != original
	})
	a.mu.Lock()
	replacement := a.devices["same"]
	a.mu.Unlock()
	b.mu.Lock()
	b.infos = []Info{bt}
	b.mu.Unlock()
	_ = replacement.Close()
	waitFor(t, "Bluetooth fallback", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.devices["same"] != nil && a.devices["same"] != replacement
	})
}

func TestScannerDisconnectIgnoresBothLinksUntilUnplugged(t *testing.T) {
	bt := Info{Path: "bt", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID, Bluetooth: true}
	usb := Info{Path: "usb", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}
	b := &testBackend{infos: []Info{bt, usb}, macs: map[string]string{"bt": "same", "usb": "same"}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Scanner{Backend: b, App: a, Interval: 5 * time.Millisecond}).Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "USB attach", func() bool { return a.Connected("same") })
	a.mu.Lock()
	d := a.devices["same"]
	a.mu.Unlock()
	a.RemoveDevice("same")
	_ = d.Close()
	time.Sleep(35 * time.Millisecond)
	if a.Connected("same") {
		t.Fatal("reattached through another link after disconnect")
	}
	b.mu.Lock()
	b.infos = nil
	b.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	b.mu.Lock()
	b.infos = []Info{bt}
	b.mu.Unlock()
	waitFor(t, "Bluetooth attach after both links disappeared", func() bool { return a.Connected("same") })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// Unplugging (the read fails, nobody called Close) removes the device from the
// app on a scan; plugging it back attaches it again.
func TestScannerRemovesUnpluggedDevice(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}}
	a := &testApp{devices: map[string]app.Device{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Scanner{Backend: b, App: a, Interval: 4 * time.Millisecond}).Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "attach", func() bool { return a.Connected("ds") })

	b.mu.Lock()
	b.infos = nil
	tr := b.last
	b.mu.Unlock()
	_ = tr.Close() // the cable is pulled: reads fail, the device was never closed by the app
	waitFor(t, "removal", func() bool { return !a.Connected("ds") })

	b.mu.Lock()
	b.infos = []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}
	b.mu.Unlock()
	waitFor(t, "reattach", func() bool { return a.Connected("ds") })
}

// While the app is full the scanner attaches nothing and keeps trying; it
// attaches as soon as a slot frees.
func TestScannerRetriesWhileAppFull(t *testing.T) {
	b := &testBackend{infos: []Info{{Path: "ds", VendorID: 0x054C, ProductID: 0x0CE6}}}
	a := &testApp{devices: map[string]app.Device{}, full: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Scanner{Backend: b, App: a, Interval: 4 * time.Millisecond}).Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(40 * time.Millisecond)
	a.mu.Lock()
	adds := a.adds
	a.full = false
	a.mu.Unlock()
	if adds != 0 {
		t.Fatalf("attached %d times while full", adds)
	}
	waitFor(t, "attach after a slot frees", func() bool { return a.Connected("ds") })
}
