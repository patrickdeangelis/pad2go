package demo

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/hid"
)

type hidBackend struct{}

func NewHIDBackend() hid.Backend { return hidBackend{} }
func (hidBackend) List() ([]hid.Info, error) {
	return []hid.Info{{Path: "demo-dualsense", VendorID: dualsense.VendorID, ProductID: dualsense.ProductID}}, nil
}
func (hidBackend) Open(hid.Info) (dualsense.Transport, error) {
	return &hidTransport{closed: make(chan struct{})}, nil
}

type hidTransport struct {
	once   sync.Once
	closed chan struct{}
	frame  uint64
}

// Feature supplies synthetic, unit-scale sensor calibration for the demo.
func (t *hidTransport) Feature(id byte, size int) ([]byte, error) {
	if id != 5 || size != 41 {
		return nil, errors.New("demo feature unavailable")
	}
	r := make([]byte, size)
	r[0] = 5
	put := func(offset int, v int16) { binary.LittleEndian.PutUint16(r[offset:], uint16(v)) }
	for i := range 3 {
		put(7+4*i, 1000)
		put(9+4*i, -1000)
		put(23+4*i, 8192)
		put(25+4*i, -8192)
	}
	put(19, 100)
	put(21, 100)
	return r, nil
}

func demoDualSenseInput(frame uint64) [64]byte {
	var b [64]byte
	b[0] = 1
	t := float64(frame) / 60
	b[1], b[2] = byte(128+80*math.Sin(t)), byte(128+80*math.Cos(t))
	b[3], b[4] = byte(128+70*math.Cos(t*.8)), byte(128+70*math.Sin(t*.8))
	b[5], b[6] = byte(127+127*math.Sin(t)), byte(127+127*math.Cos(t))
	b[8] = 8 | []byte{0x10, 0x20, 0x40, 0x80}[(frame/40)%4]
	b[33], b[37] = 0x80, 0x80
	if frame%300 < 200 {
		x, y := uint16((frame%200)*9), uint16(540+300*math.Sin(t))
		b[33], b[34], b[35], b[36] = 1, byte(x), byte((x>>8)&15)|byte((y&15)<<4), byte(y>>4)
	}
	if frame%300 >= 100 && frame%300 < 200 {
		x, y := uint16(1500), uint16(300)
		b[37], b[38], b[39], b[40] = 2, byte(x), byte((x>>8)&15)|byte((y&15)<<4), byte(y>>4)
	}
	if frame%300 >= 200 && frame%300 < 220 {
		b[10] |= 2
	}
	binary.LittleEndian.PutUint16(b[16:], uint16(int16(150*math.Sin(t))))
	binary.LittleEndian.PutUint16(b[26:], 0xe000)
	b[53] = 0x16 // 65% estimated, charging
	return b
}

func (t *hidTransport) Read(buf []byte) (int, error) {
	timer := time.NewTimer(16 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-t.closed:
		return 0, errors.New("demo DualSense closed")
	case <-timer.C:
	}
	if len(buf) < 64 {
		return 0, nil
	}
	t.frame++
	r := demoDualSenseInput(t.frame)
	copy(buf, r[:])
	return 64, nil
}
func (t *hidTransport) Write([]byte) error { return nil }
func (t *hidTransport) Close() error       { t.once.Do(func() { close(t.closed) }); return nil }
