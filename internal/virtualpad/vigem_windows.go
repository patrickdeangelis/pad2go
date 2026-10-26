//go:build windows && cgo

package virtualpad

// The ViGEm client SDK (vigemclient_windows.cpp, MIT) is compiled in, so no
// ViGEmClient.dll is needed; only the ViGEmBus driver must be installed.

/*
#cgo CXXFLAGS: -I${SRCDIR}/vigemclient -I${SRCDIR}/vigemclient/include -w
#cgo CFLAGS: -I${SRCDIR}/vigemclient/include
#cgo LDFLAGS: -lsetupapi
#include <windows.h>
#include "ViGEm/Client.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
)

const platformName = "vigem"

const vigemErrorNone = C.VIGEM_ERROR_NONE

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
	client C.PVIGEM_CLIENT
}

func openPlatform() (Backend, error) {
	client := C.vigem_alloc()
	if client == nil {
		return nil, fmt.Errorf("vigem_alloc failed")
	}
	if r := C.vigem_connect(client); r != vigemErrorNone {
		C.vigem_free(client)
		return nil, fmt.Errorf("vigem_connect: 0x%08x (is the ViGEmBus driver installed?)", uint32(r))
	}
	callbackOnce.Do(func() { callbackPtr = syscall.NewCallback(x360Notification) })
	return &vigem{client: client}, nil
}

func (v *vigem) Name() string { return "ViGEmBus (Xbox 360)" }

func (v *vigem) Close() error {
	C.vigem_disconnect(v.client)
	C.vigem_free(v.client)
	return nil
}

func (v *vigem) NewPad(onRumble RumbleFunc) (Pad, error) {
	target := C.vigem_target_x360_alloc()
	if target == nil {
		return nil, fmt.Errorf("vigem_target_x360_alloc failed")
	}
	if r := C.vigem_target_add(v.client, target); r != vigemErrorNone {
		C.vigem_target_free(target)
		return nil, fmt.Errorf("vigem_target_add: 0x%08x", uint32(r))
	}
	p := &vigemPad{client: v.client, target: target}
	if onRumble != nil {
		targetsMu.Lock()
		targets[uintptr(unsafe.Pointer(target))] = onRumble
		targetsMu.Unlock()
		cb := *(*C.PFN_VIGEM_X360_NOTIFICATION)(unsafe.Pointer(&callbackPtr))
		if r := C.vigem_target_x360_register_notification(v.client, target, cb, nil); r != vigemErrorNone {
			targetsMu.Lock()
			delete(targets, uintptr(unsafe.Pointer(target)))
			targetsMu.Unlock()
		} else {
			p.registered = true
		}
	}
	return p, nil
}

type vigemPad struct {
	client     C.PVIGEM_CLIENT
	target     C.PVIGEM_TARGET
	registered bool
}

func (p *vigemPad) Update(s mapping.XboxState) error {
	rep := C.XUSB_REPORT{
		wButtons: C.USHORT(s.Buttons), bLeftTrigger: C.BYTE(s.LeftTrigger), bRightTrigger: C.BYTE(s.RightTrigger),
		sThumbLX: C.SHORT(s.LX), sThumbLY: C.SHORT(s.LY), sThumbRX: C.SHORT(s.RX), sThumbRY: C.SHORT(s.RY),
	}
	if r := C.vigem_target_x360_update(p.client, p.target, rep); r != vigemErrorNone {
		return fmt.Errorf("vigem_target_x360_update: 0x%08x", uint32(r))
	}
	return nil
}

func (p *vigemPad) Close() error {
	if p.registered {
		C.vigem_target_x360_unregister_notification(p.target)
		targetsMu.Lock()
		delete(targets, uintptr(unsafe.Pointer(p.target)))
		targetsMu.Unlock()
	}
	C.vigem_target_remove(p.client, p.target)
	C.vigem_target_free(p.target)
	return nil
}

// busInstalled reports whether a ViGEmBus device answers. The service alone
// is not enough: the bus device node must exist.
func busInstalled() bool {
	client := C.vigem_alloc()
	if client == nil {
		return false
	}
	defer C.vigem_free(client)
	if C.vigem_connect(client) != vigemErrorNone {
		return false
	}
	C.vigem_disconnect(client)
	return true
}

func diagnose() []Check {
	bus := Check{Name: "ViGEmBus", OK: true, Detail: "instalado", Help: "output"}
	if !busInstalled() {
		bus.OK, bus.Detail = false, "não instalado"
	}
	return []Check{bus}
}
