//go:build windows

package hidpad

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openPlatformHost() (Host, error) {
	return &winUHIDHost{}, nil
}

type winUHIDHost struct {
	mu         sync.Mutex
	handle     windows.Handle
	events     chan HostEvent
	done       chan struct{}
	readerDone chan struct{}
	closed     bool
	closeOnce  sync.Once
	writeMu    sync.Mutex
}

func (h *winUHIDHost) Start(ctx context.Context, cfg HostConfig) error {
	path, err := windows.UTF16PtrFromString(winUHIDDevicePath)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return classifyWinUHIDError(err)
	}
	h.mu.Lock()
	h.handle = handle
	h.events = make(chan HostEvent, 32)
	h.done = make(chan struct{})
	h.readerDone = make(chan struct{})
	h.mu.Unlock()

	cleanup := func() {
		h.mu.Lock()
		h.closed = true
		done := h.done
		h.mu.Unlock()
		if done != nil {
			close(done)
		}
		_ = windows.CancelIoEx(handle, nil)
		_ = windows.CloseHandle(handle)
	}

	if version, err := h.interfaceVersion(); err != nil {
		cleanup()
		return err
	} else if version < winUHIDVersion {
		cleanup()
		return fmt.Errorf("%w: WinUHid interface version %d", ErrUnsupported, version)
	}
	if err := h.ioctl(winUHIDSetDeviceInfo, encodeWinUHIDDeviceInfo(cfg), nil); err != nil {
		cleanup()
		return fmt.Errorf("set WinUHid device info: %w", err)
	}
	if err := h.ioctl(winUHIDSetReportDescriptor, cfg.Descriptor, nil); err != nil {
		cleanup()
		return fmt.Errorf("set WinUHid report descriptor: %w", err)
	}
	instance, err := windows.UTF16FromString(cfg.ID)
	if err != nil {
		cleanup()
		return err
	}
	if err := h.ioctl(winUHIDSetInstanceID, utf16Bytes(instance), nil); err != nil {
		cleanup()
		return fmt.Errorf("set WinUHid instance ID: %w", err)
	}
	if err := h.ioctl(winUHIDCreateDevice, nil, nil); err != nil {
		cleanup()
		return fmt.Errorf("create WinUHid device: %w", err)
	}

	if err := h.ioctl(winUHIDStartDevice, nil, nil); err != nil {
		cleanup()
		return fmt.Errorf("start WinUHid device: %w", err)
	}
	go h.readLoop()
	return nil
}

func (h *winUHIDHost) Events() <-chan HostEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.events
}

func (h *winUHIDHost) SubmitReport(ctx context.Context, report []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.mu.Lock()
	handle, closed := h.handle, h.closed
	h.mu.Unlock()
	if closed || handle == 0 {
		return ErrClosed
	}
	return winUHIDWrite(handle, report)
}

func (h *winUHIDHost) Complete(_ context.Context, event HostEvent, data []byte, completionErr error) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.mu.Lock()
	handle, closed := h.handle, h.closed
	h.mu.Unlock()
	if closed || handle == 0 {
		return ErrClosed
	}
	status := uint32(0) // STATUS_SUCCESS
	if completionErr != nil {
		status = 0xC0000001 // STATUS_UNSUCCESSFUL
	}
	switch event.Kind {
	case HostGetInput, HostGetFeature:
		return h.ioctlHandle(handle, winUHIDCompleteReadEvent, encodeWinUHIDCompleteRead(uint32(event.Request), status, data), nil)
	case HostSetFeature, HostOutputReport:
		return h.ioctlHandle(handle, winUHIDCompleteWriteEvent, encodeWinUHIDCompleteWrite(uint32(event.Request), status), nil)
	default:
		return fmt.Errorf("%w: host event kind %d", ErrUnsupported, event.Kind)
	}
}

func (h *winUHIDHost) Close() error {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		handle, done, readerDone := h.handle, h.done, h.readerDone
		h.mu.Unlock()
		if done != nil {
			close(done)
		}
		if handle != 0 {
			_ = windows.CancelIoEx(handle, nil)
			_ = windows.CloseHandle(handle)
		}
		if readerDone != nil {
			<-readerDone
		}
	})
	return nil
}

func (h *winUHIDHost) readLoop() {
	defer close(h.readerDone)
	defer func() {
		h.mu.Lock()
		events := h.events
		h.mu.Unlock()
		if events != nil {
			close(events)
		}
	}()
	h.mu.Lock()
	handle := h.handle
	h.mu.Unlock()
	for {
		buf, err := winUHIDNextEvent(handle)
		if err != nil {
			return
		}
		event, err := decodeWinUHIDEvent(buf)
		if err != nil {
			continue
		}
		h.mu.Lock()
		events, done := h.events, h.done
		h.mu.Unlock()
		if events == nil {
			return
		}
		select {
		case events <- event:
		case <-done:
			return
		}
	}
}

func (h *winUHIDHost) interfaceVersion() (uint32, error) {
	out := make([]byte, 4)
	if err := h.ioctl(winUHIDGetInterfaceVersion, nil, out); err != nil {
		return 0, classifyWinUHIDError(err)
	}
	return *(*uint32)(unsafe.Pointer(&out[0])), nil
}

func (h *winUHIDHost) ioctl(code uint32, in, out []byte) error {
	h.mu.Lock()
	handle := h.handle
	h.mu.Unlock()
	return h.ioctlHandle(handle, code, in, out)
}

func (h *winUHIDHost) ioctlHandle(handle windows.Handle, code uint32, in, out []byte) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	var inPtr, outPtr *byte
	if len(in) > 0 {
		inPtr = &in[0]
	}
	if len(out) > 0 {
		outPtr = &out[0]
	}
	overlapped := windows.Overlapped{HEvent: event}
	var returned uint32
	err = windows.DeviceIoControl(handle, code, inPtr, uint32(len(in)), outPtr, uint32(len(out)), &returned, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(handle, &overlapped, &returned, true)
	}
	return err
}

func winUHIDNextEvent(handle windows.Handle) ([]byte, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(event)
	buf := make([]byte, 4096+128)
	overlapped := windows.Overlapped{HEvent: event}
	var returned uint32
	err = windows.DeviceIoControl(handle, winUHIDGetNextEvent, nil, 0, &buf[0], uint32(len(buf)), &returned, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		status, waitErr := windows.WaitForSingleObject(event, 250)
		if waitErr != nil {
			return nil, waitErr
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			_ = windows.CancelIoEx(handle, &overlapped)
			_ = windows.GetOverlappedResult(handle, &overlapped, &returned, true)
			return nil, syscall.Errno(1460)
		}
		err = windows.GetOverlappedResult(handle, &overlapped, &returned, false)
	}
	if err != nil {
		return nil, err
	}
	return buf[:returned], nil
}

func winUHIDWrite(handle windows.Handle, report []byte) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var written uint32
	err = windows.WriteFile(handle, report, &written, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(handle, &overlapped, &written, true)
	}
	return err
}

func utf16Bytes(s []uint16) []byte {
	buf := make([]byte, len(s)*2)
	for i, v := range s {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}
