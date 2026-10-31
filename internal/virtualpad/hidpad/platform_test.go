package hidpad

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

type captureTransport struct {
	written []byte
	reader  *bytes.Buffer
}

func (c *captureTransport) Write(p []byte) (int, error) {
	c.written = append(c.written, p...)
	return len(p), nil
}
func (c *captureTransport) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *captureTransport) Close() error              { return nil }

func lastUHIDEvent(t *testing.T, written []byte) (uint32, []byte) {
	t.Helper()
	if len(written) < uhidEventSize {
		t.Fatalf("wrote %d bytes, want at least one %d-byte event", len(written), uhidEventSize)
	}
	start := len(written) - uhidEventSize
	return binary.LittleEndian.Uint32(written[start:]), written[start+4:]
}

func TestUHIDCodec(t *testing.T) {
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
	typ, payload := lastUHIDEvent(t, transport.written)
	if typ != uhidCreate2 {
		t.Fatalf("create type %d, want %d", typ, uhidCreate2)
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
	if !bytes.Equal(payload[258:258+273], descriptor) {
		t.Error("descriptor bytes were not copied")
	}
	if got := binary.LittleEndian.Uint32(payload[4354:4358]); got != uhidBusUSB {
		t.Errorf("bus %d, want %d", got, uhidBusUSB)
	}
	if got := binary.LittleEndian.Uint32(payload[4358:4362]); got != 0x054C {
		t.Errorf("vendor %#x, want 0x054C", got)
	}
	if got := binary.LittleEndian.Uint32(payload[4362:4366]); got != 0x0CE6 {
		t.Errorf("product %#x, want 0x0CE6", got)
	}
	if got := binary.LittleEndian.Uint32(payload[4366:4370]); got != 0x0100 {
		t.Errorf("version %#x, want 0x0100", got)
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
	if err := classifyUHIDOpenError(fs.ErrNotExist); !errors.Is(err, ErrDriverMissing) {
		t.Errorf("ENOENT -> %v, want ErrDriverMissing", err)
	}
	if err := classifyUHIDOpenError(fs.ErrPermission); !errors.Is(err, ErrPermission) {
		t.Errorf("EACCES -> %v, want ErrPermission", err)
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
	info := encodeWinUHIDDeviceInfo(cfg)
	if len(info) != 30 {
		t.Fatalf("device info size %d, want 30", len(info))
	}
	if binary.LittleEndian.Uint32(info[0:4]) != winUHIDVersion || binary.LittleEndian.Uint32(info[4:8]) != supportedHostEvents {
		t.Error("device info header fields are wrong")
	}
	if binary.LittleEndian.Uint16(info[8:10]) != 0x054C || binary.LittleEndian.Uint16(info[10:12]) != 0x0CE6 || binary.LittleEndian.Uint16(info[12:14]) != 0x0100 {
		t.Error("device info IDs are wrong")
	}
	packed := encodeWinUHIDConfig(cfg)
	wantSize := 56
	if strconv.IntSize == 32 {
		wantSize = 44
	}
	if len(packed) != wantSize {
		t.Fatalf("config size %d, want %d", len(packed), wantSize)
	}
	if binary.LittleEndian.Uint16(packed[10:12]) != 273 {
		t.Errorf("config descriptor length %d, want 273", binary.LittleEndian.Uint16(packed[10:12]))
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
}

func TestWindowsErrorClassification(t *testing.T) {
	if err := classifyWinUHIDError(syscall.Errno(2)); !errors.Is(err, ErrDriverMissing) {
		t.Errorf("ERROR_FILE_NOT_FOUND -> %v, want ErrDriverMissing", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(3)); !errors.Is(err, ErrDriverMissing) {
		t.Errorf("ERROR_PATH_NOT_FOUND -> %v, want ErrDriverMissing", err)
	}
	if err := classifyWinUHIDError(syscall.Errno(5)); !errors.Is(err, ErrPermission) {
		t.Errorf("ERROR_ACCESS_DENIED -> %v, want ErrPermission", err)
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
}

var _ = io.EOF
