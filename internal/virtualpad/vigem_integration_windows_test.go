//go:build windows && cgo

package virtualpad

import (
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
)

var (
	xinput         = windows.NewLazySystemDLL("xinput1_4.dll")
	xinputGetState = xinput.NewProc("XInputGetState")
	xinputSetState = xinput.NewProc("XInputSetState")
)

// xinputState mirrors XINPUT_STATE.
type xinputState struct {
	Packet       uint32
	Buttons      uint16
	LeftTrigger  uint8
	RightTrigger uint8
	LX, LY       int16
	RX, RY       int16
}

// TestViGEmEndToEnd plugs a virtual pad into ViGEmBus, reads it back through
// XInput and checks that XInput vibration reaches the rumble callback. Needs
// the ViGEmBus driver; enable with S2C_VIGEM_IT=1.
func TestViGEmEndToEnd(t *testing.T) {
	if os.Getenv("S2C_VIGEM_IT") != "1" {
		t.Skip("set S2C_VIGEM_IT=1 to run (needs the ViGEmBus driver)")
	}
	b, err := openPlatform()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rumble := make(chan [2]uint8, 16)
	pad, err := b.NewPad(func(large, small uint8) {
		select {
		case rumble <- [2]uint8{large, small}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pad.Close()

	slot := findSlot(t, pad)
	// XINPUT_VIBRATION: 16-bit motor speeds; ViGEm reports the high byte.
	vib := [2]uint16{0x8000, 0x4000}
	if r, _, _ := xinputSetState.Call(slot, uintptr(unsafe.Pointer(&vib))); r != 0 {
		t.Fatalf("XInputSetState: %d", r)
	}
	// The bus may first report the idle (zero) state.
	timeout := time.After(3 * time.Second)
	for {
		select {
		case got := <-rumble:
			if got == [2]uint8{0x80, 0x40} {
				return
			}
			t.Logf("rumble %v", got)
		case <-timeout:
			t.Fatal("rumble callback never reported [128 64]")
		}
	}
}

// findSlot updates the pad until XInput reports its state, returning the slot.
func findSlot(t *testing.T, pad Pad) uintptr {
	t.Helper()
	want := mapping.XboxState{Buttons: 0x1000, LeftTrigger: 200, LX: 12345, RY: -2000} // A
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := pad.Update(want); err != nil {
			t.Fatal(err)
		}
		for slot := uintptr(0); slot < 4; slot++ {
			var s xinputState
			if r, _, _ := xinputGetState.Call(slot, uintptr(unsafe.Pointer(&s))); r != 0 {
				continue
			}
			if s.Buttons == want.Buttons && s.LeftTrigger == want.LeftTrigger && s.LX == want.LX && s.RY == want.RY {
				t.Logf("virtual pad seen by XInput in slot %d", slot)
				return slot
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("XInput never reported the virtual pad's state")
	return 0
}
