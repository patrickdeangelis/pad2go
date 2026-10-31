package hidpad

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
)

func TestFactSheet(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return this test path")
	}

	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "docs", "dualsense-usb-facts.md")
	facts, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"d6cebbef5c7909168d1f881185be8f607d6aefd4",
		"Size is 32 bytes",
		"WINUHID_EVENT` is packed",
		"UHID_CREATE2` payload",
		"rd_size u16 @256",
		"event size is **4376 bytes**",
		"descriptor field summary",
		"| output | 0x02 | 0xFF00 | 0x23 |",
		"| feature | 0x05 | 0xFF00 | 0x33 |",
		"F5:3",
	} {
		if !bytes.Contains(facts, []byte(expected)) {
			t.Errorf("fact sheet does not contain %q", expected)
		}
	}
	if bytes.Contains(facts, []byte("4384")) {
		t.Fatal("fact sheet still contains the old UHID event size")
	}
	if bytes.Contains(facts, []byte("30 bytes")) {
		t.Fatal("fact sheet still contains the old DEVICE_INFO size")
	}
}

func TestWindowsConfigFields(t *testing.T) {
	config := hostConfig(DualSenseWired, "pad2go-test", makeTestFeatures("pad2go-test"))
	if config.VendorID != 0x054C || config.ProductID != 0x0CE6 || config.Version != 0x0100 {
		t.Fatalf("unexpected device IDs: VID=%04x PID=%04x version=%04x", config.VendorID, config.ProductID, config.Version)
	}
	if len(config.Descriptor) != 273 {
		t.Fatalf("descriptor length = %d, want 273", len(config.Descriptor))
	}
	if config.SupportedEvents != 0x07 {
		t.Fatalf("supported events = %#x, want GET_FEATURE|SET_FEATURE|WRITE_REPORT (0x07)", config.SupportedEvents)
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos string
		arch string
		ok   bool
	}{
		{"windows", "amd64", true},
		{"windows", "arm64", true},
		{"windows", "386", false},
		{"linux", "amd64", true},
		{"darwin", "arm64", false},
		{"plan9", "amd64", false},
	} {
		if got := supportedPlatform(tc.goos, tc.arch); got != tc.ok {
			t.Errorf("supportedPlatform(%q, %q) = %v, want %v", tc.goos, tc.arch, got, tc.ok)
		}
	}
}

func TestOpenNeutralAndEvents(t *testing.T) {
	host := newFakeHost()
	host.probeIDs = []byte{0x05, 0x09, 0x20}
	host.waitForProbes = true
	host.mutateCompletedData = true
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if got := host.snapshotConfig(); got.SupportedEvents != 0x07 {
		t.Fatalf("host event mask = %#x, want 0x07", got.SupportedEvents)
	}
	reports := host.snapshotSubmits()
	if len(reports) != 1 || !bytes.Equal(reports[0], neutralReport()) {
		t.Fatalf("initial host report = %v, want the neutral report", reports)
	}

	output := validOutputReport(0x24)
	host.emit(HostEvent{Kind: HostOutputReport, Data: output})
	gotFeedback := receiveFeedback(t, device.Feedback())
	if !gotFeedback.Known || gotFeedback.Raw[3] != 0x24 {
		t.Fatalf("unexpected output feedback: %+v", gotFeedback)
	}
	outputControl := receiveControl(t, device.Control())
	if outputControl.Kind != HostOutputReport || outputControl.ReportID != 0x02 || outputControl.Data[3] != 0x24 {
		t.Fatalf("unexpected output control notification: %+v", outputControl)
	}

	set := make([]byte, featureLengthTotal(0x05))
	set[0] = 0x05
	host.emit(HostEvent{Kind: HostSetFeature, Data: set})
	gotControl := receiveControl(t, device.Control())
	if gotControl.Kind != HostSetFeature || gotControl.ReportID != 0x05 || gotControl.Err != nil {
		t.Fatalf("unexpected control notification: %+v", gotControl)
	}
}

func TestOpenStartTimeout(t *testing.T) {
	host := newFakeHost()
	host.blockStart = true
	if _, err := Open(DualSenseWired, Options{Host: host}); !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("Open error = %v, want ErrStartTimeout", err)
	}
	if !host.isClosed() {
		t.Fatal("host was not closed after startup timeout")
	}
}

func TestIdentityMACPrefix(t *testing.T) {
	mac := identityMAC("pad2go-abc")
	if mac[0] != 0x02 || mac[1] != 0x50 || mac[2] != 0x32 {
		t.Fatalf("identity MAC %x does not start with 02:50:32", mac)
	}
	if !validLocalUnicastMAC(mac) {
		t.Fatalf("identity MAC %x is not locally administered unicast", mac)
	}
}

func TestIdentityAndMAC(t *testing.T) {
	firstHost, secondHost := newFakeHost(), newFakeHost()
	if _, err := Open(DualSenseWired, Options{ID: "not-an-identity", Host: newFakeHost()}); !errors.Is(err, ErrBadFeature) {
		t.Fatalf("invalid explicit identity error = %v, want ErrBadFeature", err)
	}
	first, err := Open(DualSenseWired, Options{Host: firstHost})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(DualSenseWired, Options{Host: secondHost})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	identityPattern := regexp.MustCompile(`^pad2go-[0-9a-f]+$`)
	if first.ID() == second.ID() || !identityPattern.MatchString(first.ID()) || !identityPattern.MatchString(second.ID()) {
		t.Fatalf("device IDs are not unique pad2go identities: %q, %q", first.ID(), second.ID())
	}
	firstMAC := firstHost.snapshotConfig().Features[0x09][1:7]
	secondMAC := secondHost.snapshotConfig().Features[0x09][1:7]
	if bytes.Equal(firstMAC, secondMAC) || !validLocalUnicastMAC(firstMAC) || !validLocalUnicastMAC(secondMAC) {
		t.Fatalf("feature MACs are not distinct locally administered unicast addresses: %x / %x", firstMAC, secondMAC)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	report := neutralReport()
	report[1] = 0x22
	if err := second.SubmitReport(report); err != nil {
		t.Fatalf("closing first device affected second device: %v", err)
	}
}

func TestSubmitCopiesReport(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	report := neutralReport()
	report[1] = 0x21
	if err := device.SubmitReport(report); err != nil {
		t.Fatal(err)
	}
	report[1] = 0x99
	got := host.snapshotSubmits()
	if len(got) != 2 || got[1][1] != 0x21 {
		t.Fatalf("host received report that aliases caller memory: %#v", got)
	}
}

func TestSubmitRejectsBadReport(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if err := device.SubmitReport(make([]byte, 63)); !errors.Is(err, ErrBadReport) {
		t.Fatalf("short report error = %v, want ErrBadReport", err)
	}
	report := neutralReport()
	report[0] = 0x02
	if err := device.SubmitReport(report); !errors.Is(err, ErrBadReport) {
		t.Fatalf("wrong report ID error = %v, want ErrBadReport", err)
	}
	select {
	case <-device.Done():
		t.Fatal("bad report closed the device")
	default:
	}
}

func TestNeutralReportParses(t *testing.T) {
	report := neutralReport()
	if report[0] != 0x01 || !allBytes(report, 1, 4, 0x80) || report[8] != 0x08 ||
		report[33] != 0x80 || report[37] != 0x80 || report[53] != 0x0A {
		t.Fatalf("neutral report has unexpected required fields: %x", report)
	}
	for i, value := range report {
		if i == 0 || i >= 1 && i <= 4 || i == 8 || i == 33 || i == 37 || i == 53 {
			continue
		}
		if value != 0 {
			t.Errorf("neutral report byte %d = %#x, want zero", i, value)
		}
	}
	parsed, ok := dualsense.Parse(report)
	if !ok || parsed.Buttons != 0 || parsed.Left.X != 0 || parsed.Left.Y != 0 || parsed.Right.X != 0 || parsed.Right.Y != 0 {
		t.Fatalf("neutral report parsed as non-neutral input: %+v, ok=%v", parsed, ok)
	}
}

func TestBackpressureAndRace(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	for i := byte(0); i < 20; i++ {
		report := validOutputReport(i)
		host.emit(HostEvent{Kind: HostOutputReport, Data: report})
	}
	host.waitCompletions(t, 20)
	for expected := byte(4); expected < 20; expected++ {
		feedback := receiveFeedback(t, device.Feedback())
		if feedback.Raw[3] != expected {
			t.Fatalf("drop-oldest feedback byte = %#x, want %#x", feedback.Raw[3], expected)
		}
	}
	for expected := byte(4); expected < 20; expected++ {
		control := receiveControl(t, device.Control())
		if control.Kind != HostOutputReport || control.Data[3] != expected {
			t.Fatalf("drop-oldest control byte = %#x, want %#x", control.Data[3], expected)
		}
	}
	blockingHost := newFakeHost()
	blocking, err := Open(DualSenseWired, Options{Host: blockingHost})
	if err != nil {
		t.Fatal(err)
	}
	blockingHost.setBlockSubmit(true)
	submitted := make(chan struct{})
	submitDone := make(chan error, 1)
	go func() {
		close(submitted)
		submitDone <- blocking.SubmitReport(neutralReport())
	}()
	<-submitted
	blockingHost.waitSubmitStarted(t)
	closeDone := make(chan error, 1)
	go func() { closeDone <- blocking.Close() }()
	select {
	case err := <-submitDone:
		if err == nil {
			t.Fatal("submit blocked in host I/O unexpectedly succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not cancel blocked host submission within one second")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return within one second")
	}
	if err := blocking.SubmitReport(neutralReport()); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after Close error = %v, want ErrClosed", err)
	}
}

func TestFeedbackAfterHostComplete(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	host.mu.Lock()
	host.blockComplete = true
	host.mu.Unlock()
	host.emit(HostEvent{Kind: HostOutputReport, ReportID: 0x02, Data: validOutputReport(9)})
	select {
	case <-host.completeStarted:
	case <-time.After(time.Second):
		t.Fatal("host Complete was not called")
	}
	select {
	case feedback := <-device.Feedback():
		t.Fatalf("Feedback arrived before host completion: %+v", feedback)
	default:
	}
	close(host.completeGate)
	select {
	case feedback := <-device.Feedback():
		if !feedback.Known || len(feedback.Raw) == 0 || feedback.Raw[0] != reportOutputID {
			t.Fatalf("Feedback after completion = %+v", feedback)
		}
	case <-time.After(time.Second):
		t.Fatal("Feedback did not arrive after host completion")
	}
}

func TestFeedback63(t *testing.T) {
	report := make([]byte, 63)
	report[0] = 0x02
	report[1], report[2], report[39] = 0x03, 0x14, 0x04
	report[3], report[4], report[44] = 0x40, 0x80, 0x1B
	report[45], report[46], report[47] = 0x12, 0x34, 0x56
	feedback := decodeFeedback(report)
	if !feedback.Known || feedback.Rumble == nil || *feedback.Rumble != (Rumble{Left: 0x80, Right: 0x40}) ||
		feedback.Lightbar == nil || *feedback.Lightbar != (RGB{Red: 0x12, Green: 0x34, Blue: 0x56}) ||
		feedback.PlayerLEDs == nil || *feedback.PlayerLEDs != 0x1B ||
		feedback.Flag0 != 0x03 || feedback.Flag1 != 0x14 || feedback.Flag2 != 0x04 ||
		!bytes.Equal(feedback.Raw, report) {
		t.Fatalf("unexpected decoded feedback: %+v", feedback)
	}
}

func TestFeedbackValidity(t *testing.T) {
	report := validOutputReport(0x75)
	report[1], report[2], report[39] = 0, 0, 0
	feedback := decodeFeedback(report)
	if feedback.Rumble != nil || feedback.Lightbar != nil || feedback.PlayerLEDs != nil {
		t.Fatalf("invalid flags exposed fields: %+v", feedback)
	}
	report[1] = 0x02
	report[3], report[4] = 0, 0
	feedback = decodeFeedback(report)
	if feedback.Rumble == nil || *feedback.Rumble != (Rumble{}) {
		t.Fatalf("valid zero-motor report did not expose zero rumble: %+v", feedback)
	}
	report[1], report[2], report[39] = 0, 0, 0x04
	feedback = decodeFeedback(report)
	if feedback.Rumble == nil {
		t.Fatal("flag2 rumble validity bit was ignored")
	}
	report[1], report[2], report[39] = 0, 0x04, 0
	feedback = decodeFeedback(report)
	if feedback.Lightbar == nil || feedback.PlayerLEDs != nil {
		t.Fatalf("flag1 lightbar validity was not applied: %+v", feedback)
	}
	report[2] = 0x10
	feedback = decodeFeedback(report)
	if feedback.PlayerLEDs == nil || feedback.Lightbar != nil {
		t.Fatalf("flag1 player LED validity was not applied: %+v", feedback)
	}
}

func TestFeedbackLengths(t *testing.T) {
	for _, length := range []int{48, 49, 62, 63} {
		report := make([]byte, length)
		copy(report, validOutputReport(0x61))
		feedback := decodeFeedback(report)
		if !feedback.Known || len(feedback.Raw) != length {
			t.Errorf("length %d: Known=%v raw length=%d", length, feedback.Known, len(feedback.Raw))
		}
	}
	for _, length := range []int{0, 1, 47, 64} {
		report := make([]byte, length)
		if length > 0 {
			report[0] = 0x02
		}
		feedback := decodeFeedback(report)
		if feedback.Known || len(feedback.Raw) != length {
			t.Errorf("length %d should be raw/unknown: %+v", length, feedback)
		}
	}
}

func TestFeedbackUnknownReport(t *testing.T) {
	report := make([]byte, 48)
	report[0] = 0x7F
	feedback := decodeFeedback(report)
	if feedback.Known || !bytes.Equal(feedback.Raw, report) {
		t.Fatalf("unknown report was not retained raw: %+v", feedback)
	}
}

func TestFeatureDefaults(t *testing.T) {
	features, mac, err := makeFeatures(DualSenseWired, "pad2go-defaults", nil)
	if err != nil {
		t.Fatal(err)
	}
	calibration := features[0x05]
	if len(calibration) != 41 || calibration[0] != 0x05 {
		t.Fatalf("invalid calibration feature: %x", calibration)
	}
	want := map[int]int16{
		1: 0, 3: 0, 5: 0,
		7: 8192, 9: -8192, 11: 8192, 13: -8192, 15: 8192, 17: -8192,
		19: 16384, 21: 16384,
		23: 8192, 25: -8192, 27: 8192, 29: -8192, 31: 8192, 33: -8192,
	}
	for offset, value := range want {
		if got := int16(binary.LittleEndian.Uint16(calibration[offset : offset+2])); got != value {
			t.Fatalf("calibration offset %d = %d, want %d", offset, got, value)
		}
	}
	if len(features[0x09]) != 20 || features[0x09][0] != 0x09 ||
		!bytes.Equal(features[0x09][1:7], mac) || !validLocalUnicastMAC(mac) {
		t.Fatalf("invalid pairing feature: %x", features[0x09])
	}
	firmware := features[0x20]
	if len(firmware) != 64 || firmware[0] != 0x20 ||
		binary.LittleEndian.Uint32(firmware[24:28]) != 3 ||
		binary.LittleEndian.Uint32(firmware[28:32]) != 0x00000015 ||
		binary.LittleEndian.Uint16(firmware[44:46]) != 0x0221 {
		t.Fatalf("invalid firmware feature defaults: %x", firmware)
	}
}

func TestFeatureUnprofiled(t *testing.T) {
	features, _, err := makeFeatures(DualSenseWired, "pad2go-unprofiled", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []byte{0x08, 0x0A, 0x22, 0x80, 0xF1} {
		reply, err := featureReply(id, features)
		length, _ := featureLength(id)
		if err != nil || len(reply) != length+1 || reply[0] != id {
			t.Fatalf("feature %#02x reply = %x, err=%v", id, reply, err)
		}
		for i, value := range reply[1:] {
			if value != 0 {
				t.Fatalf("feature %#02x byte %d = %#x, want zero", id, i+1, value)
			}
		}
	}
}

func TestFeatureUndeclared(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if _, err := featureReply(0x7F, makeTestFeatures(device.ID())); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("undeclared GET error = %v, want ErrUnsupportedFeature", err)
	}
	host.emit(HostEvent{Kind: HostGetFeature, ReportID: 0x7F})
	completion := host.receiveCompletion(t)
	if !errors.Is(completion.err, ErrUnsupportedFeature) {
		t.Fatalf("host GET completion error = %v, want ErrUnsupportedFeature", completion.err)
	}
	select {
	case <-device.Done():
		t.Fatal("undeclared feature request closed the device")
	default:
	}
}

func TestSetFeature(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	payload := make([]byte, featureLengthTotal(0x05))
	payload[0] = 0x05
	host.emit(HostEvent{Kind: HostSetFeature, Data: payload})
	valid := host.receiveCompletion(t)
	if valid.err != nil {
		t.Fatalf("declared SET completion error = %v", valid.err)
	}
	event := receiveControl(t, device.Control())
	if event.ReportID != 0x05 || !bytes.Equal(event.Data, payload) {
		t.Fatalf("SET_FEATURE notification = %+v", event)
	}
	host.emit(HostEvent{Kind: HostSetFeature, ReportID: 0x7F})
	invalid := host.receiveCompletion(t)
	if !errors.Is(invalid.err, ErrUnsupportedFeature) {
		t.Fatalf("undeclared SET error = %v", invalid.err)
	}
	if !errors.Is(receiveControl(t, device.Control()).Err, ErrUnsupportedFeature) {
		t.Fatal("undeclared SET notification did not retain the error")
	}
}

func TestFeatureOverrides(t *testing.T) {
	host := newFakeHost()
	host.probeIDs = []byte{0x05, 0x09, 0x20}
	host.waitForProbes = true
	overrides := makeTestFeatures("pad2go-overridden")
	overrides[0x05][1] = 0x5A
	overrides[0x09][1] = 0x02
	overrides[0x09][2] = 0x11
	overrides[0x09][3] = 0x22
	overrides[0x09][4] = 0x33
	overrides[0x09][5] = 0x44
	overrides[0x09][6] = 0x55
	overrides[0x20][24] = 0x66
	device, err := Open(DualSenseWired, Options{Host: host, Features: overrides})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	seen := host.snapshotCompletions()
	for _, id := range []byte{0x05, 0x09, 0x20} {
		var found bool
		for _, completion := range seen {
			if completion.event.Kind == HostGetFeature && completion.event.ReportID == id &&
				bytes.Equal(completion.response, overrides[id]) {
				found = true
			}
		}
		if !found {
			t.Errorf("startup probe did not receive feature override %#02x", id)
		}
	}
	for _, invalid := range []map[byte][]byte{
		{0x05: {0x06}},
		{0x05: make([]byte, 41)},
		{0x09: append([]byte{0x09, 0x00}, make([]byte, 18)...)},
	} {
		badHost := newFakeHost()
		if _, err := Open(DualSenseWired, Options{Host: badHost, Features: invalid}); !errors.Is(err, ErrBadFeature) {
			t.Errorf("invalid override error = %v, want ErrBadFeature", err)
		}
		if badHost.startCount() != 0 {
			t.Error("invalid feature override created a host device")
		}
	}
	duplicate := append([]byte(nil), overrides[0x09][1:7]...)
	bad := map[byte][]byte{0x09: append([]byte{0x09}, append(duplicate, make([]byte, 13)...)...)}
	otherHost := newFakeHost()
	if _, err := Open(DualSenseWired, Options{Host: otherHost, Features: bad}); !errors.Is(err, ErrBadFeature) {
		t.Fatalf("duplicate active feature MAC error = %v, want ErrBadFeature", err)
	} else if strings.Contains(err.Error(), device.ID()) {
		t.Errorf("duplicate MAC error leaked the other device ID %q: %v", device.ID(), err)
	}
	if otherHost.startCount() != 0 {
		t.Fatal("duplicate MAC created a host device")
	}
}

func TestNoDriverRequired(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if host.startCount() != 1 {
		t.Fatalf("fake host starts = %d, want 1", host.startCount())
	}
	if len(host.snapshotSubmits()) != 1 {
		t.Fatal("fake host did not receive the initial report")
	}
}

func TestHostLossAndCloseCancels(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	host.lose()
	select {
	case <-device.Done():
	case <-time.After(time.Second):
		t.Fatal("host loss did not close Done")
	}
	if err := device.SubmitReport(neutralReport()); err == nil || !strings.Contains(err.Error(), "event stream closed") {
		t.Fatalf("submit after host loss error = %v, want host-loss cause", err)
	}
	if _, ok := <-device.Feedback(); ok {
		t.Fatal("Feedback remained open after host loss")
	}
	if _, ok := <-device.Control(); ok {
		t.Fatal("Control remained open after host loss")
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}

	blockingHost := newFakeHost()
	blocking, err := Open(DualSenseWired, Options{Host: blockingHost})
	if err != nil {
		t.Fatal(err)
	}
	blockingHost.setBlockSubmit(true)
	submitDone := make(chan error, 1)
	go func() { submitDone <- blocking.SubmitReport(neutralReport()) }()
	blockingHost.waitSubmitStarted(t)
	start := time.Now()
	if err := blocking.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Close took %s, want under one second", elapsed)
	}
	if err := blocking.SubmitReport(neutralReport()); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after Close error = %v, want ErrClosed", err)
	}
	if err := <-submitDone; err == nil {
		t.Fatal("blocked submit unexpectedly succeeded after Close")
	}
}

func allBytes(data []byte, start, end int, want byte) bool {
	for i := start; i <= end; i++ {
		if data[i] != want {
			return false
		}
	}
	return true
}

func makeTestFeatures(id string) map[byte][]byte {
	features, _, err := makeFeatures(DualSenseWired, id, nil)
	if err != nil {
		panic(err)
	}
	return features
}

func validOutputReport(motor byte) []byte {
	report := make([]byte, 48)
	report[0], report[1], report[2], report[39] = 0x02, 0x03, 0x14, 0x04
	report[3], report[4] = motor, motor
	return report
}

func receiveFeedback(t *testing.T, events <-chan Feedback) Feedback {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("Feedback channel closed")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Feedback")
		return Feedback{}
	}
}

func receiveControl(t *testing.T, events <-chan ControlEvent) ControlEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("Control channel closed")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Control")
		return ControlEvent{}
	}
}

type fakeCompletion struct {
	event    HostEvent
	response []byte
	err      error
}

type fakeHost struct {
	mu                  sync.Mutex
	events              chan HostEvent
	completions         chan fakeCompletion
	probeIDs            []byte
	waitForProbes       bool
	blockStart          bool
	blockSubmit         bool
	submitErr           error
	mutateCompletedData bool
	blockComplete       bool
	completeStarted     chan struct{}
	completeGate        chan struct{}
	submitStarted       chan struct{}
	started             bool
	starts              int
	closed              bool
	closeOnce           sync.Once
	stopOnce            sync.Once
	config              HostConfig
	submitted           [][]byte
	completed           []fakeCompletion
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		events:          make(chan HostEvent, 128),
		completions:     make(chan fakeCompletion, 128),
		submitStarted:   make(chan struct{}, 1),
		completeStarted: make(chan struct{}, 1),
		completeGate:    make(chan struct{}),
	}
}

func (h *fakeHost) Start(ctx context.Context, config HostConfig) error {
	h.mu.Lock()
	h.started = true
	h.starts++
	h.config = config
	probes := append([]byte(nil), h.probeIDs...)
	wait := h.waitForProbes
	block := h.blockStart
	h.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	for _, id := range probes {
		h.events <- HostEvent{Kind: HostGetFeature, ReportID: id}
	}
	if wait {
		for range probes {
			select {
			case <-h.completions:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

func (h *fakeHost) Events() <-chan HostEvent { return h.events }

func (h *fakeHost) SubmitReport(ctx context.Context, report []byte) error {
	h.mu.Lock()
	h.submitted = append(h.submitted, append([]byte(nil), report...))
	block := h.blockSubmit
	submitErr := h.submitErr
	h.mu.Unlock()
	if submitErr != nil {
		return submitErr
	}
	if block {
		select {
		case h.submitStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (h *fakeHost) Complete(_ context.Context, event HostEvent, response []byte, err error) error {
	h.mu.Lock()
	mutate := h.mutateCompletedData
	block := h.blockComplete
	h.mu.Unlock()
	if block {
		select {
		case h.completeStarted <- struct{}{}:
		default:
		}
		<-h.completeGate
	}
	if mutate && len(event.Data) > 3 {
		event.Data[3] = 0xEE
	}
	completion := fakeCompletion{
		event:    HostEvent{Kind: event.Kind, Request: event.Request, ReportID: event.ReportID, Data: append([]byte(nil), event.Data...)},
		response: append([]byte(nil), response...),
		err:      err,
	}
	h.mu.Lock()
	h.completed = append(h.completed, completion)
	h.mu.Unlock()
	h.completions <- completion
	return nil
}

func (h *fakeHost) StopEvents() error {
	h.stopOnce.Do(func() { close(h.events) })
	return nil
}

func (h *fakeHost) Close() error {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		_ = h.StopEvents()
	})
	return nil
}

func (h *fakeHost) emit(event HostEvent) {
	h.events <- event
}

func (h *fakeHost) lose() {
	_ = h.StopEvents()
}

func (h *fakeHost) waitCompletions(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		h.mu.Lock()
		n := len(h.completed)
		h.mu.Unlock()
		if n >= count {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("received %d host completions, want %d", n, count)
		case <-time.After(time.Millisecond):
		}
	}
}

func (h *fakeHost) receiveCompletion(t *testing.T) fakeCompletion {
	t.Helper()
	select {
	case completion := <-h.completions:
		return completion
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host completion")
		return fakeCompletion{}
	}
}

func (h *fakeHost) waitSubmitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-h.submitStarted:
	case <-time.After(time.Second):
		t.Fatal("host submit was not started")
	}
}

func (h *fakeHost) snapshotConfig() HostConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	config := h.config
	config.Descriptor = append([]byte(nil), config.Descriptor...)
	config.NeutralReport = append([]byte(nil), config.NeutralReport...)
	config.Features = cloneFeatures(config.Features)
	return config
}

func (h *fakeHost) snapshotSubmits() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	reports := make([][]byte, len(h.submitted))
	for i, report := range h.submitted {
		reports[i] = append([]byte(nil), report...)
	}
	return reports
}

func (h *fakeHost) snapshotCompletions() []fakeCompletion {
	h.mu.Lock()
	defer h.mu.Unlock()
	completions := make([]fakeCompletion, len(h.completed))
	copy(completions, h.completed)
	return completions
}

func (h *fakeHost) startCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.starts
}

func (h *fakeHost) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *fakeHost) setBlockSubmit(block bool) {
	h.mu.Lock()
	h.blockSubmit = block
	h.mu.Unlock()
}
