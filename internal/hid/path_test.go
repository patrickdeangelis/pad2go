package hid

import "testing"

func TestParseHIDPath(t *testing.T) {
	tests := []struct {
		name, path string
		vid, pid   uint16
		ok         bool
	}{
		{"DualSense", `\\?\hid#vid_054c&pid_0ce6#abc`, 0x054c, 0x0ce6, true},
		{"uppercase", `\\?\HID#VID_054C&PID_0CE6#abc`, 0x054c, 0x0ce6, true},
		{"missing", `\\?\hid#device#abc`, 0, 0, false},
		{"Bluetooth", `\\?\hid#{00001124-0000-1000-8000-00805f9b34fb}_vid&0002054c_pid&0ce6#abc`, 0x054c, 0x0ce6, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, p, ok := parsePath(tt.path)
			if v != tt.vid || p != tt.pid || ok != tt.ok {
				t.Fatalf("parsePath() = %04x:%04x, %v; want %04x:%04x, %v", v, p, ok, tt.vid, tt.pid, tt.ok)
			}
		})
	}
}
