package hidpad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
)

// WinUHid's user-mode ABI is the shared IOCTL interface in the pinned MIT
// upstream (`WinUHid Driver/Public.h`). This file keeps the packed layouts and
// IOCTL numbers platform-independent so they can be tested on any OS; the
// Windows-only host performs the calls.
const (
	winUHIDDevicePath = `\\.\WinUHid`
	winUHIDVersion    = 1

	winUHIDEventNone        uint32 = 0
	winUHIDEventGetFeature  uint32 = 1
	winUHIDEventSetFeature  uint32 = 2
	winUHIDEventWriteReport uint32 = 4
	winUHIDEventReadReport  uint32 = 8
)

func winUHIDCtlCode(function uint32, anyAccess bool) uint32 {
	access := uint32(2) // FILE_WRITE_DATA
	if anyAccess {
		access = 0 // FILE_ANY_ACCESS
	}
	return (0x22 << 16) | (access << 14) | ((0x800 + function) << 2) | 0 // METHOD_BUFFERED
}

var (
	winUHIDSetDeviceInfo       = winUHIDCtlCode(0, false)
	winUHIDSetReportDescriptor = winUHIDCtlCode(1, false)
	winUHIDSetInstanceID       = winUHIDCtlCode(2, false)
	winUHIDSetHardwareIDs      = winUHIDCtlCode(3, false)
	winUHIDCreateDevice        = winUHIDCtlCode(4, false)
	winUHIDStartDevice         = winUHIDCtlCode(5, false)
	winUHIDGetNextEvent        = winUHIDCtlCode(6, false)
	winUHIDCompleteWriteEvent  = winUHIDCtlCode(7, false)
	winUHIDCompleteReadEvent   = winUHIDCtlCode(8, false)
	winUHIDGetInterfaceVersion = winUHIDCtlCode(9, true)
)

func encodeWinUHIDDeviceInfo(cfg HostConfig) []byte {
	buf := make([]byte, 32)
	binary.LittleEndian.PutUint32(buf[0:4], winUHIDVersion)
	binary.LittleEndian.PutUint32(buf[4:8], cfg.SupportedEvents)
	binary.LittleEndian.PutUint16(buf[8:10], cfg.VendorID)
	binary.LittleEndian.PutUint16(buf[10:12], cfg.ProductID)
	binary.LittleEndian.PutUint16(buf[12:14], cfg.Version)
	// bytes 14..15 are alignment padding; the all-zero ContainerId GUID is at 16..31.
	return buf
}

func decodeWinUHIDEvent(buf []byte) (HostEvent, error) {
	if len(buf) < 9 {
		return HostEvent{}, fmt.Errorf("%w: short WinUHid event %d", ErrBadReport, len(buf))
	}
	eventType := binary.LittleEndian.Uint32(buf[0:4])
	request := binary.LittleEndian.Uint32(buf[4:8])
	reportID := buf[8]
	dataLength := uint32(0)
	if len(buf) >= 13 {
		dataLength = binary.LittleEndian.Uint32(buf[9:13])
	}
	switch eventType {
	case winUHIDEventGetFeature:
		return HostEvent{Kind: HostGetFeature, Request: uint64(request), ReportID: reportID, RequestedLength: dataLength}, nil
	case winUHIDEventReadReport:
		return HostEvent{Kind: HostGetInput, Request: uint64(request), ReportID: reportID, RequestedLength: dataLength}, nil
	case winUHIDEventSetFeature, winUHIDEventWriteReport:
		kind := HostSetFeature
		if eventType == winUHIDEventWriteReport {
			kind = HostOutputReport
		}
		event := HostEvent{Kind: kind, Request: uint64(request), ReportID: reportID}
		if dataLength > 4096 || len(buf) < 13+int(dataLength) {
			return event, fmt.Errorf("%w: WinUHid event data %d", ErrBadReport, dataLength)
		}
		event.Data = append([]byte(nil), buf[13:13+int(dataLength)]...)
		return event, nil
	default:
		return HostEvent{}, fmt.Errorf("%w: WinUHid event type 0x%x", ErrUnsupported, eventType)
	}
}

func encodeWinUHIDCompleteWrite(request uint32, status uint32) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[0:4], request)
	binary.LittleEndian.PutUint32(buf[4:8], status)
	return buf
}

func encodeWinUHIDCompleteRead(request uint32, status uint32, data []byte) []byte {
	buf := make([]byte, 12+len(data))
	binary.LittleEndian.PutUint32(buf[0:4], request)
	binary.LittleEndian.PutUint32(buf[4:8], status)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(data)))
	copy(buf[12:], data)
	return buf
}

func fitReadData(data []byte, requested uint32) []byte {
	if requested == 0 {
		return data
	}
	out := make([]byte, requested)
	copy(out, data)
	return out
}

func classifyWinUHIDError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, syscall.Errno(2)), errors.Is(err, syscall.Errno(3)):
		return fmt.Errorf("%w: WinUHid %s: %v", ErrDriverMissing, winUHIDDevicePath, err)
	case errors.Is(err, syscall.Errno(5)):
		return fmt.Errorf("%w: WinUHid %s: %v", ErrPermission, winUHIDDevicePath, err)
	default:
		return fmt.Errorf("open %s: %w", winUHIDDevicePath, err)
	}
}

func winUHIDTimeout(err error) bool {
	return errors.Is(err, syscall.Errno(1460)) // ERROR_TIMEOUT / WAIT_TIMEOUT
}

func checkWinUHIDInterfaceVersion(version uint32) error {
	if version < winUHIDVersion {
		return fmt.Errorf("%w: WinUHid interface version %d", ErrUnsupported, version)
	}
	return nil
}

func winUHIDReadLoop(next func() ([]byte, error), events chan<- HostEvent, done <-chan struct{}, fail func(HostEvent, error)) error {
	for {
		select {
		case <-done:
			return nil
		default:
		}
		buf, err := next()
		if err != nil {
			if winUHIDTimeout(err) {
				select {
				case <-done:
					return nil
				default:
					continue
				}
			}
			return err
		}
		event, err := decodeWinUHIDEvent(buf)
		if err != nil {
			// Complete the request before stopping: returning without doing so
			// leaves the HID client's IOCTL pending forever.
			if event.Kind != 0 && fail != nil {
				fail(event, err)
			}
			return err
		}
		select {
		case events <- event:
		case <-done:
			return nil
		}
	}
}
