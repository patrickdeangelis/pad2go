// Package hidpad provides a clean-room, report-level virtual DualSense device.
package hidpad

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

var (
	ErrDriverMissing      = errors.New("virtual HID driver is missing")
	ErrPermission         = errors.New("permission denied opening virtual HID driver")
	ErrUnsupported        = errors.New("virtual HID is unsupported")
	ErrBadReport          = errors.New("invalid input report")
	ErrClosed             = errors.New("virtual HID device is closed")
	ErrStartTimeout       = errors.New("virtual HID device start timed out")
	ErrUnsupportedFeature = errors.New("unsupported feature report")
	ErrBadFeature         = errors.New("invalid feature report")
)

const (
	reportInputID  = 0x01
	reportOutputID = 0x02
	inputReportLen = 64
	startTimeout   = 2 * time.Second
)

// Profile describes a supported virtual HID device.
type Profile struct {
	name      string
	vendorID  uint16
	productID uint16
	version   uint16
}

// Options configures one device. Host is intended for package tests and host
// implementations; application code should leave it nil.
type Options struct {
	ID       string
	Features map[byte][]byte
	Host     Host
}

// Device is one open report-level virtual HID device.
type Device interface {
	ID() string
	SubmitReport([]byte) error
	Feedback() <-chan Feedback
	Control() <-chan ControlEvent
	Done() <-chan struct{}
	Close() error
}

// HostConfig is the immutable information a platform host needs to create a
// HID collection. Slice and map values are owned copies.
type HostConfig struct {
	Name            string
	Phys            string
	ID              string
	VendorID        uint16
	ProductID       uint16
	Version         uint16
	Descriptor      []byte
	NeutralReport   []byte
	Features        map[byte][]byte
	SupportedEvents uint32
	ReadReportPeriodUs uint32
}

// HostEventKind identifies a request from the operating-system HID host.
type HostEventKind uint8

const (
	HostOutputReport HostEventKind = iota + 1
	HostGetInput
	HostGetFeature
	HostSetFeature
)

// HostEvent is copied before it is completed, so a host may reuse its buffers.
type HostEvent struct {
	Kind     HostEventKind
	Request  uint64
	ReportID byte
	Data     []byte
}

// Host is the narrow seam between the report engine and an OS virtual-HID
// transport. Implementations must honor context cancellation for blocking I/O.
type Host interface {
	Start(context.Context, HostConfig) error
	Events() <-chan HostEvent
	SubmitReport(context.Context, []byte) error
	Complete(context.Context, HostEvent, []byte, error) error
	Close() error
}

// Open starts a device and submits its neutral input report before returning.
func Open(profile Profile, options Options) (Device, error) {
	if profile.name == "" || profile.vendorID == 0 || profile.productID == 0 {
		return nil, fmt.Errorf("%w: unknown profile", ErrUnsupported)
	}

	id, err := reserveID(options.ID)
	if err != nil {
		return nil, err
	}
	features, mac, err := makeFeatures(profile, id, options.Features)
	if err != nil {
		releaseIdentity(id, nil)
		return nil, err
	}
	if err := reserveMAC(id, mac); err != nil {
		releaseIdentity(id, nil)
		return nil, err
	}

	host := options.Host
	if host == nil {
		if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
			releaseIdentity(id, mac)
			return nil, ErrUnsupported
		}
		host, err = openPlatformHost()
		if err != nil {
			releaseIdentity(id, mac)
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &device{
		id:             id,
		host:           host,
		ctx:            ctx,
		cancel:         cancel,
		feedback:       make(chan Feedback, 16),
		control:        make(chan ControlEvent, 16),
		done:           make(chan struct{}),
		last:           neutralReport(),
		mac:            mac,
		featureReports: features,
		loopDone:       make(chan struct{}),
	}
	config := hostConfig(profile, id, features)
	go d.eventLoop()

	started := make(chan error, 1)
	go func() { started <- host.Start(ctx, config) }()
	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	select {
	case err = <-started:
		if err != nil {
			_ = d.Close()
			return nil, fmt.Errorf("start %s virtual HID: %w", profile.name, err)
		}
	case <-timer.C:
		_ = d.Close()
		return nil, ErrStartTimeout
	case <-d.done:
		_ = d.Close()
		return nil, fmt.Errorf("start %s virtual HID: %w", profile.name, d.failure())
	}

	if err := d.SubmitReport(neutralReport()); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("submit neutral report: %w", err)
	}
	return d, nil
}

type device struct {
	id       string
	host     Host
	ctx      context.Context
	cancel   context.CancelFunc
	feedback chan Feedback
	control  chan ControlEvent
	done     chan struct{}

	mu             sync.Mutex
	closed         bool
	failureErr     error
	last           []byte
	mac            []byte
	featureReports map[byte][]byte
	submits        sync.WaitGroup
	submitMu       sync.Mutex
	loopDone       chan struct{}

	closeOnce sync.Once
	closeErr  error
}

func (d *device) ID() string                   { return d.id }
func (d *device) Feedback() <-chan Feedback    { return d.feedback }
func (d *device) Control() <-chan ControlEvent { return d.control }
func (d *device) Done() <-chan struct{}        { return d.done }

func (d *device) SubmitReport(report []byte) error {
	if len(report) != inputReportLen || report[0] != reportInputID {
		return ErrBadReport
	}
	d.submitMu.Lock()
	defer d.submitMu.Unlock()

	copyOfReport := append([]byte(nil), report...)
	d.mu.Lock()
	if d.closed {
		err := d.submitErrorLocked()
		d.mu.Unlock()
		return err
	}
	d.submits.Add(1)
	d.last = append(d.last[:0], copyOfReport...)
	d.mu.Unlock()
	defer d.submits.Done()

	if err := d.host.SubmitReport(d.ctx, copyOfReport); err != nil {
		d.mu.Lock()
		cause := d.submitErrorLocked()
		d.mu.Unlock()
		if cause != nil {
			return fmt.Errorf("submit input report: %w", cause)
		}
		return fmt.Errorf("submit input report: %w", err)
	}
	return nil
}

func (d *device) submitErrorLocked() error {
	if d.failureErr != nil {
		return d.failureErr
	}
	return ErrClosed
}

func (d *device) Close() error {
	d.beginClose(nil)
	d.closeOnce.Do(func() {
		d.submits.Wait()
		if d.loopDone != nil {
			<-d.loopDone
		}
		// Stop the host reader first: after this returns the event channel is
		// stable, so a request cannot be enqueued between the drain and close.
		d.closeErr = d.host.Close()
		drainHostEvents(d.host, context.Background())
		releaseIdentity(d.id, d.mac)
	})
	return d.closeErr
}

func drainHostEvents(host Host, ctx context.Context) {
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
			_ = host.Complete(ctx, event, nil, ErrClosed)
		default:
			return
		}
	}
}

func (d *device) beginClose(cause error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	d.failureErr = cause
	d.cancel()
	close(d.feedback)
	close(d.control)
	close(d.done)
}

func (d *device) failure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failureErr != nil {
		return d.failureErr
	}
	return ErrClosed
}

func (d *device) eventLoop() {
	defer close(d.loopDone)
	events := d.host.Events()
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-events:
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

func (d *device) handleEvent(event HostEvent) error {
	event.Data = append([]byte(nil), event.Data...)
	completionEvent := event
	completionEvent.Data = append([]byte(nil), event.Data...)
	var response []byte
	var completionErr error
	switch event.Kind {
	case HostOutputReport:
		feedback := decodeFeedback(event.Data)
		if err := d.host.Complete(d.ctx, completionEvent, nil, nil); err != nil {
			return err
		}
		d.emitFeedback(feedback)
		d.emitControl(ControlEvent{
			Kind:     HostOutputReport,
			ReportID: eventReportID(event),
			Data:     append([]byte(nil), event.Data...),
		})
		return nil
	case HostGetInput:
		d.mu.Lock()
		response = append([]byte(nil), d.last...)
		d.mu.Unlock()
	case HostGetFeature:
		reportID := eventReportID(event)
		response, completionErr = featureReply(reportID, d.features())
	case HostSetFeature:
		reportID := eventReportID(event)
		if _, ok := featureLength(reportID); !ok {
			completionErr = fmt.Errorf("%w: 0x%02x", ErrUnsupportedFeature, reportID)
		} else if len(event.Data) != 0 && len(event.Data) != featureLengthTotal(reportID) {
			completionErr = fmt.Errorf("%w: 0x%02x has %d bytes", ErrBadFeature, reportID, len(event.Data))
		}
	default:
		completionErr = fmt.Errorf("%w: host event kind %d", ErrUnsupported, event.Kind)
	}
	if err := d.host.Complete(d.ctx, completionEvent, response, completionErr); err != nil {
		return err
	}
	if event.Kind == HostSetFeature {
		d.emitControl(ControlEvent{
			Kind: HostSetFeature, ReportID: eventReportID(event),
			Data: append([]byte(nil), event.Data...), Err: completionErr,
		})
	}
	return nil
}

func eventReportID(event HostEvent) byte {
	if event.ReportID != 0 {
		return event.ReportID
	}
	if len(event.Data) != 0 {
		return event.Data[0]
	}
	return 0
}

func (d *device) features() map[byte][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.featureReports
}

func (d *device) emitFeedback(feedback Feedback) {
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

func (d *device) emitControl(event ControlEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	select {
	case d.control <- event:
	default:
		select {
		case <-d.control:
		default:
		}
		select {
		case d.control <- event:
		default:
		}
	}
}

func hostConfig(profile Profile, id string, features map[byte][]byte) HostConfig {
	config := HostConfig{
		Name:            profile.name,
		Phys:            "pad2go/uhid",
		ID:              id,
		VendorID:        profile.vendorID,
		ProductID:       profile.productID,
		Version:         profile.version,
		Descriptor:      buildDescriptor(),
		NeutralReport:   neutralReport(),
		Features:        cloneFeatures(features),
		SupportedEvents: supportedHostEvents,
	}
	return config
}

func cloneFeatures(features map[byte][]byte) map[byte][]byte {
	out := make(map[byte][]byte, len(features))
	for id, report := range features {
		out[id] = append([]byte(nil), report...)
	}
	return out
}
