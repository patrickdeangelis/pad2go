package hidpad

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type captureTransport struct {
	mu      sync.Mutex
	written []byte
	reader  *bytes.Buffer
	closed  bool
}

func (c *captureTransport) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	c.written = append(c.written, p...)
	return len(p), nil
}
func (c *captureTransport) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *captureTransport) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}
func (c *captureTransport) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
func (c *captureTransport) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.written...)
}

func lastUHIDEvent(t *testing.T, written []byte) (uint32, []byte) {
	t.Helper()
	if len(written) < uhidEventSize {
		t.Fatalf("wrote %d bytes, want at least one %d-byte event", len(written), uhidEventSize)
	}
	start := len(written) - uhidEventSize
	return binary.LittleEndian.Uint32(written[start:]), written[start+4:]
}

func TestUHIDCodec(t *testing.T) {
	if uhidEventSize != 4376 {
		t.Fatalf("uhidEventSize = %d, want the kernel's 4376", uhidEventSize)
	}
	transport := &captureTransport{reader: bytes.NewBuffer(nil)}
	codec := &uhidCodec{rw: transport}
	descriptor := make([]byte, 273)
	for i := range descriptor {
		descriptor[i] = byte(i)
	}
	cfg := HostConfig{
		Name: "DualSense Wireless Controller", Phys: "pad2go/uhid", ID: "pad2go-abc",
		VendorID: 0x054C, ProductID: 0x0CE6, Version: 0x0100, Descriptor: descriptor,
	}
	if err := codec.createDevice(cfg); err != nil {
		t.Fatal(err)
	}
	if len(transport.written) != 4376 {
		t.Fatalf("CREATE2 wrote %d bytes, want one 4376-byte event", len(transport.written))
	}
	typ, payload := lastUHIDEvent(t, transport.written)
	if typ != uhidCreate2 {
		t.Fatalf("create type %d, want %d", typ, uhidCreate2)
	}
	if len(payload) != 4372 {
		t.Fatalf("CREATE2 payload %d, want 4372", len(payload))
	}
	if got := string(bytes.TrimRight(payload[0:128], "\x00")); got != cfg.Name {
		t.Errorf("name %q, want %q", got, cfg.Name)
	}
	if got := string(bytes.TrimRight(payload[128:192], "\x00")); got != cfg.Phys {
		t.Errorf("phys %q, want %q", got, cfg.Phys)
	}
	if got := string(bytes.TrimRight(payload[192:256], "\x00")); got != cfg.ID {
		t.Errorf("uniq %q, want %q", got, cfg.ID)
	}
	if got := binary.LittleEndian.Uint16(payload[256:258]); got != 273 {
		t.Errorf("rd_size %d, want 273", got)
	}
	if got := binary.LittleEndian.Uint16(payload[258:260]); got != uint16(uhidBusUSB) {
		t.Errorf("bus %d, want %d", got, uhidBusUSB)
	}
	if got := binary.LittleEndian.Uint32(payload[260:264]); got != 0x054C {
		t.Errorf("vendor %#x, want 0x054C", got)
	}
	if got := binary.LittleEndian.Uint32(payload[264:268]); got != 0x0CE6 {
		t.Errorf("product %#x, want 0x0CE6", got)
	}
	if got := binary.LittleEndian.Uint32(payload[268:272]); got != 0x0100 {
		t.Errorf("version %#x, want 0x0100", got)
	}
	if got := binary.LittleEndian.Uint32(payload[272:276]); got != 0 {
		t.Errorf("country %d, want 0", got)
	}
	if !bytes.Equal(payload[276:276+273], descriptor) {
		t.Error("descriptor bytes were not copied at offset 276")
	}

	report := make([]byte, 64)
	report[0] = 0x01
	if err := codec.inputReport(report); err != nil {
		t.Fatal(err)
	}
	typ, payload = lastUHIDEvent(t, transport.written)
	if typ != uhidInput2 {
		t.Fatalf("input type %d, want %d", typ, uhidInput2)
	}
	if got := binary.LittleEndian.Uint16(payload[0:2]); got != 64 {
		t.Errorf("input size %d, want 64", got)
	}
	if !bytes.Equal(payload[2:66], report) {
		t.Error("input report bytes were not copied")
	}

	if err := codec.getReportReply(7, 0, []byte{0x05, 1, 2}); err != nil {
		t.Fatal(err)
	}
	typ, payload = lastUHIDEvent(t, transport.written)
	if typ != uhidGetReportReply {
		t.Fatalf("get reply type %d, want %d", typ, uhidGetReportReply)
	}
	if got := binary.LittleEndian.Uint32(payload[0:4]); got != 7 {
		t.Errorf("request %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint16(payload[6:8]); got != 3 {
		t.Errorf("reply size %d, want 3", got)
	}
	if err := codec.setReportReply(8, 5); err != nil {
		t.Fatal(err)
	}
	typ, payload = lastUHIDEvent(t, transport.written)
	if typ != uhidSetReportReply || binary.LittleEndian.Uint32(payload[0:4]) != 8 || binary.LittleEndian.Uint16(payload[4:6]) != 5 {
		t.Fatalf("bad set report reply: type %d payload %x", typ, payload[:6])
	}
	if err := codec.destroy(); err != nil {
		t.Fatal(err)
	}
	typ, _ = lastUHIDEvent(t, transport.written)
	if typ != uhidDestroy {
		t.Fatalf("destroy type %d, want %d", typ, uhidDestroy)
	}

	readTransport := &captureTransport{reader: bytes.NewBuffer(testUHIDEvent(uhidOutput, nil))}
	readCodec := &uhidCodec{rw: readTransport}
	typ, payload, readErr := readCodec.read()
	if readErr != nil || typ != uhidOutput || len(payload) != 4372 {
		t.Fatalf("read event: type %d len %d err %v", typ, len(payload), readErr)
	}

	output := make([]byte, 4099)
	copy(output[:2], []byte{0x02, 0x03})
	binary.LittleEndian.PutUint16(output[4096:4098], 2)
	output[4098] = uhidReportOutput
	data, rtype, err := decodeUHIDOutput(output)
	if err != nil || rtype != uhidReportOutput || !bytes.Equal(data, []byte{0x02, 0x03}) {
		t.Fatalf("decode output: data=%x type=%d err=%v", data, rtype, err)
	}
	get := make([]byte, 6)
	binary.LittleEndian.PutUint32(get, 9)
	get[4], get[5] = 0x05, uhidReportFeature
	request, reportID, reportType, err := decodeUHIDGetReport(get)
	if err != nil || request != 9 || reportID != 0x05 || reportType != uhidReportFeature {
		t.Fatalf("decode get: %d %#x %d %v", request, reportID, reportType, err)
	}
	set := make([]byte, 10)
	binary.LittleEndian.PutUint32(set, 10)
	set[4], set[5] = 0x09, uhidReportFeature
	binary.LittleEndian.PutUint16(set[6:8], 2)
	set[8], set[9] = 1, 2
	request, reportID, _, data, err = decodeUHIDSetReport(set)
	if err != nil || request != 10 || reportID != 0x09 || !bytes.Equal(data, []byte{1, 2}) {
		t.Fatalf("decode set: %d %#x %x %v", request, reportID, data, err)
	}
}

func TestUHIDSingleEventRead(t *testing.T) {
	transport := &strictEventTransport{
		events: [][]byte{
			literalUHIDEvent(uhidOutput, append([]byte{0x01, 0x02}, make([]byte, 4097)...)),
			literalUHIDEvent(uhidGetReport, append([]byte{7, 0, 0, 0}, []byte{0x05, uhidReportFeature}...)),
		},
	}
	codec := &uhidCodec{rw: transport}
	for i, want := range []uint32{uhidOutput, uhidGetReport} {
		typ, payload, err := codec.read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if typ != want {
			t.Fatalf("read %d type %d, want %d", i, typ, want)
		}
		if len(payload) != 4372 {
			t.Fatalf("read %d payload %d, want 4372", i, len(payload))
		}
	}
	if transport.reads != 2 {
		t.Fatalf("transport reads %d, want one per event", transport.reads)
	}
}

const literalUHIDEventSize = 4376

// literalUHIDEvent pins the kernel size independently of uhidEventSize so a
// constant change cannot make the test agree with the code by construction.
func literalUHIDEvent(typ uint32, payload []byte) []byte {
	buf := make([]byte, literalUHIDEventSize)
	binary.LittleEndian.PutUint32(buf, typ)
	copy(buf[4:], payload)
	return buf
}

type strictEventTransport struct {
	events [][]byte
	reads  int
}

func (s *strictEventTransport) Read(p []byte) (int, error) {
	if len(p) != literalUHIDEventSize {
		return 0, fmt.Errorf("read buffer %d, want exactly one %d-byte event", len(p), literalUHIDEventSize)
	}
	if s.reads >= len(s.events) {
		return 0, io.EOF
	}
	n := copy(p, s.events[s.reads])
	s.reads++
	return n, nil
}

func (s *strictEventTransport) Write(p []byte) (int, error) { return len(p), nil }
func (s *strictEventTransport) Close() error                { return nil }

type blockingTransport struct {
	mu      sync.Mutex
	events  chan []byte
	written []byte
	closed  chan struct{}
	once    sync.Once
}

func newBlockingTransport(events ...[]byte) *blockingTransport {
	t := &blockingTransport{
		events: make(chan []byte, len(events)+1),
		closed: make(chan struct{}),
	}
	for _, event := range events {
		t.events <- event
	}
	return t
}

func (t *blockingTransport) Read(p []byte) (int, error) {
	select {
	case event := <-t.events:
		return copy(p, event), nil
	case <-t.closed:
		return 0, io.EOF
	}
}

func (t *blockingTransport) Write(p []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	t.mu.Lock()
	t.written = append(t.written, p...)
	t.mu.Unlock()
	return len(p), nil
}

func (t *blockingTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func (t *blockingTransport) snapshot() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.written...)
}

func testUHIDEvent(typ uint32, payload []byte) []byte {
	buf := make([]byte, uhidEventSize)
	binary.LittleEndian.PutUint32(buf, typ)
	copy(buf[4:], payload)
	return buf
}

type orderTransport struct {
	readStarted chan struct{}
	once        sync.Once
	reads       chan []byte
}

func (o *orderTransport) Read(p []byte) (int, error) {
	o.once.Do(func() { close(o.readStarted) })
	event, ok := <-o.reads
	if !ok {
		return 0, io.EOF
	}
	return copy(p, event), nil
}
func (o *orderTransport) Write(p []byte) (int, error) {
	if binary.LittleEndian.Uint32(p) == uhidCreate2 {
		select {
		case <-o.readStarted:
		case <-time.After(time.Second):
			return 0, errors.New("CREATE2 written before the reader started")
		}
		o.reads <- testUHIDEvent(uhidStart, nil)
	}
	return len(p), nil
}
func (o *orderTransport) Close() error {
	o.once.Do(func() {})
	select {
	case <-o.readStarted:
	default:
	}
	close(o.reads)
	return nil
}

func TestUHIDCreateOrder(t *testing.T) {
	transport := &orderTransport{readStarted: make(chan struct{}), reads: make(chan []byte, 1)}
	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg := HostConfig{
		Name: "DualSense Wireless Controller", Phys: "pad2go/uhid", ID: "pad2go-abc",
		VendorID: 0x054C, ProductID: 0x0CE6, Version: 0x0100, Descriptor: make([]byte, 273),
	}
	if err := host.Start(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUHIDGetInput(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	report := make([]byte, 64)
	report[0] = 0x01
	for i := 1; i < len(report); i++ {
		report[i] = byte(i)
	}
	if err := device.SubmitReport(report); err != nil {
		t.Fatal(err)
	}
	host.emit(HostEvent{Kind: HostGetInput, Request: 21, ReportID: 0x01})
	completion := host.receiveCompletion(t)
	if completion.err != nil {
		t.Fatalf("input GET completed with %v", completion.err)
	}
	if !bytes.Equal(completion.response, report) {
		t.Fatalf("input GET response %x, want %x", completion.response, report)
	}
}

func TestUHIDOpenErrors(t *testing.T) {
	err := classifyUHIDOpenError(fs.ErrNotExist)
	if !errors.Is(err, ErrDriverMissing) || !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "/dev/uhid") {
		t.Errorf("ENOENT -> %v, want ErrDriverMissing wrapping fs.ErrNotExist", err)
	}
	err = classifyUHIDOpenError(fs.ErrPermission)
	if !errors.Is(err, ErrPermission) || !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), "/dev/uhid") {
		t.Errorf("EACCES -> %v, want ErrPermission wrapping fs.ErrPermission", err)
	}
}

func TestWinUHIDABI(t *testing.T) {
	if got := winUHIDGetNextEvent; got != 0x0022A018 {
		t.Errorf("GET_NEXT_EVENT %#x, want 0x0022A018", got)
	}
	if got := winUHIDGetInterfaceVersion; got != 0x00222024 {
		t.Errorf("GET_INTERFACE_VERSION %#x, want 0x00222024", got)
	}
	cfg := HostConfig{VendorID: 0x054C, ProductID: 0x0CE6, Version: 0x0100, Descriptor: make([]byte, 273), SupportedEvents: supportedHostEvents}
	if got := winUHIDSetDeviceInfo; got != 0x0022A000 {
		t.Errorf("SET_DEVICE_INFO %#x, want 0x0022A000", got)
	}
	if got := winUHIDCreateDevice; got != 0x0022A010 {
		t.Errorf("CREATE_DEVICE %#x, want 0x0022A010", got)
	}
	if got := winUHIDStartDevice; got != 0x0022A014 {
		t.Errorf("START_DEVICE %#x, want 0x0022A014", got)
	}
	if got := winUHIDCompleteReadEvent; got != 0x0022A020 {
		t.Errorf("COMPLETE_READ_EVENT %#x, want 0x0022A020", got)
	}
	info := encodeWinUHIDDeviceInfo(cfg)
	if len(info) != 32 {
		t.Fatalf("device info size %d, want 32", len(info))
	}
	if binary.LittleEndian.Uint32(info[0:4]) != winUHIDVersion || binary.LittleEndian.Uint32(info[4:8]) != supportedHostEvents {
		t.Error("device info header fields are wrong")
	}
	if binary.LittleEndian.Uint16(info[8:10]) != 0x054C || binary.LittleEndian.Uint16(info[10:12]) != 0x0CE6 || binary.LittleEndian.Uint16(info[12:14]) != 0x0100 {
		t.Error("device info IDs are wrong")
	}
	if !bytes.Equal(info[16:32], make([]byte, 16)) {
		t.Error("device info ContainerId is not zero at offset 16")
	}
	if err := checkWinUHIDInterfaceVersion(0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("interface version 0 -> %v, want ErrUnsupported", err)
	}
	if err := checkWinUHIDInterfaceVersion(1); err != nil {
		t.Errorf("interface version 1 -> %v, want nil", err)
	}
	event := encodeWinUHIDCompleteRead(3, 0, []byte{1, 2, 3})
	if len(event) != 15 || binary.LittleEndian.Uint32(event[0:4]) != 3 || binary.LittleEndian.Uint32(event[8:12]) != 3 {
		t.Fatalf("bad complete-read encoding: %x", event)
	}
	write := encodeWinUHIDCompleteWrite(4, 0)
	if len(write) != 8 || binary.LittleEndian.Uint32(write[0:4]) != 4 {
		t.Fatalf("bad complete-write encoding: %x", write)
	}

	raw := make([]byte, 13+4)
	binary.LittleEndian.PutUint32(raw[0:4], winUHIDEventWriteReport)
	binary.LittleEndian.PutUint32(raw[4:8], 12)
	raw[8] = 0x02
	binary.LittleEndian.PutUint32(raw[9:13], 4)
	copy(raw[13:], []byte{0x02, 1, 2, 3})
	decoded, err := decodeWinUHIDEvent(raw)
	if err != nil || decoded.Kind != HostOutputReport || decoded.Request != 12 || decoded.ReportID != 0x02 || !bytes.Equal(decoded.Data, []byte{0x02, 1, 2, 3}) {
		t.Fatalf("decode event: %+v %v", decoded, err)
	}

	readRaw := make([]byte, 14)
	binary.LittleEndian.PutUint32(readRaw[0:4], winUHIDEventGetFeature)
	binary.LittleEndian.PutUint32(readRaw[4:8], 21)
	readRaw[8] = 0x05
	binary.LittleEndian.PutUint32(readRaw[9:13], 41)
	readEvent, err := decodeWinUHIDEvent(readRaw)
	if err != nil || readEvent.Kind != HostGetFeature || readEvent.Request != 21 || readEvent.ReportID != 0x05 || readEvent.RequestedLength != 41 || len(readEvent.Data) != 0 {
		t.Fatalf("decode read event: %+v %v", readEvent, err)
	}
}

func TestDualSenseUniqOrder(t *testing.T) {
	mac := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	if got := dualsenseUniq(mac); got != "55:44:33:22:11:02" {
		t.Fatalf("uniq %q, want the reversed form 55:44:33:22:11:02", got)
	}
	if got := dualsenseUniq([]byte{1, 2}); got != "" {
		t.Fatalf("short MAC formatted as %q, want empty", got)
	}
}

func TestWinUHIDStatusMapping(t *testing.T) {
	if got := winUHIDStatus(nil); got != 0 {
		t.Fatalf("success status %#x, want 0", got)
	}
	if got := winUHIDStatus(errors.New("boom")); got != 0xC0000001 {
		t.Fatalf("failure status %#x, want STATUS_UNSUCCESSFUL", got)
	}
}

func TestWindowsErrorClassification(t *testing.T) {
	if err := classifyWinUHIDError(nil); err != nil {
		t.Errorf("nil -> %v, want nil", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(2)); !errors.Is(err, ErrDriverMissing) {
		t.Errorf("ERROR_FILE_NOT_FOUND -> %v, want ErrDriverMissing", err)
	} else if !strings.Contains(err.Error(), "WinUHid") || !strings.Contains(err.Error(), `\\.\WinUHid`) {
		t.Errorf("missing-driver error does not name the device: %v", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(3)); !errors.Is(err, ErrDriverMissing) {
		t.Errorf("ERROR_PATH_NOT_FOUND -> %v, want ErrDriverMissing", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(5)); !errors.Is(err, ErrPermission) {
		t.Errorf("ERROR_ACCESS_DENIED -> %v, want ErrPermission", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(87)); err == nil ||
		errors.Is(err, ErrDriverMissing) || errors.Is(err, ErrPermission) ||
		!strings.Contains(err.Error(), "WinUHid") {
		t.Errorf("unexpected errno -> %v, want a wrapped non-sentinel error", err)
	}
}

func TestCloseLifecycle(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := device.SubmitReport(neutralReport()); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close: %v, want ErrClosed", err)
	}
	select {
	case <-device.Done():
	default:
		t.Fatal("Done is not closed")
	}
	if _, ok := <-device.Feedback(); ok {
		t.Fatal("Feedback is still open")
	}
	if _, ok := <-device.Control(); ok {
		t.Fatal("Control is still open")
	}
	host.mu.Lock()
	closed := host.closed
	host.mu.Unlock()
	if !closed {
		t.Fatal("host was not closed")
	}
}

type lateHost struct{ *fakeHost }

func (h *lateHost) StopEvents() error {
	h.emit(HostEvent{Kind: HostGetFeature, Request: 42, ReportID: 0x05})
	return h.fakeHost.StopEvents()
}

func TestCloseDrainsLateHostEvent(t *testing.T) {
	host := &lateHost{fakeHost: newFakeHost()}
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	completion := host.receiveCompletion(t)
	if completion.event.Request != 42 {
		t.Fatalf("drained request %d, want 42", completion.event.Request)
	}
	if !errors.Is(completion.err, ErrClosed) {
		t.Fatalf("late completion error %v, want ErrClosed", completion.err)
	}
}

func TestFitReadData(t *testing.T) {
	if got := fitReadData([]byte{1, 2, 3}, 5); !bytes.Equal(got, []byte{1, 2, 3, 0, 0}) {
		t.Fatalf("pad = %x, want 0102030000", got)
	}
	if got := fitReadData([]byte{1, 2, 3}, 2); !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("truncate = %x, want 0102", got)
	}
	if got := fitReadData([]byte{1, 2, 3}, 0); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("unbounded = %x, want 010203", got)
	}
}

func TestWinUHIDCompleteReadPayload(t *testing.T) {
	payload := winUHIDCompleteReadPayload(HostEvent{Request: 7, RequestedLength: 5}, []byte{1, 2, 3}, 0)
	if len(payload) != 17 || binary.LittleEndian.Uint32(payload[0:4]) != 7 ||
		binary.LittleEndian.Uint32(payload[4:8]) != 0 || binary.LittleEndian.Uint32(payload[8:12]) != 5 ||
		!bytes.Equal(payload[12:], []byte{1, 2, 3, 0, 0}) {
		t.Fatalf("padded complete-read payload %x", payload)
	}
	payload = winUHIDCompleteReadPayload(HostEvent{Request: 8, RequestedLength: 2}, []byte{1, 2, 3}, 0)
	if len(payload) != 14 || binary.LittleEndian.Uint32(payload[8:12]) != 2 ||
		!bytes.Equal(payload[12:], []byte{1, 2}) {
		t.Fatalf("truncated complete-read payload %x", payload)
	}
	payload = winUHIDCompleteReadPayload(HostEvent{Request: 9}, nil, 0)
	if len(payload) != 13 || binary.LittleEndian.Uint32(payload[8:12]) != 0 {
		t.Fatalf("zero-length complete-read payload %x, want the 13-byte driver minimum", payload)
	}
	payload = winUHIDCompleteReadPayload(HostEvent{Request: 10}, nil, 0xC0000001)
	if binary.LittleEndian.Uint32(payload[4:8]) != 0xC0000001 {
		t.Fatalf("status %#x, want 0xC0000001", binary.LittleEndian.Uint32(payload[4:8]))
	}
}

func TestSubmitErrorIsNotClosed(t *testing.T) {
	host := newFakeHost()
	device, err := Open(DualSenseWired, Options{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	host.mu.Lock()
	host.submitErr = errors.New("boom")
	host.mu.Unlock()
	err = device.SubmitReport(neutralReport())
	if errors.Is(err, ErrClosed) {
		t.Fatalf("submit error %v must not be ErrClosed", err)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("submit error %v, want the host cause", err)
	}
	select {
	case <-device.Done():
		t.Fatal("failed submit closed the device")
	default:
	}

	// A failed submit must not become the GET_INPUT cache.
	host.emit(HostEvent{Kind: HostGetInput, Request: 1, ReportID: 0x01})
	completion := host.receiveCompletion(t)
	if !bytes.Equal(completion.response, neutralReport()) {
		t.Fatalf("GET_INPUT after failed submit = %x, want the neutral report", completion.response)
	}

	host.mu.Lock()
	host.submitErr = nil
	host.mu.Unlock()
	marker := neutralReport()
	marker[1] = 0x7E
	if err := device.SubmitReport(marker); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	host.submitErr = errors.New("boom")
	host.mu.Unlock()
	failed := neutralReport()
	failed[1] = 0x7F
	if err := device.SubmitReport(failed); err == nil {
		t.Fatal("expected the second submit to fail")
	}
	host.emit(HostEvent{Kind: HostGetInput, Request: 2, ReportID: 0x01})
	completion = host.receiveCompletion(t)
	if !bytes.Equal(completion.response, marker) {
		t.Fatalf("GET_INPUT after failed submit = %x, want the last successful report %x", completion.response, marker)
	}
}

func TestConcurrentSubmitClose(t *testing.T) {
	for round := 0; round < 50; round++ {
		host := newFakeHost()
		device, err := Open(DualSenseWired, Options{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = device.SubmitReport(neutralReport())
			}()
		}
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = device.Close()
			}()
		}
		wg.Wait()
	}
}

func TestConcurrentEmitClose(t *testing.T) {
	for round := 0; round < 25; round++ {
		host := newEmittingHost()
		device, err := Open(DualSenseWired, Options{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		report := validOutputReport(1)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-device.Done():
						return
					case <-host.stop:
						return
					case host.events <- HostEvent{Kind: HostOutputReport, ReportID: 0x02, Data: report}:
					}
				}
			}()
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = device.SubmitReport(neutralReport())
			}()
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = device.Close()
			}()
		}
		wg.Wait()
	}
}

type emittingHost struct {
	events chan HostEvent
	stop   chan struct{}
	once   sync.Once
}

func newEmittingHost() *emittingHost {
	return &emittingHost{events: make(chan HostEvent, 32), stop: make(chan struct{})}
}

func (h *emittingHost) Start(context.Context, HostConfig) error { return nil }
func (h *emittingHost) Events() <-chan HostEvent                { return h.events }
func (h *emittingHost) SubmitReport(context.Context, []byte) error {
	return nil
}
func (h *emittingHost) Complete(context.Context, HostEvent, []byte, error) error { return nil }
func (h *emittingHost) StopEvents() error {
	h.once.Do(func() { close(h.stop) })
	return nil
}
func (h *emittingHost) Close() error {
	_ = h.StopEvents()
	return nil
}

func TestUHIDHostRequestKinds(t *testing.T) {
	transport := &captureTransport{reader: bytes.NewBuffer(nil)}
	write := func(typ uint32, payload []byte) {
		transport.reader.Write(literalUHIDEvent(typ, payload))
	}
	write(uhidStart, nil)

	get := make([]byte, 8)
	binary.LittleEndian.PutUint32(get[0:4], 21)
	get[4], get[5] = 0x01, uhidReportInput
	write(uhidGetReport, get)

	set := make([]byte, 10)
	binary.LittleEndian.PutUint32(set[0:4], 22)
	set[4], set[5] = 0x02, uhidReportOutput
	binary.LittleEndian.PutUint16(set[6:8], 1)
	set[8] = 0x02
	write(uhidSetReport, set)

	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	if err := host.Start(context.Background(), HostConfig{
		Name: "DualSense", Phys: "pad2go/uhid", ID: "pad2go-test",
		VendorID: 0x054C, ProductID: 0x0CE6, Version: 0x0100,
		Descriptor: []byte{0x05, 0x01},
	}); err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	seen := make(map[HostEventKind]HostEvent)
	for len(seen) < 2 {
		select {
		case event, ok := <-host.Events():
			if !ok {
				t.Fatalf("host event stream closed with %+v", seen)
			}
			seen[event.Kind] = event
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for host events; got %+v", seen)
		}
	}
	input, ok := seen[HostGetInput]
	if !ok || input.Request != 21 || input.ReportID != 0x01 {
		t.Fatalf("input GET_REPORT mapped to %+v (present %t)", input, ok)
	}
	output, ok := seen[HostOutputReport]
	if !ok || output.Request != 22 || output.ReportID != 0x02 || !bytes.Equal(output.Data, []byte{0x02}) {
		t.Fatalf("output SET_REPORT mapped to %+v (present %t)", output, ok)
	}

	base := len(transport.written)
	ctx := context.Background()
	if err := host.Complete(ctx, input, []byte{0x01, 0xAA}, nil); err != nil {
		t.Fatal(err)
	}
	// An output-type SET_REPORT must answer the kernel's request.
	if err := host.Complete(ctx, output, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := host.Complete(ctx, HostEvent{Kind: HostSetFeature, Request: 40}, nil, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if err := host.Complete(ctx, HostEvent{Kind: HostGetFeature, Request: 41}, nil, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if err := host.Complete(ctx, HostEvent{Kind: HostOutputReport}, nil, nil); err != nil {
		t.Fatal(err)
	}

	written := transport.written[base:]
	if len(written) != 4*literalUHIDEventSize {
		t.Fatalf("wrote %d reply bytes, want 4 events", len(written))
	}
	replies := make([][]byte, 0, 4)
	types := make([]uint32, 0, 4)
	for offset := 0; offset < len(written); offset += literalUHIDEventSize {
		types = append(types, binary.LittleEndian.Uint32(written[offset:]))
		replies = append(replies, written[offset+4:offset+literalUHIDEventSize])
	}
	if types[0] != uhidGetReportReply || binary.LittleEndian.Uint32(replies[0][0:4]) != 21 ||
		binary.LittleEndian.Uint16(replies[0][4:6]) != 0 || binary.LittleEndian.Uint16(replies[0][6:8]) != 2 ||
		!bytes.Equal(replies[0][8:10], []byte{0x01, 0xAA}) {
		t.Fatalf("input reply type %d payload %x", types[0], replies[0][:10])
	}
	if types[1] != uhidSetReportReply || binary.LittleEndian.Uint32(replies[1][0:4]) != 22 ||
		binary.LittleEndian.Uint16(replies[1][4:6]) != 0 {
		t.Fatalf("output SET_REPORT reply type %d payload %x", types[1], replies[1][:6])
	}
	if types[2] != uhidSetReportReply || binary.LittleEndian.Uint32(replies[2][0:4]) != 40 ||
		binary.LittleEndian.Uint16(replies[2][4:6]) != 5 {
		t.Fatalf("failed SET reply type %d payload %x", types[2], replies[2][:6])
	}
	if types[3] != uhidGetReportReply || binary.LittleEndian.Uint32(replies[3][0:4]) != 41 ||
		binary.LittleEndian.Uint16(replies[3][4:6]) != 5 {
		t.Fatalf("failed GET reply type %d payload %x", types[3], replies[3][:8])
	}

	// Request id zero is legal after the kernel counter wraps; NeedsReply must
	// drive the reply instead of a non-zero sentinel.
	base = len(transport.snapshot())
	if err := host.Complete(ctx, HostEvent{Kind: HostOutputReport, NeedsReply: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	written = transport.snapshot()[base:]
	if len(written) != literalUHIDEventSize {
		t.Fatalf("request-id-zero reply wrote %d bytes, want one event", len(written))
	}
	if typ := binary.LittleEndian.Uint32(written); typ != uhidSetReportReply ||
		binary.LittleEndian.Uint32(written[4:8]) != 0 {
		t.Fatalf("request-id-zero reply type %d id %d", typ, binary.LittleEndian.Uint32(written[4:8]))
	}
}

func TestUHIDStartCanceledAfterOpenClosesTransport(t *testing.T) {
	transport := &captureTransport{reader: bytes.NewBuffer(nil)}
	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := host.Start(ctx, HostConfig{
		Name: "DualSense", Phys: "pad2go/uhid", ID: "pad2go-5e7",
		Descriptor: []byte{0x05, 0x01},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start with canceled ctx = %v, want context.Canceled", err)
	}
	if !transport.isClosed() {
		t.Fatal("transport opened after cancellation was not closed")
	}
}

func TestUHIDStartReaderError(t *testing.T) {
	transport := &captureTransport{reader: bytes.NewBuffer(nil)}
	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	start := time.Now()
	err := host.Start(context.Background(), HostConfig{
		Name: "DualSense", Phys: "pad2go/uhid", ID: "pad2go-5e7",
		Descriptor: []byte{0x05, 0x01},
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Start with an EOF reader = %v, want io.EOF", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Start waited %s for the reader error, want under one second", elapsed)
	}
}

func TestUHIDStartAfterStopClosesTransport(t *testing.T) {
	transport := &captureTransport{reader: bytes.NewBuffer(nil)}
	opened := make(chan struct{})
	release := make(chan struct{})
	host := newUHIDHost(func() (io.ReadWriteCloser, error) {
		close(opened)
		<-release
		return transport, nil
	})
	result := make(chan error, 1)
	go func() {
		result <- host.Start(context.Background(), HostConfig{
			Name: "DualSense", Phys: "pad2go/uhid", ID: "pad2go-5e7",
			Descriptor: []byte{0x05, 0x01},
		})
	}()
	<-opened
	if err := host.StopEvents(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Stop = %v, want ErrClosed", err)
	}
	if !transport.isClosed() {
		t.Fatal("late-opened transport was not closed")
	}
}

func TestUHIDHostCloseDestroys(t *testing.T) {
	transport := &captureTransport{reader: bytes.NewBuffer(literalUHIDEvent(uhidStart, nil))}
	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	if err := host.Start(context.Background(), HostConfig{
		Name: "DualSense", Phys: "pad2go/uhid", ID: "pad2go-test",
		VendorID: 0x054C, ProductID: 0x0CE6, Version: 0x0100,
		Descriptor: []byte{0x05, 0x01},
	}); err != nil {
		t.Fatal(err)
	}
	base := len(transport.written)
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	written := transport.written[base:]
	if len(written) != literalUHIDEventSize {
		t.Fatalf("Close wrote %d bytes, want one DESTROY event", len(written))
	}
	if typ := binary.LittleEndian.Uint32(written); typ != uhidDestroy {
		t.Fatalf("Close wrote type %d, want DESTROY %d", typ, uhidDestroy)
	}
}

func TestUHIDOutputSetReportReplies(t *testing.T) {
	set := make([]byte, 8)
	binary.LittleEndian.PutUint32(set[0:4], 22)
	set[4], set[5] = 0x02, uhidReportOutput
	output := validOutputReport(9)
	binary.LittleEndian.PutUint16(set[6:8], uint16(len(output)))
	set = append(set, output...)
	transport := newBlockingTransport(
		literalUHIDEvent(uhidStart, nil),
		literalUHIDEvent(uhidSetReport, set),
	)

	host := newUHIDHost(func() (io.ReadWriteCloser, error) { return transport, nil })
	device, err := Open(DualSenseWired, Options{Host: host, ID: "pad2go-5e7"})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	select {
	case feedback := <-device.Feedback():
		if !feedback.Known || len(feedback.Raw) == 0 || feedback.Raw[0] != reportOutputID {
			t.Fatalf("output feedback = %+v", feedback)
		}
	case <-time.After(time.Second):
		t.Fatal("output report did not reach Feedback")
	}
	reply := findUHIDReply(t, transport.snapshot(), uhidSetReportReply, 22)
	if errno := binary.LittleEndian.Uint16(reply[4:6]); errno != 0 {
		t.Fatalf("SET_REPORT reply errno %d, want 0", errno)
	}
}

func findUHIDReply(t *testing.T, written []byte, typ, request uint32) []byte {
	t.Helper()
	for offset := 0; offset+literalUHIDEventSize <= len(written); offset += literalUHIDEventSize {
		if binary.LittleEndian.Uint32(written[offset:]) == typ &&
			binary.LittleEndian.Uint32(written[offset+4:]) == request {
			return written[offset+4 : offset+literalUHIDEventSize]
		}
	}
	t.Fatalf("no UHID type %d reply for request %d in %d written bytes", typ, request, len(written))
	return nil
}

func TestCloseDrainsPendingEvents(t *testing.T) {
	host := newFakeHost()
	host.emit(HostEvent{Kind: HostGetFeature, Request: 9, ReportID: 0x05})
	// Close stops the host reader and closes its channel before the core drains,
	// so no new request can arrive between the drain and host shutdown.
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	drainHostEvents(host, context.Background())
	completion := host.receiveCompletion(t)
	if !errors.Is(completion.err, ErrClosed) {
		t.Fatalf("drained completion error %v, want ErrClosed", completion.err)
	}
}

func TestHostEventsStable(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("no native host on this platform")
	}
	host, err := openPlatformHost()
	if err != nil {
		t.Fatal(err)
	}
	if host.Events() == nil {
		t.Fatal("Events() is nil before Start")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

func winUHIDTestEvent(eventType uint32, request uint32, reportID byte, data []byte) []byte {
	raw := make([]byte, 13+len(data))
	binary.LittleEndian.PutUint32(raw[0:4], eventType)
	binary.LittleEndian.PutUint32(raw[4:8], request)
	raw[8] = reportID
	binary.LittleEndian.PutUint32(raw[9:13], uint32(len(data)))
	copy(raw[13:], data)
	return raw
}

func TestWinUHIDReadLoopStopsWhenDone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	calls := 0
	next := func() ([]byte, error) {
		calls++
		return nil, syscall.Errno(1460)
	}
	if err := winUHIDReadLoop(next, make(chan HostEvent, 1), done, nil); err != nil {
		t.Fatalf("closed done returned %v", err)
	}
	if calls != 0 {
		t.Fatalf("next called %d times after done closed", calls)
	}

	done = make(chan struct{})
	calls = 0
	next = func() ([]byte, error) {
		calls++
		if calls == 1 {
			close(done)
			return nil, syscall.Errno(1460)
		}
		return nil, io.EOF
	}
	if err := winUHIDReadLoop(next, make(chan HostEvent, 1), done, nil); err != nil {
		t.Fatalf("timeout after done returned %v", err)
	}
	if calls != 1 {
		t.Fatalf("next called %d times, want one", calls)
	}
}

func TestWinUHIDBadWriteCompletes(t *testing.T) {
	raw := winUHIDTestEvent(winUHIDEventWriteReport, 31, 0x02, []byte{0x02})
	binary.LittleEndian.PutUint32(raw[9:13], 4096) // claims more data than the driver sent
	calls := 0
	next := func() ([]byte, error) {
		calls++
		if calls == 1 {
			return raw, nil
		}
		return nil, io.EOF
	}
	var failed HostEvent
	var failedErr error
	err := winUHIDReadLoop(next, make(chan HostEvent, 1), make(chan struct{}), func(event HostEvent, err error) {
		failed, failedErr = event, err
	})
	if !errors.Is(err, ErrBadReport) {
		t.Fatalf("read loop returned %v, want ErrBadReport", err)
	}
	if failedErr == nil || failed.Kind != HostOutputReport || failed.Request != 31 || failed.ReportID != 0x02 {
		t.Fatalf("failed request was not completed: %+v %v", failed, failedErr)
	}
}

func TestWinUHIDTimeoutNonFatal(t *testing.T) {
	calls := 0
	next := func() ([]byte, error) {
		calls++
		switch calls {
		case 1:
			return nil, syscall.Errno(1460)
		case 2:
			return winUHIDTestEvent(winUHIDEventWriteReport, 1, 0x02, []byte{0x02, 0, 0}), nil
		default:
			return nil, io.EOF
		}
	}
	events := make(chan HostEvent, 1)
	done := make(chan struct{})
	err := winUHIDReadLoop(next, events, done, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read loop returned %v, want io.EOF", err)
	}
	select {
	case event := <-events:
		if event.Kind != HostOutputReport || event.ReportID != 0x02 {
			t.Fatalf("event %+v, want output report", event)
		}
	default:
		t.Fatal("timeout stopped the event loop")
	}
}
