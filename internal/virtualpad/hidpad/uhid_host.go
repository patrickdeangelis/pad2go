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

	mu         sync.Mutex
	rw         io.ReadWriteCloser
	codec      *uhidCodec
	events     chan HostEvent
	started    chan struct{}
	startOnce  sync.Once
	done       chan struct{}
	readerDone chan struct{}
	closed     bool
	closeOnce  sync.Once
	writeMu    sync.Mutex
}

func newUHIDHost(open func() (io.ReadWriteCloser, error)) *uhidHost {
	return &uhidHost{open: open}
}

func (h *uhidHost) Start(ctx context.Context, cfg HostConfig) error {
	rw, err := h.open()
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.rw = rw
	h.codec = &uhidCodec{rw: rw}
	h.events = make(chan HostEvent, 32)
	h.started = make(chan struct{})
	h.done = make(chan struct{})
	h.readerDone = make(chan struct{})
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
	case <-ctx.Done():
		_ = h.Close()
		return ctx.Err()
	case <-time.After(startTimeout):
		_ = h.Close()
		return ErrStartTimeout
	}
}

func (h *uhidHost) Events() <-chan HostEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.events
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
		return nil
	case HostGetInput, HostGetFeature:
		return codec.getReportReply(uint32(event.Request), errno, data)
	case HostSetFeature:
		return codec.setReportReply(uint32(event.Request), errno)
	default:
		return fmt.Errorf("%w: host event kind %d", ErrUnsupported, event.Kind)
	}
}

func (h *uhidHost) Close() error {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		codec, rw, done, readerDone := h.codec, h.rw, h.done, h.readerDone
		h.mu.Unlock()

		if done != nil {
			close(done)
		}
		if codec != nil {
			_ = codec.destroy()
		}
		if rw != nil {
			_ = rw.Close()
		}
		if readerDone != nil {
			<-readerDone
		}
	})
	return nil
}

func (h *uhidHost) readLoop() {
	defer close(h.readerDone)
	defer close(h.events)

	for {
		typ, payload, err := h.codec.read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// The core observes the closed event stream and closes the
				// device; no separate error channel is needed.
			}
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
			request, reportID, _, data, err := decodeUHIDSetReport(payload)
			if err != nil {
				continue
			}
			if !h.emit(HostEvent{Kind: HostSetFeature, Request: uint64(request), ReportID: reportID, Data: data}) {
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
