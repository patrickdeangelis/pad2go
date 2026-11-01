package hidpad

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// Linux UHID UAPI (`include/uapi/linux/uhid.h`). The event is a u32 type
// followed by a fixed 4372-byte union, so a whole event is 4376 bytes. The
// kernel returns exactly one whole event per read() and drops any excess.
const (
	uhidEventSize = 4376
	uhidDataMax   = 4096

	uhidDestroy        uint32 = 1
	uhidStart          uint32 = 2
	uhidStop           uint32 = 3
	uhidOpen           uint32 = 4
	uhidClose          uint32 = 5
	uhidOutput         uint32 = 6
	uhidInput          uint32 = 8
	uhidGetReport      uint32 = 9
	uhidGetReportReply uint32 = 10
	uhidCreate2        uint32 = 11
	uhidInput2         uint32 = 12
	uhidSetReport      uint32 = 13
	uhidSetReportReply uint32 = 14

	uhidReportFeature uint8 = 0
	uhidReportOutput  uint8 = 1
	uhidReportInput   uint8 = 2

	uhidBusUSB uint32 = 3
)

var errShortUHIDEvent = errors.New("short UHID event")

type uhidCodec struct {
	rw io.ReadWriteCloser
}

func (c *uhidCodec) write(typ uint32, payload []byte) error {
	if len(payload) > uhidEventSize-4 {
		return fmt.Errorf("UHID event payload %d exceeds %d", len(payload), uhidEventSize-4)
	}
	buf := make([]byte, uhidEventSize)
	binary.LittleEndian.PutUint32(buf, typ)
	copy(buf[4:], payload)
	_, err := c.rw.Write(buf)
	return err
}

// read consumes exactly one event with a single read. uhid_char_read returns
// one 4376-byte event per call and discards anything beyond the caller's
// buffer, so a second read would desynchronize or drop the next event.
func (c *uhidCodec) read() (uint32, []byte, error) {
	buf := make([]byte, uhidEventSize)
	n, err := c.rw.Read(buf)
	if err != nil {
		return 0, nil, err
	}
	if n != uhidEventSize {
		return 0, nil, fmt.Errorf("%w: got %d bytes, want %d", errShortUHIDEvent, n, uhidEventSize)
	}
	return binary.LittleEndian.Uint32(buf), buf[4:], nil
}

func (c *uhidCodec) createDevice(cfg HostConfig) error {
	if len(cfg.Name) > 127 || len(cfg.Phys) > 63 || len(cfg.ID) > 63 {
		return fmt.Errorf("%w: UHID name/phys/uniq too long", ErrBadFeature)
	}
	if len(cfg.Descriptor) == 0 || len(cfg.Descriptor) > uhidDataMax {
		return fmt.Errorf("%w: UHID report descriptor length %d", ErrBadFeature, len(cfg.Descriptor))
	}
	payload := make([]byte, 4372)
	copy(payload[0:128], cfg.Name)
	copy(payload[128:192], cfg.Phys)
	copy(payload[192:256], cfg.ID)
	binary.LittleEndian.PutUint16(payload[256:258], uint16(len(cfg.Descriptor)))
	binary.LittleEndian.PutUint16(payload[258:260], uint16(uhidBusUSB))
	binary.LittleEndian.PutUint32(payload[260:264], uint32(cfg.VendorID))
	binary.LittleEndian.PutUint32(payload[264:268], uint32(cfg.ProductID))
	binary.LittleEndian.PutUint32(payload[268:272], uint32(cfg.Version))
	binary.LittleEndian.PutUint32(payload[272:276], 0)
	copy(payload[276:276+len(cfg.Descriptor)], cfg.Descriptor)
	return c.write(uhidCreate2, payload)
}

func (c *uhidCodec) inputReport(report []byte) error {
	if len(report) == 0 || len(report) > uhidDataMax {
		return fmt.Errorf("%w: UHID input report length %d", ErrBadReport, len(report))
	}
	payload := make([]byte, 2+len(report))
	binary.LittleEndian.PutUint16(payload[0:2], uint16(len(report)))
	copy(payload[2:], report)
	return c.write(uhidInput2, payload)
}

func (c *uhidCodec) destroy() error {
	return c.write(uhidDestroy, nil)
}

func (c *uhidCodec) getReportReply(request uint32, errno uint16, data []byte) error {
	if len(data) > uhidDataMax {
		return fmt.Errorf("%w: UHID report reply length %d", ErrBadFeature, len(data))
	}
	payload := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint32(payload[0:4], request)
	binary.LittleEndian.PutUint16(payload[4:6], errno)
	binary.LittleEndian.PutUint16(payload[6:8], uint16(len(data)))
	copy(payload[8:], data)
	return c.write(uhidGetReportReply, payload)
}

func (c *uhidCodec) setReportReply(request uint32, errno uint16) error {
	payload := make([]byte, 6)
	binary.LittleEndian.PutUint32(payload[0:4], request)
	binary.LittleEndian.PutUint16(payload[4:6], errno)
	return c.write(uhidSetReportReply, payload)
}

func decodeUHIDOutput(payload []byte) ([]byte, uint8, error) {
	if len(payload) < 4099 {
		return nil, 0, errShortUHIDEvent
	}
	size := int(binary.LittleEndian.Uint16(payload[4096:4098]))
	if size > uhidDataMax {
		return nil, 0, fmt.Errorf("UHID output size %d", size)
	}
	return append([]byte(nil), payload[:size]...), payload[4098], nil
}

func decodeUHIDGetReport(payload []byte) (request uint32, reportID, reportType uint8, err error) {
	if len(payload) < 6 {
		return 0, 0, 0, errShortUHIDEvent
	}
	return binary.LittleEndian.Uint32(payload[0:4]), payload[4], payload[5], nil
}

func decodeUHIDSetReport(payload []byte) (request uint32, reportID, reportType uint8, data []byte, err error) {
	if len(payload) < 8 {
		return 0, 0, 0, nil, errShortUHIDEvent
	}
	request = binary.LittleEndian.Uint32(payload[0:4])
	reportID = payload[4]
	reportType = payload[5]
	size := int(binary.LittleEndian.Uint16(payload[6:8]))
	if size > uhidDataMax || len(payload) < 8+size {
		return 0, 0, 0, nil, errShortUHIDEvent
	}
	return request, reportID, reportType, append([]byte(nil), payload[8:8+size]...), nil
}

func classifyUHIDOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return err
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: /dev/uhid: %w", ErrDriverMissing, err)
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: /dev/uhid: %w", ErrPermission, err)
	}
	return fmt.Errorf("open /dev/uhid: %w", err)
}
