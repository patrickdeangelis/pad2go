package hidpad

import "testing"

// The WinUHid IOCTLs are CTL_CODE(FILE_DEVICE_UNKNOWN, 0x800+n,
// METHOD_BUFFERED, access): device type 0x22, FILE_WRITE_DATA (2) for every
// call except GET_INTERFACE_VERSION, which uses FILE_ANY_ACCESS (0).
func TestWinUHIDIoctlCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"SetDeviceInfo", winUHIDSetDeviceInfo, 0x22A000},
		{"SetReportDescriptor", winUHIDSetReportDescriptor, 0x22A004},
		{"SetInstanceID", winUHIDSetInstanceID, 0x22A008},
		{"SetHardwareIDs", winUHIDSetHardwareIDs, 0x22A00C},
		{"CreateDevice", winUHIDCreateDevice, 0x22A010},
		{"StartDevice", winUHIDStartDevice, 0x22A014},
		{"GetNextEvent", winUHIDGetNextEvent, 0x22A018},
		{"CompleteWriteEvent", winUHIDCompleteWriteEvent, 0x22A01C},
		{"CompleteReadEvent", winUHIDCompleteReadEvent, 0x22A020},
		{"GetInterfaceVersion", winUHIDGetInterfaceVersion, 0x222024},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#x, want %#x", tc.name, tc.got, tc.want)
		}
	}
}
