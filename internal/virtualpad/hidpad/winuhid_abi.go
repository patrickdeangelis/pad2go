package hidpad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
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
	winUHIDSetDeviceInfo      = winUHIDCtlCode(0, false)
	winUHIDSetReportDescriptor = winUHIDCtlCode(1, false)
	winUHIDSetInstanceID      = winUHIDCtlCode(2, false)
	winUHIDSetHardwareIDs     = winUHIDCtlCode(3, false)
	winUHIDCreateDevice       = winUHIDCtlCode(4, false)
	winUHIDStartDevice        = winUHIDCtlCode(5, false)
	winUHIDGetNextEvent       = winUHIDCtlCode(6, false)
	winUHIDCompleteWriteEvent = winUHIDCtlCode(7, false)
	winUHIDCompleteReadEvent  = winUHIDCtlCode(8, false)
	winUHIDGetInterfaceVersion = winUHIDCtlCode(9, true)
)

func encodeWinUHIDDeviceInfo(cfg HostConfig) []byte {
	buf := make([]byte, 30)
	binary.LittleEndian.PutUint32(buf[0:4], winUHIDVersion)
	binary.LittleEndian.PutUint32(buf[4:8], cfg.SupportedEvents)
	binary.LittleEndian.PutUint16(buf[8:10], cfg.VendorID)
	binary.LittleEndian.PutUint16(buf[10:12], cfg.ProductID)
	binary.LittleEndian.PutUint16(buf[12:14], cfg.Version)
	// bytes 14..29 are the all-zero ContainerId GUID.
	return buf
}

func encodeWinUHIDConfig(cfg HostConfig) []byte {
	size := 44
	if strconv.IntSize == 64 {
		size = 56
	}
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], cfg.SupportedEvents)
	binary.LittleEndian.PutUint16(buf[4:6], cfg.VendorID)
	binary.LittleEndian.PutUint16(buf[6:8], cfg.ProductID)
	binary.LittleEndian.PutUint16(buf[8:10], cfg.Version)
	binary.LittleEndian.PutUint16(buf[10:12], uint16(len(cfg.Descriptor)))
	// Pointer fields are zero in this pure encoder; the host sets them at the
	// actual call site. The offsets are what the ABI test checks.
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
	if dataLength > 4096 || len(buf) < 13+int(dataLength) {
		return HostEvent{}, fmt.Errorf("%w: WinUHid event data %d", ErrBadReport, dataLength)
	}
	var kind HostEventKind
	switch eventType {
	case winUHIDEventGetFeature:
		kind = HostGetFeature
	case winUHIDEventSetFeature:
		kind = HostSetFeature
	case winUHIDEventWriteReport:
		kind = HostOutputReport
	case winUHIDEventReadReport:
		kind = HostGetInput
	default:
		return HostEvent{}, fmt.Errorf("%w: WinUHid event type 0x%x", ErrUnsupported, eventType)
	}
	data := append([]byte(nil), buf[13:13+int(dataLength)]...)
	return HostEvent{Kind: kind, Request: uint64(request), ReportID: reportID, Data: data}, nil
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

func classifyWinUHIDError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, syscall.Errno(2)), errors.Is(err, syscall.Errno(3)):
		return fmt.Errorf("%w: %v", ErrDriverMissing, err)
	case errors.Is(err, syscall.Errno(5)):
		return fmt.Errorf("%w: %v", ErrPermission, err)
	default:
		return fmt.Errorf("open %s: %w", winUHIDDevicePath, err)
	}
}
