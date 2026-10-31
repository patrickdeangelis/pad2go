package dualsense

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestMotionRawUSBAndBluetooth(t *testing.T) {
	usb := inputReport(nil)
	values := [6]int16{-1234, 5678, -32768, 8192, -8192, 321}
	for i, v := range values {
		binary.LittleEndian.PutUint16(usb[16+2*i:], uint16(v))
	}
	var bt [bluetoothReportLen]byte
	bt[0] = 0x31
	copy(bt[2:65], usb[1:])
	binary.LittleEndian.PutUint32(bt[74:], bluetoothCRC(0xa1, bt[:74]))
	for _, parse := range []func() (protocol.Input, bool){func() (protocol.Input, bool) { return Parse(usb) }, func() (protocol.Input, bool) { return ParseBluetooth(bt[:]) }} {
		in, ok := parse()
		if !ok || in.DualSenseGyroRaw != [3]int16{-1234, 5678, -32768} || in.DualSenseAccelRaw != [3]int16{8192, -8192, 321} {
			t.Fatalf("unexpected motion: %+v ok=%v", in, ok)
		}
	}
}

func TestTouchContactsAndClick(t *testing.T) {
	r := inputReport(nil)
	r[10] |= 2
	copy(r[33:41], []byte{7, 0x34, 0x52, 0x18, 0x80 | 8, 0, 0, 0})
	in, ok := Parse(r)
	if !ok || in.Buttons&protocol.BtnPSCClick == 0 {
		t.Fatal("touch click missing")
	}
	if got := in.Touch[0]; !got.Active || got.ID != 7 || got.X != 0x234 || got.Y != 0x185 {
		t.Fatalf("contact: %+v", got)
	}
	if in.Touch[1].Active || in.Touch[1].ID != 8 {
		t.Fatalf("inactive contact: %+v", in.Touch[1])
	}
}

func TestDiagnosticsCountsReports(t *testing.T) {
	tr := newFakeTransport()
	d := NewDevice(tr, "diagnostics", nil)
	defer d.Close()
	if got := d.Diagnostics(); got.LastValidMillis != -1 || got.Valid != 0 {
		t.Fatalf("initial diagnostics: %+v", got)
	}
	tr.reads <- []byte{1, 2}
	tr.reads <- inputReport(nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := d.Diagnostics()
		if got.Valid == 1 && got.Invalid == 1 {
			if got.LastValidMillis < 0 || got.Calibrated {
				t.Fatalf("diagnostics: %+v", got)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("reports not counted: %+v", d.Diagnostics())
}

func TestQueuedOutputAfterCloseIsRejected(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Device) error
	}{
		{"rumble", func(d *Device) error { return d.Rumble(protocol.FromMotors(100, 100, 5)) }},
		{"led", func(d *Device) error { return d.SetPlayerLEDs(context.Background(), 1) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			tr := newFakeTransport()
			d := NewDevice(tr, "id", nil)
			d.wmu.Lock()
			done := make(chan error, 1)
			go func() { done <- op.run(d) }()
			closed := make(chan struct{})
			go func() { d.Close(); close(closed) }()
			deadline := time.Now().Add(time.Second)
			for !d.Closed() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !d.Closed() {
				d.wmu.Unlock()
				t.Fatal("close did not start")
			}
			d.wmu.Unlock()
			if err := <-done; !errors.Is(err, ErrClosed) {
				t.Fatalf("queued output: %v", err)
			}
			<-closed
			tr.mu.Lock()
			defer tr.mu.Unlock()
			if len(tr.writes) != 1 {
				t.Fatalf("got %d writes, want only close report", len(tr.writes))
			}
		})
	}
}

// fakeTransport records writes in order and feeds queued input reports.
type fakeTransport struct {
	mu     sync.Mutex
	writes [][]byte
	events []string
	reads  chan []byte
	once   sync.Once
	gone   sync.Once
}

// unplug makes the next and all later Reads fail, as if the device was removed.
func (f *fakeTransport) unplug() { f.gone.Do(func() { close(f.reads) }) }

func newFakeTransport() *fakeTransport {
	return &fakeTransport{reads: make(chan []byte, 16)}
}

func (f *fakeTransport) Read(buf []byte) (int, error) {
	r, ok := <-f.reads
	if !ok {
		return 0, fmt.Errorf("fake: device unplugged")
	}
	return copy(buf, r), nil
}

func (f *fakeTransport) Write(r []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), r...))
	f.events = append(f.events, "write")
	return nil
}

func (f *fakeTransport) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.events = append(f.events, "close")
		f.mu.Unlock()
		f.unplug()
	})
	return nil
}

func (f *fakeTransport) lastWrite(t *testing.T) []byte {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		t.Fatal("no output report written")
	}
	return f.writes[len(f.writes)-1]
}

// inputReport builds a 64-byte report with sticks centred and the hat released.
func inputReport(edit func(r []byte)) []byte {
	r := make([]byte, InputReportLen)
	r[0] = 0x01
	r[1], r[2], r[3], r[4] = 128, 128, 128, 128
	r[8] = 0x08
	r[53] = 0x08
	if edit != nil {
		edit(r)
	}
	return r
}

func TestParseMute(t *testing.T) {
	report := inputReport(func(r []byte) { r[10] = 0x04 })
	in, ok := Parse(report)
	if !ok || in.Buttons&protocol.BtnMute == 0 {
		t.Fatalf("mute not parsed: %+v ok=%v", in.Buttons, ok)
	}
	if in.Buttons&(protocol.BtnHome|protocol.BtnPSCClick) != 0 {
		t.Fatalf("mute report also set system buttons: %#x", in.Buttons)
	}
}

func TestParseRejectsBadReports(t *testing.T) {
	good := inputReport(nil)
	tooShort := append([]byte(nil), good[:63]...)
	tooLong := append(append([]byte(nil), good...), 0)
	wrongID := inputReport(func(r []byte) { r[0] = 0x02 })

	for name, report := range map[string][]byte{
		"length 63": tooShort,
		"length 65": tooLong,
		"ID 0x02":   wrongID,
		"empty":     nil,
	} {
		if in, ok := Parse(report); ok {
			t.Errorf("%s: Parse ok=true, want false (decoded %+v)", name, in)
		}
	}
	if _, ok := Parse(good); !ok {
		t.Error("a valid 64-byte report with ID 0x01 was rejected")
	}
}

func TestConfiguredDeadzoneBothTransports(t *testing.T) {
	usb := inputReport(func(r []byte) { r[offLX] = 140; r[offRX] = 140 })
	var bt [bluetoothReportLen]byte
	bt[0] = 0x31
	copy(bt[2:65], usb[1:])
	crc := bluetoothCRC(0xa1, bt[:74])
	bt[74], bt[75], bt[76], bt[77] = byte(crc), byte(crc>>8), byte(crc>>16), byte(crc>>24)
	for name, parse := range map[string]func(float64) (protocol.Input, bool){
		"usb":       func(dz float64) (protocol.Input, bool) { return parseWithDeadzone(usb, dz) },
		"bluetooth": func(dz float64) (protocol.Input, bool) { return parseBluetoothWithDeadzone(bt[:], dz) },
	} {
		t.Run(name, func(t *testing.T) {
			small, ok := parse(0.05)
			if !ok || small.Left.X == 0 || small.Right.X == 0 {
				t.Fatal("5% should preserve both sticks")
			}
			large, ok := parse(0.10)
			if !ok || large.Left.X != 0 || large.Right.X != 0 {
				t.Fatal("10% should suppress both sticks")
			}
		})
	}
}

func TestDualSenseOutputRumble(t *testing.T) {
	cases := []struct {
		name        string
		left, right uint8 // Xbox-style motor values fed through protocol.FromMotors
		wantLeft    byte  // heavy motor, output offset 4
		wantRight   byte  // light motor, output offset 3
	}{
		{"(255,0)", 255, 0, 255, 0},
		{"(0,128)", 0, 128, 0, 128},
		{"(0,0)", 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ft := newFakeTransport()
			d := NewDevice(ft, "id", nil)
			defer d.Close()
			if err := d.Rumble(protocol.FromMotors(c.left, c.right, 5)); err != nil {
				t.Fatal(err)
			}
			r := ft.lastWrite(t)
			if len(r) != OutputReportLen || r[0] != 0x02 {
				t.Fatalf("report len %d id %#x, want %d and 0x02", len(r), r[0], OutputReportLen)
			}
			if r[1]&0x03 != 0x03 {
				t.Errorf("valid_flag0 = %#x, want bits 0x03 set", r[1])
			}
			if r[4] != c.wantLeft || r[3] != c.wantRight {
				t.Errorf("motors left=%d right=%d, want left=%d right=%d", r[4], r[3], c.wantLeft, c.wantRight)
			}
			if r[2]&flag1PlayerLED != 0 {
				t.Error("player-LED valid bit set before any LED was set")
			}
		})
	}
}

func TestDualSenseOutputPlayerLEDs(t *testing.T) {
	want := []byte{0x04, 0x0A, 0x15, 0x1B, 0x1F, 0x1F, 0x1F, 0x1F}
	ft := newFakeTransport()
	d := NewDevice(ft, "id", nil)
	defer d.Close()
	for slot := 1; slot <= 8; slot++ {
		if err := d.SetPlayerLEDs(context.Background(), slot); err != nil {
			t.Fatal(err)
		}
		r := ft.lastWrite(t)
		if r[44] != want[slot-1] {
			t.Errorf("slot %d: LED byte %#x, want %#x", slot, r[44], want[slot-1])
		}
		if r[2]&0x10 == 0 {
			t.Errorf("slot %d: valid_flag1 %#x lacks 0x10", slot, r[2])
		}
	}
	// A later rumble re-sends the last LED value.
	if err := d.Rumble(protocol.SilentVibration()); err != nil {
		t.Fatal(err)
	}
	if r := ft.lastWrite(t); r[44] != 0x1F || r[2]&0x10 == 0 {
		t.Errorf("rumble after LEDs: LED byte %#x flag1 %#x, want 0x1F with 0x10", r[44], r[2])
	}
}

func TestCloseClearsPlayerLEDsAndMotors(t *testing.T) {
	for _, bluetooth := range []bool{false, true} {
		t.Run(fmt.Sprintf("bluetooth=%t", bluetooth), func(t *testing.T) {
			ft := newFakeTransport()
			var d *Device
			if bluetooth {
				d = NewBluetoothDevice(ft, "id", nil)
			} else {
				d = NewDevice(ft, "id", nil)
			}
			if err := d.SetPlayerLEDs(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if err := d.Rumble(protocol.FromMotors(200, 100, 5)); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			r := ft.lastWrite(t)
			offset := 0
			if bluetooth {
				offset = 2
			}
			if r[outFlag1+offset]&flag1PlayerLED == 0 || r[outPlayerLED+offset] != 0 || r[outLeftMtr+offset] != 0 || r[outRightMtr+offset] != 0 {
				t.Fatalf("close report did not clear LEDs and motors: %x", r)
			}
		})
	}
}

func TestLightingPreservedWithRumble(t *testing.T) {
	for _, bt := range []bool{false, true} {
		t.Run(fmt.Sprintf("bluetooth=%v", bt), func(t *testing.T) {
			tr := newFakeTransport()
			var d *Device
			if bt {
				d = NewBluetoothDevice(tr, "id", nil)
			} else {
				d = NewDevice(tr, "id", nil)
			}
			defer d.Close()
			if err := d.SetPlayerLEDs(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if err := d.SetLighting([3]byte{200, 100, 50}, 50, true); err != nil {
				t.Fatal(err)
			}
			if err := d.Rumble(protocol.FromMotors(200, 100, 5)); err != nil {
				t.Fatal(err)
			}
			r := tr.lastWrite(t)
			shift := 0
			if bt {
				shift = 2
				if binary.LittleEndian.Uint32(r[74:]) != bluetoothCRC(0xa2, r[:74]) {
					t.Fatal("bad CRC")
				}
			}
			if r[2+shift]&0x14 != 0x14 || r[44+shift] != 0 || !slices.Equal(r[45+shift:48+shift], []byte{100, 50, 25}) {
				t.Fatalf("lighting report %x", r)
			}
			if err := d.SetLighting([3]byte{0, 0, 0}, 100, false); err != nil {
				t.Fatal(err)
			}
			if tr.lastWrite(t)[44+shift] != 4 {
				t.Fatal("player LED was not restored")
			}
		})
	}
}

func TestDualSenseBatteryBands(t *testing.T) {
	cases := []struct {
		level, status byte
		want          protocol.BatteryBand
	}{
		{6, 0x00, protocol.BatteryHigh},    // 65 %
		{5, 0x00, protocol.BatteryMedium},  // 55 %
		{2, 0x00, protocol.BatteryMedium},  // 25 %
		{1, 0x00, protocol.BatteryLow},     // 15 %
		{10, 0x00, protocol.BatteryHigh},   // 100 %
		{6, 0x0F, protocol.BatteryUnknown}, // status unknown
		{6, 0x0A, protocol.BatteryUnknown}, // abnormal voltage
		{6, 0x0B, protocol.BatteryUnknown}, // abnormal temperature
		{6, 0x01, protocol.BatteryHigh},    // charging is not a special case
	}
	for _, c := range cases {
		if got := BatteryBand(c.level, c.status); got != c.want {
			t.Errorf("BatteryBand(level %d, status %#x) = %v, want %v", c.level, c.status, got, c.want)
		}
	}
	// The same byte through Parse: level in the low nibble, status in the high.
	in, ok := Parse(inputReport(func(r []byte) { r[53] = 0x06 }))
	if !ok || in.BatteryBand != protocol.BatteryHigh {
		t.Errorf("Parse battery byte 0x06: ok=%v band=%v, want high", ok, in.BatteryBand)
	}
}

func TestDualSenseClosesWithZeroRumble(t *testing.T) {
	ft := newFakeTransport()
	d := NewDevice(ft, "id", nil)
	if err := d.Rumble(protocol.FromMotors(255, 255, 5)); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ft.mu.Lock()
	events := slices.Clone(ft.events)
	last := ft.writes[len(ft.writes)-1]
	ft.mu.Unlock()
	if n := len(events); n < 2 || events[n-1] != "close" || events[n-2] != "write" {
		t.Fatalf("events %v: want a write immediately before close", events)
	}
	if last[0] != 0x02 || last[3] != 0 || last[4] != 0 {
		t.Errorf("close wrote id %#x right %d left %d, want 0x02 with zero motors", last[0], last[3], last[4])
	}
	if !d.Closed() {
		t.Error("Closed() = false after Close")
	}
	select {
	case <-d.Done():
	default:
		t.Error("Done() not closed after Close")
	}
	if err := d.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if n := len(ft.writes); n != len(events)-1 {
		t.Errorf("second Close wrote again: %d writes, events %v", n, events)
	}
}

func TestDeviceDeliversInputAndDoneOnUnplug(t *testing.T) {
	ft := newFakeTransport()
	d := NewDevice(ft, "serial-1", nil)
	if d.Address() != "serial-1" || d.Name() != "DualSense" || d.Kind() != protocol.KindDualSense {
		t.Fatalf("identity: %q %q %v", d.Address(), d.Name(), d.Kind())
	}
	got := make(chan protocol.Input, 4)
	d.OnInput(func(in protocol.Input) { got <- in })

	ft.reads <- make([]byte, 63) // dropped: wrong length
	cross := inputReport(func(r []byte) { r[8] |= 0x20 })
	ft.reads <- cross
	select {
	case in := <-got:
		if in.Buttons != protocol.BtnB {
			t.Errorf("buttons %#x, want Cross (BtnB) only", in.Buttons)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no input delivered")
	}

	ft.unplug() // removed from the bus, without Close
	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after the read failed")
	}
	if d.Closed() {
		t.Error("Closed() true after an unplug without Close")
	}
	d.Close()
}

// Fixture input: one report per line as "<ms> <hex>", as dump-hid writes it.
type fixtureReport struct {
	ms  int
	raw []byte
}

// fixtureExpect is what a fixture's sidecar (<name>.expect.json) requires:
// some report in the capture must decode to exactly this state.
type fixtureExpect struct {
	Buttons []string   `json:"buttons"`
	Left    [2]float64 `json:"left"`
	Right   [2]float64 `json:"right"`
	L2      uint8      `json:"l2"`
	R2      uint8      `json:"r2"`
	Battery string     `json:"battery"`
}

// requiredFixtures are the synthetic captures the set must always cover.
var requiredFixtures = []string{
	"neutral", "square", "cross", "circle", "triangle",
	"left-stick-right", "left-stick-left", "left-stick-up", "left-stick-down",
	"right-stick-right", "right-stick-left", "right-stick-up", "right-stick-down",
	"l2-full", "r2-full",
}

// fixtureButtons names the Switch-shaped bits Parse can set, as a DualSense
// labels them.
var fixtureButtons = []struct {
	bit  uint32
	name string
}{
	{protocol.BtnB, "Cross"}, {protocol.BtnA, "Circle"}, {protocol.BtnY, "Square"}, {protocol.BtnX, "Triangle"},
	{protocol.BtnL, "L1"}, {protocol.BtnR, "R1"}, {protocol.BtnLStick, "L3"}, {protocol.BtnRStick, "R3"},
	{protocol.BtnPlus, "Options"}, {protocol.BtnMinus, "Create"}, {protocol.BtnHome, "PS"},
	{protocol.BtnUp, "Up"}, {protocol.BtnDown, "Down"}, {protocol.BtnLeft, "Left"}, {protocol.BtnRight, "Right"},
}

func pressedNames(buttons uint32) []string {
	var names []string
	var known uint32
	for _, b := range fixtureButtons {
		known |= b.bit
		if buttons&b.bit != 0 {
			names = append(names, b.name)
		}
	}
	if extra := buttons &^ known; extra != 0 {
		names = append(names, fmt.Sprintf("unlabelled(%#x)", extra))
	}
	slices.Sort(names)
	return names
}

func bandName(b protocol.BatteryBand) string {
	if b == protocol.BatteryUnknown {
		return "unknown"
	}
	return b.String()
}

func readFixture(t *testing.T, path string) []fixtureReport {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []fixtureReport
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 {
			t.Fatalf("%s:%d: want \"<ms> <hex>\", got %q", path, line, text)
		}
		ms, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("%s:%d: bad timestamp: %v", path, line, err)
		}
		raw, err := hex.DecodeString(fields[1])
		if err != nil {
			t.Fatalf("%s:%d: bad hex: %v", path, line, err)
		}
		out = append(out, fixtureReport{ms, raw})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.hex"))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range paths {
		have[strings.TrimSuffix(filepath.Base(p), ".hex")] = true
	}
	for _, name := range requiredFixtures {
		if !have["synthetic-"+name] {
			t.Errorf("fixture set lacks synthetic-%s.hex", name)
		}
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures under testdata")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			reports := readFixture(t, path)
			if len(reports) == 0 {
				t.Fatal("fixture has no reports")
			}
			decoded := make([]protocol.Input, len(reports))
			for i, r := range reports {
				in, ok := Parse(r.raw)
				if !ok {
					t.Fatalf("line %d (%d ms): report of %d bytes not accepted", i+1, r.ms, len(r.raw))
				}
				decoded[i] = in
			}

			sidecar := strings.TrimSuffix(path, ".hex") + ".expect.json"
			data, err := os.ReadFile(sidecar)
			if err != nil {
				t.Fatalf("missing expectation sidecar: %v", err)
			}
			var want fixtureExpect
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatalf("%s: %v", sidecar, err)
			}

			var mismatches []string
			for i, in := range decoded {
				got := fixtureState(in)
				if diff := compareFixture(want, got); diff == "" {
					return
				} else {
					mismatches = append(mismatches, fmt.Sprintf("line %d: %s", i+1, diff))
				}
			}
			t.Errorf("no report matches %s; %d reports checked:\n%s",
				filepath.Base(sidecar), len(decoded), strings.Join(mismatches, "\n"))
		})
	}
}

// fixtureState renders a decoded Input in the sidecar's terms.
func fixtureState(in protocol.Input) fixtureExpect {
	return fixtureExpect{
		Buttons: pressedNames(in.Buttons),
		Left:    [2]float64{in.Left.X, in.Left.Y},
		Right:   [2]float64{in.Right.X, in.Right.Y},
		L2:      in.LeftTrigger,
		R2:      in.RightTrigger,
		Battery: bandName(in.BatteryBand),
	}
}

// compareFixture returns "" when got matches want. Sticks match within 0.02.
func compareFixture(want, got fixtureExpect) string {
	wantButtons := slices.Clone(want.Buttons)
	slices.Sort(wantButtons)
	if !slices.Equal(wantButtons, got.Buttons) {
		return fmt.Sprintf("buttons %v, want %v", got.Buttons, wantButtons)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.02 }
	if !near(got.Left[0], want.Left[0]) || !near(got.Left[1], want.Left[1]) {
		return fmt.Sprintf("left %v, want %v", got.Left, want.Left)
	}
	if !near(got.Right[0], want.Right[0]) || !near(got.Right[1], want.Right[1]) {
		return fmt.Sprintf("right %v, want %v", got.Right, want.Right)
	}
	if got.L2 != want.L2 || got.R2 != want.R2 {
		return fmt.Sprintf("triggers %d/%d, want %d/%d", got.L2, got.R2, want.L2, want.R2)
	}
	if got.Battery != want.Battery {
		return fmt.Sprintf("battery %q, want %q", got.Battery, want.Battery)
	}
	return ""
}
