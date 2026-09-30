//go:build linux

package virtualpad

import (
	"testing"
	"unsafe"
)

// Values from linux/uinput.h as computed by the C preprocessor on 64-bit Linux.
func TestUinputIoctlNumbers(t *testing.T) {
	cases := map[string][2]uintptr{
		"UI_SET_EVBIT":      {uiSetEvBit, 0x40045564},
		"UI_SET_KEYBIT":     {uiSetKeyBit, 0x40045565},
		"UI_SET_ABSBIT":     {uiSetAbsBit, 0x40045567},
		"UI_SET_FFBIT":      {uiSetFFBit, 0x4004556b},
		"UI_DEV_CREATE":     {uiDevCreate, 0x5501},
		"UI_DEV_DESTROY":    {uiDevDestroy, 0x5502},
		"UI_BEGIN_FF_ERASE": {uiBeginFFErase, 0xc00c55ca},
		"UI_END_FF_ERASE":   {uiEndFFErase, 0x400c55cb},
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		cases["UI_BEGIN_FF_UPLOAD"] = [2]uintptr{uiBeginFFUpload, 0xc06855c8}
		cases["UI_END_FF_UPLOAD"] = [2]uintptr{uiEndFFUpload, 0x406855c9}
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s = %#x, want %#x", name, c[0], c[1])
		}
	}
}

func TestInvertAxis(t *testing.T) {
	if invertAxis(-32768) != 32767 || invertAxis(32767) != -32767 || invertAxis(0) != 0 {
		t.Fatal("invertAxis")
	}
}
