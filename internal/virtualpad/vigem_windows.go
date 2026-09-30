//go:build windows

package virtualpad

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/angelispatrick/pad2go/internal/mapping"
)

const platformName = "vigem"

// ViGEmClient.dll must sit next to the executable or on PATH. It ships with
// ViGEmBus-based tools (e.g. the vgamepad Python package) and can be built
// from https://github.com/nefarius/ViGEmClient.
var (
	vigemDLL           = windows.NewLazyDLL("ViGEmClient.dll")
	procAlloc          = vigemDLL.NewProc("vigem_alloc")
	procFree           = vigemDLL.NewProc("vigem_free")
	procConnect        = vigemDLL.NewProc("vigem_connect")
	procDisconnect     = vigemDLL.NewProc("vigem_disconnect")
	procX360Alloc      = vigemDLL.NewProc("vigem_target_x360_alloc")
	procTargetAdd      = vigemDLL.NewProc("vigem_target_add")
	procTargetRemove   = vigemDLL.NewProc("vigem_target_remove")
	procTargetFree     = vigemDLL.NewProc("vigem_target_free")
	procX360Update     = vigemDLL.NewProc("vigem_target_x360_update")
	procX360Register   = vigemDLL.NewProc("vigem_target_x360_register_notification")
	procX360Unregister = vigemDLL.NewProc("vigem_target_x360_unregister_notification")
)

const vigemErrorNone = 0x20000000

var (
	callbackOnce sync.Once
	callbackPtr  uintptr
	targetsMu    sync.Mutex
	targets      = map[uintptr]RumbleFunc{}
)

// A single callback trampoline dispatches by target handle, since
// syscall.NewCallback slots are a limited, never-freed resource.
func x360Notification(client, target, large, small, led, user uintptr) uintptr {
	targetsMu.Lock()
	fn := targets[target]
	targetsMu.Unlock()
	if fn != nil {
		fn(uint8(large), uint8(small))
	}
	return 0
}

type vigem struct {
	client uintptr
}

func openPlatform() (Backend, error) {
	if err := vigemDLL.Load(); err != nil {
		return nil, fmt.Errorf("load ViGEmClient.dll: %w", err)
	}
	client, _, _ := procAlloc.Call()
	if client == 0 {
		return nil, fmt.Errorf("vigem_alloc failed")
	}
	if r, _, _ := procConnect.Call(client); r != vigemErrorNone {
		procFree.Call(client)
		return nil, fmt.Errorf("vigem_connect: 0x%08x (is the ViGEmBus driver installed?)", r)
	}
	callbackOnce.Do(func() { callbackPtr = syscall.NewCallback(x360Notification) })
	return &vigem{client: client}, nil
}

func (v *vigem) Name() string { return "ViGEmBus (Xbox 360)" }

func (v *vigem) Close() error {
	procDisconnect.Call(v.client)
	procFree.Call(v.client)
	return nil
}

func (v *vigem) NewPad(onRumble RumbleFunc) (Pad, error) {
	target, _, _ := procX360Alloc.Call()
	if target == 0 {
		return nil, fmt.Errorf("vigem_target_x360_alloc failed")
	}
	if r, _, _ := procTargetAdd.Call(v.client, target); r != vigemErrorNone {
		procTargetFree.Call(target)
		return nil, fmt.Errorf("vigem_target_add: 0x%08x", r)
	}
	p := &vigemPad{client: v.client, target: target}
	if onRumble != nil {
		targetsMu.Lock()
		targets[target] = onRumble
		targetsMu.Unlock()
		if r, _, _ := procX360Register.Call(v.client, target, callbackPtr, 0); r != vigemErrorNone {
			targetsMu.Lock()
			delete(targets, target)
			targetsMu.Unlock()
		} else {
			p.registered = true
		}
	}
	return p, nil
}

type vigemPad struct {
	client, target uintptr
	registered     bool
}

// xusbReport mirrors XUSB_REPORT (12 bytes).
type xusbReport struct {
	Buttons      uint16
	LeftTrigger  uint8
	RightTrigger uint8
	LX, LY       int16
	RX, RY       int16
}

func (p *vigemPad) Update(s mapping.XboxState) error {
	rep := xusbReport{s.Buttons, s.LeftTrigger, s.RightTrigger, s.LX, s.LY, s.RX, s.RY}
	var r uintptr
	if runtime.GOARCH == "arm64" {
		// AArch64 passes a 12-byte struct by value in two registers.
		var b [16]byte
		binary.LittleEndian.PutUint16(b[0:], rep.Buttons)
		b[2], b[3] = rep.LeftTrigger, rep.RightTrigger
		binary.LittleEndian.PutUint16(b[4:], uint16(rep.LX))
		binary.LittleEndian.PutUint16(b[6:], uint16(rep.LY))
		binary.LittleEndian.PutUint16(b[8:], uint16(rep.RX))
		binary.LittleEndian.PutUint16(b[10:], uint16(rep.RY))
		r, _, _ = procX360Update.Call(p.client, p.target,
			uintptr(binary.LittleEndian.Uint64(b[0:])), uintptr(binary.LittleEndian.Uint64(b[8:])))
	} else {
		// x64 passes structs larger than 8 bytes by reference.
		r, _, _ = procX360Update.Call(p.client, p.target, uintptr(unsafe.Pointer(&rep)))
	}
	if r != vigemErrorNone {
		return fmt.Errorf("vigem_target_x360_update: 0x%08x", r)
	}
	return nil
}

func (p *vigemPad) Close() error {
	if p.registered {
		procX360Unregister.Call(p.target)
		targetsMu.Lock()
		delete(targets, p.target)
		targetsMu.Unlock()
	}
	procTargetRemove.Call(p.client, p.target)
	procTargetFree.Call(p.target)
	return nil
}
