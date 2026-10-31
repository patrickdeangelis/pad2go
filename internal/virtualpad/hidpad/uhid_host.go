package hidpad

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// uhidHost is the platform-independent Linux UHID host. Only the opener in
// uhid_linux.go is platform-specific, so the event loop is testable on
// Windows with an in-memory transport.
type uhidHost struct {
	open func() (io.ReadWriteCloser, error)

	mu            sync.Mutex
	rw            io.ReadWriteCloser
	codec         *uhidCodec
	events        chan HostEvent
	started       chan struct{}
	startOnce     sync.Once
	done          chan struct{}
	readerDone    chan struct{}
	readerStarted bool
	readErr       error
	closed        bool
	closeOnce     sync.Once
	stopOnce      sync.Once
	eventsOnce    sync.Once
	writeMu       sync.Mutex
}

func newUHIDHost(open func() (io.ReadWriteCloser, error)) *uhidHost {
	return &uhidHost{
		open:       open,
		events:     make(chan HostEvent, 32),
		started:    make(chan struct{}),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
	}
}

func (h *uhidHost) Start(ctx context.Context, cfg HostConfig) error {
	rw, err := h.open()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = rw.Close()
		return ctx.Err()
	default:
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = rw.Close()
		return ErrClosed
	}
	h.rw = rw
	h.codec = &uhidCodec{rw: rw}
	h.readerStarted = true
	h.mu.Unlock()

	go h.readLoop()

	// Do not hold writeMu here: the kernel may synchronously ask for feature
	// reports while CREATE2 is being processed, and the answer must be written
	// from the event loop.
	if err := h.codec.createDevice(cfg); err != nil {
		_ = h.Close()
		return fmt.Errorf("create UHID device: %w", err)
	}

	select {
	case <-h.started:
		return nil
	case <-h.readerDone:
		select {
		case <-h.started:
			return nil
		default:
			_ = h.Close()
			if err := h.Err(); err != nil {
				return fmt.Errorf("read UHID events: %w", err)
			}
			return ErrStartTimeout
		}
	case <-ctx.Done():
		_ = h.Close()
		return ctx.Err()
	case <-time.After(startTimeout):
		_ = h.Close()
		return ErrStartTimeout
	}
}

func (h *uhidHost) Events() <-chan HostEvent { return h.events }

func (h *uhidHost) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.readErr
}

func (h *uhidHost) SubmitReport(_ context.Context, report []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.mu.Lock()
	codec, closed := h.codec, h.closed
	h.mu.Unlock()
	if closed || codec == nil {
		return ErrClosed
	}
	return codec.inputReport(report)
}

func (h *uhidHost) Complete(_ context.Context, event HostEvent, data []byte, completionErr error) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.mu.Lock()
	codec, closed := h.codec, h.closed
	h.mu.Unlock()
	if closed || codec == nil {
		return ErrClosed
	}
	errno := uint16(0)
	if completionErr != nil {
		errno = 5 // EIO
	}
	switch event.Kind {
	case HostOutputReport:
		// A UHID_OUTPUT event has no request id and needs no reply. An
		// output-type UHID_SET_REPORT does need one, and the kernel waits for
		// UHID_SET_REPORT_REPLY before it releases the HID client.
		if event.NeedsReply {
			return codec.setReportReply(uint32(event.Request), errno)
		}
		return nil
	case HostGetInput, HostGetFeature:
		return codec.getReportReply(uint32(event.Request), errno, data)
	case HostSetFeature:
		return codec.setReportReply(uint32(event.Request), errno)
	default:
		return fmt.Errorf("%w: host event kind %d", ErrUnsupported, event.Kind)
	}
}

func (h *uhidHost) StopEvents() error {
	h.stopOnce.Do(func() {
		h.mu.Lock()
		done, rw, codec, started, readerDone := h.done, h.rw, h.codec, h.readerStarted, h.readerDone
		h.closed = true
		h.mu.Unlock()
		if done != nil {
			close(done)
		}
		// Send DESTROY while the descriptor is still open. Closing the fd also
		// destroys the device, but the explicit event is the documented path.
		if codec != nil {
			h.writeMu.Lock()
			_ = codec.destroy()
			h.writeMu.Unlock()
		}
		if rw != nil {
			_ = rw.Close()
		}
		if started {
			<-readerDone
		}
		h.closeEvents()
	})
	return nil
}

func (h *uhidHost) Close() error {
	h.closeOnce.Do(func() {
		_ = h.StopEvents()
	})
	return nil
}

func (h *uhidHost) closeEvents() {
	h.eventsOnce.Do(func() { close(h.events) })
}

func (h *uhidHost) readLoop() {
	defer close(h.readerDone)
	defer h.closeEvents()

	for {
		typ, payload, err := h.codec.read()
		if err != nil {
			h.mu.Lock()
			h.readErr = err
			h.mu.Unlock()
			return
		}
		switch typ {
		case uhidStart:
			h.startOnce.Do(func() { close(h.started) })
		case uhidStop:
			return
		case uhidOpen:
			// The kernel has opened the HID collection; nothing to do.
		case uhidClose:
			// The collection was closed; keep the transport alive for reopen.
		case uhidOutput:
			data, reportType, err := decodeUHIDOutput(payload)
			if err != nil || reportType != uhidReportOutput || len(data) == 0 {
				continue
			}
			if !h.emit(HostEvent{Kind: HostOutputReport, ReportID: data[0], Data: data}) {
				return
			}
		case uhidGetReport:
			request, reportID, reportType, err := decodeUHIDGetReport(payload)
			if err != nil {
				continue
			}
			kind := HostGetFeature
			if reportType == uhidReportInput {
				kind = HostGetInput
			}
			if !h.emit(HostEvent{Kind: kind, Request: uint64(request), ReportID: reportID}) {
				return
			}
		case uhidSetReport:
			request, reportID, reportType, data, err := decodeUHIDSetReport(payload)
			if err != nil {
				continue
			}
			kind := HostSetFeature
			if reportType == uhidReportOutput {
				kind = HostOutputReport
			}
			if !h.emit(HostEvent{Kind: kind, Request: uint64(request), ReportID: reportID, Data: data, NeedsReply: true}) {
				return
			}
		}
	}
}

func (h *uhidHost) emit(event HostEvent) bool {
	h.mu.Lock()
	events, done := h.events, h.done
	h.mu.Unlock()
	if events == nil || done == nil {
		return false
	}
	select {
	case events <- event:
		return true
	case <-done:
		return false
	}
}

var _ = errors.Is
