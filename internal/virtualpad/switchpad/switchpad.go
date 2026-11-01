// Package switchpad exposes a virtual Switch Pro Controller through WinUHid
// on Windows or /dev/uhid on Linux. It speaks the report-level protocol that
// SDL's HIDAPI Switch driver (and therefore yuzu, Ryujinx and their forks)
// expects: 64-byte input/output reports and the 0x01/0x21 subcommand handshake,
// including SPI calibration reads.
package switchpad

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad/hidpad/hidreport"
)

const (
	// Nintendo's Switch Pro Controller identity. Emulators and SDL key their
	// Switch driver off this VID/PID pair.
	vendorID  uint16 = 0x057E
	productID uint16 = 0x2009
	version   uint16 = 0x0200

	report30 = 0x30 // standard full input report
	report21 = 0x21 // subcommand reply
	report81 = 0x81 // USB command acknowledgement
	report01 = 0x01 // rumble + subcommand
	report10 = 0x10 // rumble only
	report80 = 0x80 // USB/proprietary command

	reportSize   = 64
	startTimeout = 2 * time.Second
)

// Rumble is one decoded output report: the two Switch rumble amplitudes,
// scaled to 0-255 for the physical controller.
type Rumble struct {
	Left  uint8
	Right uint8
}

// Feedback is one host output report with its decoded rumble, if any.
type Feedback struct {
	Rumble *Rumble
}

// Options configures one virtual device. Host is intended for tests; application
// code should leave it nil.
type Options struct {
	ID   string
	Host hidpad.Host
}

// Device is one open virtual Switch Pro Controller.
type Device struct {
	id   string
	mac  []byte
	host hidpad.Host

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	closed   bool
	failure  error
	last     []byte
	submits  sync.WaitGroup
	submitMu sync.Mutex
	counter  byte

	closeOnce sync.Once
	closeErr  error

	feedback chan Feedback
	loopDone chan struct{}
}

var identities = struct {
	sync.Mutex
	ids map[string]struct{}
}{ids: make(map[string]struct{})}

// Open starts a virtual Pro Controller and submits its neutral 0x30 report.
func Open(opts Options) (*Device, error) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		return nil, hidpad.ErrUnsupported
	}
	id, err := reserveID(opts.ID)
	if err != nil {
		return nil, err
	}
	mac := identityMAC(id)

	host := opts.Host
	if host == nil {
		host, err = hidpad.OpenPlatformHost()
		if err != nil {
			releaseID(id)
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &Device{
		id:       id,
		mac:      mac,
		host:     host,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		feedback: make(chan Feedback, 16),
		loopDone: make(chan struct{}),
		last:     neutral30(),
	}
	config := hidpad.HostConfig{
		Name:            "Pro Controller",
		Phys:            "pad2go/switch",
		ID:              id,
		VendorID:        vendorID,
		ProductID:       productID,
		Version:         version,
		Descriptor:      hidreport.BuildSwitchPro(),
		NeutralReport:   neutral30(),
		SupportedEvents: 0x02 | 0x04, // SET_FEATURE, WRITE_REPORT
	}
	go d.eventLoop()

	started := make(chan error, 1)
	go func() { started <- host.Start(ctx, config) }()
	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	select {
	case err = <-started:
		if err != nil {
			_ = d.Close()
			return nil, fmt.Errorf("start Switch virtual HID: %w", err)
		}
	case <-timer.C:
		_ = d.Close()
		return nil, hidpad.ErrStartTimeout
	case <-d.done:
		_ = d.Close()
		return nil, fmt.Errorf("start Switch virtual HID: %w", d.failureError())
	}

	if err := d.SubmitReport(neutral30()); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("submit neutral report: %w", err)
	}
	return d, nil
}

func supportedPlatform(goos, arch string) bool {
	switch goos {
	case "windows":
		return arch == "amd64" || arch == "arm64"
	case "linux":
		return true
	default:
		return false
	}
}

func reserveID(requested string) (string, error) {
	if requested != "" {
		if !validDeviceID(requested) {
			return "", fmt.Errorf("%w: device ID must match pad2go-<lowercase hex>", hidpad.ErrBadFeature)
		}
		identities.Lock()
		defer identities.Unlock()
		if _, exists := identities.ids[requested]; exists {
			return "", fmt.Errorf("%w: duplicate device ID %q", hidpad.ErrBadFeature, requested)
		}
		identities.ids[requested] = struct{}{}
		return requested, nil
	}
	for {
		buf := make([]byte, 12)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate device identity: %w", err)
		}
		id := "pad2go-" + hex.EncodeToString(buf)
		identities.Lock()
		if _, exists := identities.ids[id]; !exists {
			identities.ids[id] = struct{}{}
			identities.Unlock()
			return id, nil
		}
		identities.Unlock()
	}
}

func validDeviceID(id string) bool {
	const prefix = "pad2go-"
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		return false
	}
	for _, c := range id[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func releaseID(id string) {
	identities.Lock()
	delete(identities.ids, id)
	identities.Unlock()
}

// identityMAC derives a locally administered unicast MAC from the device ID.
// The 02:50:32 prefix marks pad2go virtual devices.
func identityMAC(id string) []byte {
	sum := sha256.Sum256([]byte(id))
	mac := append([]byte(nil), sum[:6]...)
	mac[0] = 0x02
	mac[1], mac[2] = 0x50, 0x32
	return mac
}

// ID is the stable WinUHid instance ID / UHID uniq value.
func (d *Device) ID() string { return d.id }

// Feedback streams decoded rumble output reports.
func (d *Device) Feedback() <-chan Feedback { return d.feedback }

// Done closes when the device stops.
func (d *Device) Done() <-chan struct{} { return d.done }

// SubmitReport sends one 64-byte 0x30 input report.
func (d *Device) SubmitReport(report []byte) error {
	if len(report) != reportSize || report[0] != report30 {
		return fmt.Errorf("%w: input report must be %d bytes and start with 0x30", hidpad.ErrBadReport, reportSize)
	}
	d.submitMu.Lock()
	defer d.submitMu.Unlock()
	return d.submitLocked(report)
}

func (d *Device) submitLocked(report []byte) error {
	d.mu.Lock()
	if d.closed {
		err := d.failure
		d.mu.Unlock()
		if err != nil {
			return err
		}
		return hidpad.ErrClosed
	}
	d.submits.Add(1)
	d.mu.Unlock()
	defer d.submits.Done()

	copied := append([]byte(nil), report...)
	if copied[0] == report30 || copied[0] == report21 {
		copied[1] = d.counter
		d.counter++
	}
	if err := d.host.SubmitReport(d.ctx, copied); err != nil {
		d.mu.Lock()
		cause := d.failure
		d.mu.Unlock()
		if cause != nil {
			return fmt.Errorf("submit input report: %w", cause)
		}
		return fmt.Errorf("submit input report: %w", err)
	}
	if copied[0] == report30 {
		d.mu.Lock()
		d.last = append(d.last[:0], copied...)
		d.mu.Unlock()
	}
	return nil
}

// Close stops the device and releases its identity. A second call is a no-op.
func (d *Device) Close() error {
	d.beginClose(nil)
	d.closeOnce.Do(func() {
		d.submits.Wait()
		<-d.loopDone
		_ = d.host.StopEvents()
		drainHostEvents(d.host, context.Background())
		d.closeErr = d.host.Close()
		releaseID(d.id)
	})
	return d.closeErr
}

func drainHostEvents(host hidpad.Host, ctx context.Context) {
	events := host.Events()
	if events == nil {
		return
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			_ = host.Complete(ctx, event, nil, hidpad.ErrClosed)
		default:
			return
		}
	}
}

func (d *Device) beginClose(cause error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	d.failure = cause
	d.cancel()
	close(d.feedback)
	close(d.done)
}

func (d *Device) failureError() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failure != nil {
		return d.failure
	}
	return hidpad.ErrClosed
}

func (d *Device) eventLoop() {
	defer close(d.loopDone)
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-d.host.Events():
			if !ok {
				cause := errors.New("virtual HID host event stream closed")
				if hostErr, ok := d.host.(interface{ Err() error }); ok {
					if err := hostErr.Err(); err != nil {
						cause = err
					}
				}
				d.beginClose(cause)
				go d.Close()
				return
			}
			if err := d.handleEvent(event); err != nil {
				d.beginClose(fmt.Errorf("virtual HID host event: %w", err))
				go d.Close()
				return
			}
		}
	}
}

func (d *Device) handleEvent(event hidpad.HostEvent) error {
	event.Data = append([]byte(nil), event.Data...)
	completion := event
	completion.Data = append([]byte(nil), event.Data...)

	switch event.Kind {
	case hidpad.HostOutputReport:
		reply, feedback := d.handleOutput(event)
		if err := d.host.Complete(d.ctx, completion, nil, nil); err != nil {
			return err
		}
		if reply != nil {
			d.submitMu.Lock()
			err := d.submitLocked(reply)
			d.submitMu.Unlock()
			if err != nil {
				return err
			}
		}
		if feedback != nil {
			d.emit(*feedback)
		}
		return nil
	case hidpad.HostGetInput:
		d.mu.Lock()
		last := append([]byte(nil), d.last...)
		d.mu.Unlock()
		return d.host.Complete(d.ctx, completion, last, nil)
	case hidpad.HostGetFeature:
		return d.host.Complete(d.ctx, completion, nil, fmt.Errorf("%w: 0x%02x", hidpad.ErrUnsupportedFeature, event.ReportID))
	case hidpad.HostSetFeature:
		return d.host.Complete(d.ctx, completion, nil, nil)
	default:
		return d.host.Complete(d.ctx, completion, nil, fmt.Errorf("%w: host event kind %d", hidpad.ErrUnsupported, event.Kind))
	}
}

func (d *Device) emit(feedback Feedback) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	select {
	case d.feedback <- feedback:
	default:
		select {
		case <-d.feedback:
		default:
		}
		select {
		case d.feedback <- feedback:
		default:
		}
	}
}

func (d *Device) handleOutput(event hidpad.HostEvent) ([]byte, *Feedback) {
	id, body := splitOutput(event)
	switch id {
	case report01:
		if len(body) < 10 {
			return nil, nil
		}
		feedback := decodeRumble(body[1:9])
		return d.reply(body[9], body[10:]), feedback
	case report10:
		if len(body) < 9 {
			return nil, nil
		}
		return nil, decodeRumble(body[1:9])
	case report80:
		if len(body) == 0 {
			return nil, nil
		}
		return d.proprietary(body), nil
	default:
		return nil, nil
	}
}

// splitOutput normalizes the two host event shapes: a full report including
// its ID (UHID output, WinUHID write) and a report body without the ID (UHID
// SET_REPORT). A 49- or 64-byte buffer includes the ID; its 48/63-byte
// counterpart is the same report without it.
func splitOutput(event hidpad.HostEvent) (byte, []byte) {
	data := event.Data
	id := event.ReportID
	switch len(data) {
	case 49, 64:
		id = data[0]
		data = data[1:]
	case 48, 63:
		// Body already starts at the packet number.
	default:
		if id != 0 && len(data) > 0 && data[0] == id {
			data = data[1:]
		} else if id == 0 && len(data) > 0 {
			id = data[0]
			data = data[1:]
		}
	}
	return id, data
}

func decodeRumble(b []byte) *Feedback {
	if len(b) < 8 {
		return nil
	}
	left := rumbleAmplitude(b[0:4])
	right := rumbleAmplitude(b[4:8])
	if left == 0 && right == 0 {
		return nil
	}
	return &Feedback{Rumble: &Rumble{Left: left, Right: right}}
}

// rumbleAmplitude undoes the neutral offsets of one 4-byte HD-rumble frame:
// the high-band amplitude is 1 at rest, the low-band amplitude is 0x40.
func rumbleAmplitude(b []byte) uint8 {
	high := int(b[1]&0x7F) - 1
	if high < 0 {
		high = 0
	}
	low := (int(b[2]&0x80)<<1 | int(b[3])) - 0x40
	if low < 0 {
		low = 0
	}
	value := max(high*255/200, low*255/136)
	if value > 255 {
		value = 255
	}
	return uint8(value)
}

// proprietary answers the USB command set. Returning nil means the command has
// no acknowledgement (ForceUSB), which is what SDL expects.
func (d *Device) proprietary(body []byte) []byte {
	command := body[0]
	switch command {
	case 0x04:
		return nil
	case 0x01:
		reply := make([]byte, reportSize)
		reply[0], reply[1], reply[3] = report81, command, 0x03
		for i := 0; i < 6; i++ {
			reply[4+i] = d.mac[5-i]
		}
		return reply
	case 0x02, 0x03, 0x05, 0x06:
		reply := make([]byte, reportSize)
		reply[0], reply[1] = report81, command
		return reply
	default:
		return nil
	}
}

func (d *Device) reply(subcommand byte, args []byte) []byte {
	reply := make([]byte, reportSize)
	d.mu.Lock()
	if len(d.last) >= 13 {
		copy(reply[1:13], d.last[1:13])
	} else {
		copy(reply[1:13], neutral30()[1:13])
	}
	d.mu.Unlock()
	reply[0] = report21
	reply[13] = 0x80
	reply[14] = subcommand
	payload := reply[15:]

	switch subcommand {
	case 0x02: // request device info
		reply[13] = 0x82
		payload[0], payload[1] = 0x03, 0x48
		payload[2], payload[3] = 0x03, 0x02
		copy(payload[4:10], d.mac)
		payload[10], payload[11] = 0x01, 0x01
	case 0x10: // SPI flash read
		reply[13] = 0x90
		if len(args) < 5 {
			break
		}
		address := binary.LittleEndian.Uint32(args[0:4])
		size := int(args[4])
		copy(payload[0:5], args[0:5])
		data := spiRead(address, size)
		copy(payload[5:], data)
	case 0x21: // NFC/IR MCU configuration
		reply[13] = 0xA0
	}
	return reply
}

func spiRead(address uint32, size int) []byte {
	if size <= 0 {
		return nil
	}
	if size > 30 { // 35-byte payload minus the echoed address/length header
		size = 30
	}
	out := make([]byte, size)
	switch address {
	case 0x6020:
		copy(out, imuCalibration[:])
	case 0x603D:
		copy(out, stickCalibration[:])
	case 0x6080:
		copy(out, sensorParameters[:])
	case 0x6098:
		copy(out, stickParameters[:])
	case 0x8010, 0x8026:
		// No user calibration: 0xFF makes SDL fall back to the factory data.
		for i := range out {
			out[i] = 0xFF
		}
	}
	return out
}

// Factory data from the published SPI layout. The IMU values are the Pro
// Controller's accelerometer/gyroscope origins and sensitivity coefficients;
// SDL uses them to turn raw samples into m/s² and rad/s.
var (
	imuCalibration = [24]byte{
		0xB0, 0xFF, 0xB9, 0xFE, 0xE0, 0x00, // accel origin
		0x00, 0x40, 0x00, 0x40, 0x00, 0x40, // accel sensitivity (16384)
		0x0E, 0x00, 0xDF, 0xFF, 0xD0, 0xFF, // gyro origin
		0x3B, 0x34, 0x3B, 0x34, 0x3B, 0x34, // gyro sensitivity (13371)
	}
	stickCalibration = [18]byte{
		0x00, 0x08, 0x80, 0x00, 0x08, 0x80, 0x00, 0x08, 0x80,
		0x00, 0x08, 0x80, 0x00, 0x08, 0x80, 0x00, 0x08, 0x80,
	}
	sensorParameters = [24]byte{
		0x5E, 0x01, 0x00, 0x00, 0xF1, 0x0F,
		0x19, 0xD0, 0x4C, 0xAE, 0x40, 0xE1,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	}
	stickParameters = [18]byte{
		0x19, 0xD0, 0x4C, 0xAE, 0x40, 0xE1,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	}
)

func neutral30() []byte {
	report := make([]byte, reportSize)
	report[0] = report30
	report[2] = 0x8E
	report[6], report[7], report[8] = 0x00, 0x08, 0x80
	report[9], report[10], report[11] = 0x00, 0x08, 0x80
	report[12] = 0x08
	return report
}
