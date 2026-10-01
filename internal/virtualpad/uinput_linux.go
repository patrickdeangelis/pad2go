//go:build linux

package virtualpad

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
)

const platformName = "uinput"

// Linux input constants (linux/input-event-codes.h, linux/uinput.h).
const (
	evSyn    = 0x00
	evKey    = 0x01
	evAbs    = 0x03
	evFF     = 0x15
	evUinput = 0x0101

	uiFFUpload = 1
	uiFFErase  = 2

	ffRumble = 0x50

	absX, absY, absZ, absRX, absRY, absRZ = 0x00, 0x01, 0x02, 0x03, 0x04, 0x05
	absHat0X, absHat0Y                    = 0x10, 0x11

	btnA, btnB, btnX, btnY       = 0x130, 0x131, 0x133, 0x134
	btnTL, btnTR                 = 0x136, 0x137
	btnSelect, btnStart, btnMode = 0x13a, 0x13b, 0x13c
	btnThumbL, btnThumbR         = 0x13d, 0x13e

	busUSB = 0x03
	absCnt = 0x40
)

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'U'<<8 | nr }

const (
	iocNone  = 0
	iocWrite = 1
	iocRW    = 3
)

// struct ff_effect: 14 header bytes, then a union aligned to pointer size.
var (
	// The largest union member (ff_periodic_effect) is 24 bytes plus a pointer.
	ffEffectSize    = 16 + 24 + unsafe.Sizeof(uintptr(0))
	ffUploadSize    = 8 + 2*ffEffectSize
	uiSetEvBit      = ioc(iocWrite, 100, 4)
	uiSetKeyBit     = ioc(iocWrite, 101, 4)
	uiSetAbsBit     = ioc(iocWrite, 103, 4)
	uiSetFFBit      = ioc(iocWrite, 107, 4)
	uiDevCreate     = ioc(iocNone, 1, 0)
	uiDevDestroy    = ioc(iocNone, 2, 0)
	uiBeginFFUpload = ioc(iocRW, 200, ffUploadSize)
	uiEndFFUpload   = ioc(iocWrite, 201, ffUploadSize)
	uiBeginFFErase  = ioc(iocRW, 202, 12)
	uiEndFFErase    = ioc(iocWrite, 203, 12)
	timevalSize     = int(unsafe.Sizeof(unix.Timeval{}))
)

type uinputBackend struct{}

func openPlatform() (Backend, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w (load the uinput module and grant access, e.g. a udev rule)", err)
	}
	f.Close()
	return uinputBackend{}, nil
}

func (uinputBackend) Name() string { return "uinput (Xbox 360)" }
func (uinputBackend) Close() error { return nil }

type uinputPad struct {
	f        *os.File
	onRumble RumbleFunc
	mu       sync.Mutex
	effects  map[int16][3]uint16 // id -> strong, weak, length ms
	stop     *time.Timer
	done     chan struct{}
}

func ioctl(fd uintptr, req uintptr, arg uintptr) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

func (uinputBackend) NewPad(onRumble RumbleFunc) (Pad, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fd := f.Fd()
	must := func(req uintptr, v int) {
		if err == nil {
			err = ioctl(fd, req, uintptr(v))
		}
	}
	for _, ev := range []int{evKey, evAbs, evFF, evSyn} {
		must(uiSetEvBit, ev)
	}
	for _, k := range []int{btnA, btnB, btnX, btnY, btnTL, btnTR, btnSelect, btnStart, btnMode, btnThumbL, btnThumbR} {
		must(uiSetKeyBit, k)
	}
	for _, a := range []int{absX, absY, absZ, absRX, absRY, absRZ, absHat0X, absHat0Y} {
		must(uiSetAbsBit, a)
	}
	must(uiSetFFBit, ffRumble)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("configure uinput: %w", err)
	}

	// Legacy struct uinput_user_dev setup.
	var dev bytes.Buffer
	name := make([]byte, 80)
	copy(name, "pad2go Xbox 360 Controller")
	dev.Write(name)
	binary.Write(&dev, binary.LittleEndian, [4]uint16{busUSB, 0x045e, 0x028e, 0x0110})
	binary.Write(&dev, binary.LittleEndian, uint32(16)) // ff_effects_max
	var absmax, absmin, absfuzz, absflat [absCnt]int32
	for _, a := range []int{absX, absY, absRX, absRY} {
		absmin[a], absmax[a], absfuzz[a], absflat[a] = -32768, 32767, 16, 128
	}
	for _, a := range []int{absZ, absRZ} {
		absmax[a] = 255
	}
	for _, a := range []int{absHat0X, absHat0Y} {
		absmin[a], absmax[a] = -1, 1
	}
	for _, arr := range [][absCnt]int32{absmax, absmin, absfuzz, absflat} {
		binary.Write(&dev, binary.LittleEndian, arr)
	}
	if _, err := f.Write(dev.Bytes()); err != nil {
		f.Close()
		return nil, fmt.Errorf("write uinput_user_dev: %w", err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("UI_DEV_CREATE: %w", err)
	}
	p := &uinputPad{f: f, onRumble: onRumble, effects: map[int16][3]uint16{}, done: make(chan struct{})}
	go p.readFF()
	return p, nil
}

func (p *uinputPad) event(buf *bytes.Buffer, typ, code uint16, value int32) {
	buf.Write(make([]byte, timevalSize))
	binary.Write(buf, binary.LittleEndian, typ)
	binary.Write(buf, binary.LittleEndian, code)
	binary.Write(buf, binary.LittleEndian, value)
}

func (p *uinputPad) Update(s mapping.XboxState) error {
	var buf bytes.Buffer
	keys := []struct {
		code uint16
		mask uint16
	}{
		{btnA, mapping.XBA}, {btnB, mapping.XBB}, {btnX, mapping.XBX}, {btnY, mapping.XBY},
		{btnTL, mapping.XBLB}, {btnTR, mapping.XBRB}, {btnSelect, mapping.XBBack},
		{btnStart, mapping.XBStart}, {btnMode, mapping.XBGuide},
		{btnThumbL, mapping.XBLStick}, {btnThumbR, mapping.XBRStick},
	}
	for _, k := range keys {
		var v int32
		if s.Buttons&k.mask != 0 {
			v = 1
		}
		p.event(&buf, evKey, k.code, v)
	}
	hat := func(neg, pos uint16) int32 {
		switch {
		case s.Buttons&neg != 0:
			return -1
		case s.Buttons&pos != 0:
			return 1
		}
		return 0
	}
	// Linux Y axes grow downward; XInput's grow upward.
	p.event(&buf, evAbs, absX, int32(s.LX))
	p.event(&buf, evAbs, absY, invertAxis(s.LY))
	p.event(&buf, evAbs, absRX, int32(s.RX))
	p.event(&buf, evAbs, absRY, invertAxis(s.RY))
	p.event(&buf, evAbs, absZ, int32(s.LeftTrigger))
	p.event(&buf, evAbs, absRZ, int32(s.RightTrigger))
	p.event(&buf, evAbs, absHat0X, hat(mapping.XBLeft, mapping.XBRight))
	p.event(&buf, evAbs, absHat0Y, hat(mapping.XBUp, mapping.XBDown))
	p.event(&buf, evSyn, 0, 0)
	_, err := p.f.Write(buf.Bytes())
	return err
}

func invertAxis(v int16) int32 {
	if v == -32768 {
		return 32767
	}
	return -int32(v)
}

// readFF services force-feedback upload/erase requests and play events.
func (p *uinputPad) readFF() {
	evSize := timevalSize + 8
	buf := make([]byte, evSize*16)
	for {
		n, err := p.f.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+evSize <= n; off += evSize {
			e := buf[off+timevalSize : off+evSize]
			typ := binary.LittleEndian.Uint16(e[0:])
			code := binary.LittleEndian.Uint16(e[2:])
			value := int32(binary.LittleEndian.Uint32(e[4:]))
			switch {
			case typ == evUinput && code == uiFFUpload:
				p.upload(uint32(value))
			case typ == evUinput && code == uiFFErase:
				p.erase(uint32(value))
			case typ == evFF:
				p.play(int16(code), value != 0)
			}
		}
	}
}

func (p *uinputPad) upload(req uint32) {
	up := make([]byte, ffUploadSize)
	binary.LittleEndian.PutUint32(up[0:], req)
	fd := p.f.Fd()
	if err := ioctl(fd, uiBeginFFUpload, uintptr(unsafe.Pointer(&up[0]))); err != nil {
		return
	}
	eff := up[8 : 8+ffEffectSize]
	typ := binary.LittleEndian.Uint16(eff[0:])
	id := int16(binary.LittleEndian.Uint16(eff[2:]))
	length := binary.LittleEndian.Uint16(eff[10:])
	var retval int32
	if typ == ffRumble {
		strong := binary.LittleEndian.Uint16(eff[16:])
		weak := binary.LittleEndian.Uint16(eff[18:])
		p.mu.Lock()
		p.effects[id] = [3]uint16{strong, weak, length}
		p.mu.Unlock()
	} else {
		retval = -int32(unix.EINVAL)
	}
	binary.LittleEndian.PutUint32(up[4:], uint32(retval))
	_ = ioctl(fd, uiEndFFUpload, uintptr(unsafe.Pointer(&up[0])))
}

func (p *uinputPad) erase(req uint32) {
	er := make([]byte, 12)
	binary.LittleEndian.PutUint32(er[0:], req)
	fd := p.f.Fd()
	if err := ioctl(fd, uiBeginFFErase, uintptr(unsafe.Pointer(&er[0]))); err != nil {
		return
	}
	p.mu.Lock()
	delete(p.effects, int16(binary.LittleEndian.Uint32(er[8:])))
	p.mu.Unlock()
	_ = ioctl(fd, uiEndFFErase, uintptr(unsafe.Pointer(&er[0])))
}

func (p *uinputPad) play(id int16, on bool) {
	if p.onRumble == nil {
		return
	}
	p.mu.Lock()
	eff, ok := p.effects[id]
	if p.stop != nil {
		p.stop.Stop()
		p.stop = nil
	}
	if on && ok && eff[2] > 0 {
		p.stop = time.AfterFunc(time.Duration(eff[2])*time.Millisecond, func() { p.onRumble(0, 0) })
	}
	p.mu.Unlock()
	if on && ok {
		p.onRumble(uint8(eff[0]>>8), uint8(eff[1]>>8))
	} else {
		p.onRumble(0, 0)
	}
}

func (p *uinputPad) Close() error {
	p.mu.Lock()
	if p.stop != nil {
		p.stop.Stop()
	}
	p.mu.Unlock()
	err := ioctl(p.f.Fd(), uiDevDestroy, 0)
	return errors.Join(err, p.f.Close())
}

func diagnose() []Check {
	c := Check{Name: "/dev/uinput", OK: true, Detail: "acessível", Help: "output"}
	f, err := os.OpenFile("/dev/uinput", os.O_RDWR, 0)
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.OK, c.Detail = false, "não encontrado"
	case err != nil:
		c.OK, c.Detail = false, "sem permissão"
	default:
		f.Close()
	}
	return []Check{c}
}
