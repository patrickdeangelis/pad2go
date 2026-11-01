package switchpad

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad/hidreport"
)

type fakeHost struct {
	mu          sync.Mutex
	config      hidpad.HostConfig
	events      chan hidpad.HostEvent
	reports     [][]byte
	completions []hidpad.HostEvent
	closed      bool
	startErr    error
}

func newFakeHost() *fakeHost {
	return &fakeHost{events: make(chan hidpad.HostEvent, 16)}
}

func (h *fakeHost) Start(_ context.Context, cfg hidpad.HostConfig) error {
	h.mu.Lock()
	h.config = cfg
	h.mu.Unlock()
	return h.startErr
}

func (h *fakeHost) StopEvents() error { return nil }
func (h *fakeHost) Events() <-chan hidpad.HostEvent {
	return h.events
}

func (h *fakeHost) SubmitReport(_ context.Context, report []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return hidpad.ErrClosed
	}
	h.reports = append(h.reports, append([]byte(nil), report...))
	return nil
}

func (h *fakeHost) Complete(_ context.Context, event hidpad.HostEvent, _ []byte, _ error) error {
	h.mu.Lock()
	h.completions = append(h.completions, event)
	h.mu.Unlock()
	return nil
}

func (h *fakeHost) Close() error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	return nil
}

func (h *fakeHost) snapshotConfig() hidpad.HostConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.config
}

func (h *fakeHost) snapshotReports() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]byte, len(h.reports))
	for i, report := range h.reports {
		out[i] = append([]byte(nil), report...)
	}
	return out
}

func (h *fakeHost) reportCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.reports)
}

func openTestDevice(t *testing.T) (*Device, *fakeHost) {
	t.Helper()
	host := newFakeHost()
	device, err := Open(Options{Host: host})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = device.Close() })
	return device, host
}

func waitForReport(t *testing.T, host *fakeHost, match func([]byte) bool) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, report := range host.snapshotReports() {
			if match(report) {
				return append([]byte(nil), report...)
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no report matched; got %d reports", host.reportCount())
	return nil
}

func subcommandOutput(subcommand byte, args []byte) []byte {
	report := make([]byte, reportSize)
	report[0] = report01
	copy(report[10:], []byte{subcommand})
	copy(report[11:], args)
	return report
}

func TestOpenSubmitsNeutralReport(t *testing.T) {
	device, host := openTestDevice(t)

	if device.ID() == "" || device.ID()[:7] != "pad2go-" {
		t.Fatalf("unexpected ID %q", device.ID())
	}
	cfg := host.snapshotConfig()
	if cfg.VendorID != 0x057E || cfg.ProductID != 0x2009 {
		t.Fatalf("VID/PID = %04x/%04x, want 057e/2009", cfg.VendorID, cfg.ProductID)
	}
	if !bytes.Equal(cfg.Descriptor, hidreport.BuildSwitchPro()) {
		t.Fatal("host received a different descriptor")
	}
	if host.reportCount() != 1 {
		t.Fatalf("got %d reports after Open, want the neutral one", host.reportCount())
	}
	neutral := host.snapshotReports()[0]
	if len(neutral) != reportSize || neutral[0] != report30 {
		t.Fatalf("neutral report = % x", neutral)
	}
	if neutral[2] != 0x8E || neutral[12] != 0x08 {
		t.Fatalf("neutral status bytes = % x", neutral)
	}
	if !bytes.Equal(neutral[6:12], []byte{0x00, 0x08, 0x80, 0x00, 0x08, 0x80}) {
		t.Fatalf("neutral sticks = % x", neutral[6:12])
	}
}

func TestDeviceInfoReply(t *testing.T) {
	device, host := openTestDevice(t)
	host.events <- hidpad.HostEvent{
		Kind: hidpad.HostOutputReport, ReportID: report01,
		Data: subcommandOutput(0x02, nil),
	}

	reply := waitForReport(t, host, func(report []byte) bool {
		return len(report) == reportSize && report[0] == report21 && report[14] == 0x02
	})
	if reply[13] != 0x82 {
		t.Fatalf("device-info ack = 0x%02x, want 0x82", reply[13])
	}
	if reply[17] != 0x03 {
		t.Fatalf("controller type = 0x%02x, want 0x03 (Pro)", reply[17])
	}
	wantMAC := identityMAC(device.ID())
	if !bytes.Equal(reply[19:25], wantMAC) {
		t.Fatalf("device-info MAC = % x, want % x", reply[19:25], wantMAC)
	}
}

func TestSPIReadReplies(t *testing.T) {
	_, host := openTestDevice(t)

	address := []byte{0x3D, 0x60, 0x00, 0x00, 0x12}
	host.events <- hidpad.HostEvent{
		Kind: hidpad.HostOutputReport, ReportID: report01,
		Data: subcommandOutput(0x10, address),
	}
	reply := waitForReport(t, host, func(report []byte) bool {
		return len(report) == reportSize && report[0] == report21 && report[14] == 0x10
	})
	if reply[13] != 0x90 {
		t.Fatalf("SPI ack = 0x%02x, want 0x90", reply[13])
	}
	if !bytes.Equal(reply[15:20], address) {
		t.Fatalf("SPI echoed % x, want % x", reply[15:20], address)
	}
	if !bytes.Equal(reply[20:38], stickCalibration[:]) {
		t.Fatalf("stick calibration = % x", reply[20:38])
	}

	address = []byte{0x20, 0x60, 0x00, 0x00, 0x18}
	host.events <- hidpad.HostEvent{
		Kind: hidpad.HostOutputReport, ReportID: report01,
		Data: subcommandOutput(0x10, address),
	}
	reply = waitForReport(t, host, func(report []byte) bool {
		return len(report) == reportSize && report[0] == report21 && report[14] == 0x10 &&
			bytes.Equal(report[15:19], address[:4])
	})
	if !bytes.Equal(reply[20:44], imuCalibration[:]) {
		t.Fatalf("IMU calibration = % x", reply[20:44])
	}
}

func TestProprietaryStatusAndForceUSB(t *testing.T) {
	device, host := openTestDevice(t)

	status := make([]byte, reportSize)
	status[0], status[1] = report80, 0x01
	host.events <- hidpad.HostEvent{Kind: hidpad.HostOutputReport, ReportID: report80, Data: status}
	reply := waitForReport(t, host, func(report []byte) bool {
		return len(report) == reportSize && report[0] == report81 && report[1] == 0x01
	})
	if reply[3] != 0x03 {
		t.Fatalf("proprietary device type = 0x%02x, want 0x03", reply[3])
	}
	mac := identityMAC(device.ID())
	for i := 0; i < 6; i++ {
		if reply[4+i] != mac[5-i] {
			t.Fatalf("proprietary MAC = % x, want reverse of % x", reply[4:10], mac)
		}
	}

	before := host.reportCount()
	force := make([]byte, reportSize)
	force[0], force[1] = report80, 0x04
	host.events <- hidpad.HostEvent{Kind: hidpad.HostOutputReport, ReportID: report80, Data: force}
	time.Sleep(20 * time.Millisecond)
	if host.reportCount() != before {
		t.Fatalf("ForceUSB produced %d replies, want none", host.reportCount()-before)
	}
}

func TestRumbleFeedback(t *testing.T) {
	device, host := openTestDevice(t)

	active := make([]byte, reportSize)
	active[0] = report10
	copy(active[2:6], []byte{0x00, 0x20, 0x00, 0x60})
	copy(active[6:10], []byte{0x00, 0x30, 0x00, 0x70})
	host.events <- hidpad.HostEvent{Kind: hidpad.HostOutputReport, ReportID: report10, Data: active}

	select {
	case feedback := <-device.Feedback():
		if feedback.Rumble == nil || feedback.Rumble.Left == 0 || feedback.Rumble.Right == 0 {
			t.Fatalf("decoded rumble = %+v", feedback.Rumble)
		}
	case <-time.After(time.Second):
		t.Fatal("no rumble feedback")
	}

	neutral := make([]byte, reportSize)
	neutral[0] = report10
	neutral[2], neutral[3], neutral[4], neutral[5] = 0x00, 0x01, 0x40, 0x40
	neutral[6], neutral[7], neutral[8], neutral[9] = 0x00, 0x01, 0x40, 0x40
	host.events <- hidpad.HostEvent{Kind: hidpad.HostOutputReport, ReportID: report10, Data: neutral}
	select {
	case feedback := <-device.Feedback():
		t.Fatalf("neutral rumble produced %+v", feedback)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestUnknownSubcommandIsAcked(t *testing.T) {
	_, host := openTestDevice(t)
	host.events <- hidpad.HostEvent{
		Kind: hidpad.HostOutputReport, ReportID: report01,
		Data: subcommandOutput(0x77, nil),
	}
	reply := waitForReport(t, host, func(report []byte) bool {
		return len(report) == reportSize && report[0] == report21 && report[14] == 0x77
	})
	if reply[13] != 0x80 {
		t.Fatalf("unknown subcommand ack = 0x%02x, want 0x80", reply[13])
	}
}

func TestSubmitReportValidationAndClose(t *testing.T) {
	device, host := openTestDevice(t)

	if err := device.SubmitReport(make([]byte, reportSize-1)); err == nil {
		t.Fatal("short report was accepted")
	}
	wrong := make([]byte, reportSize)
	wrong[0] = report21
	if err := device.SubmitReport(wrong); err == nil {
		t.Fatal("0x21 report was accepted as input")
	}

	valid := neutral30()
	if err := device.SubmitReport(valid); err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if host.reportCount() != 2 {
		t.Fatalf("got %d reports, want 2", len(host.reports))
	}

	if err := device.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := device.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := device.SubmitReport(valid); !errors.Is(err, hidpad.ErrClosed) {
		t.Fatalf("SubmitReport after Close = %v, want ErrClosed", err)
	}
	if _, ok := <-device.Feedback(); ok {
		t.Fatal("feedback channel stayed open after Close")
	}
}

func TestSplitOutputShapes(t *testing.T) {
	id, body := splitOutput(hidpad.HostEvent{ReportID: report01, Data: append([]byte{report01}, make([]byte, 63)...)})
	if id != report01 || len(body) != 63 {
		t.Fatalf("64-byte shape = 0x%02x/%d", id, len(body))
	}
	data := append([]byte{0x05}, make([]byte, 62)...)
	id, body = splitOutput(hidpad.HostEvent{ReportID: report01, Data: data})
	if id != report01 || len(body) != 63 || body[0] != 0x05 {
		t.Fatalf("63-byte shape = 0x%02x/%d/% x", id, len(body), body[:1])
	}
	id, body = splitOutput(hidpad.HostEvent{ReportID: report01, Data: append([]byte{report01}, make([]byte, 48)...)})
	if id != report01 || len(body) != 48 {
		t.Fatalf("49-byte shape = 0x%02x/%d", id, len(body))
	}
	id, body = splitOutput(hidpad.HostEvent{ReportID: report01, Data: append([]byte{0x05}, make([]byte, 47)...)})
	if id != report01 || len(body) != 48 || body[0] != 0x05 {
		t.Fatalf("48-byte shape = 0x%02x/%d/% x", id, len(body), body[:1])
	}
}
