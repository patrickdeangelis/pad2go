//go:build windows && integration

package hidpad

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestWindowsIntegration is the opt-in Windows acceptance test for AC 29.
// Run it only on a machine with the pinned WinUHid driver installed:
//
//	go test -tags integration ./internal/virtualpad/hidpad -run TestWindowsIntegration -v
func TestWindowsIntegration(t *testing.T) {
	id := fmt.Sprintf("pad2go-%x", os.Getpid())
	device, err := Open(DualSenseWired, Options{ID: id})
	if err != nil {
		if errors.Is(err, ErrDriverMissing) || errors.Is(err, ErrPermission) {
			t.Skipf("WinUHid not available: %v", err)
		}
		t.Fatalf("open: %v", err)
	}
	defer device.Close()

	handle := openHIDInterface(t, id, 0x054C, 0x0CE6)
	defer windows.CloseHandle(handle)
	caps := hidCaps(t, handle)
	if caps.InputReportByteLength != 64 || caps.OutputReportByteLength != 48 || caps.FeatureReportByteLength < 41 {
		t.Fatalf("report lengths in=%d out=%d feature=%d, want 64/48/>=41",
			caps.InputReportByteLength, caps.OutputReportByteLength, caps.FeatureReportByteLength)
	}

	report := neutralReport()
	report[1], report[3] = 0x11, 0x22
	if err := device.SubmitReport(report); err != nil {
		t.Fatalf("submit input report: %v", err)
	}
	readBack := waitInputReport(t, handle, int(caps.InputReportByteLength), report, 2*time.Second)
	if !bytes.Equal(readBack, report) {
		t.Fatalf("input report read back %x, want %x", readBack, report)
	}

	for _, reportID := range []byte{0x05, 0x09, 0x20} {
		feature := hidFeature(t, handle, int(caps.FeatureReportByteLength), reportID)
		if feature[0] != reportID {
			t.Fatalf("feature 0x%02x returned report ID 0x%02x", reportID, feature[0])
		}
		switch reportID {
		case 0x05:
			if binary.LittleEndian.Uint16(feature[7:9]) != 8192 {
				t.Fatalf("feature 0x05 calibration %x", feature[:41])
			}
		case 0x09:
			if !validLocalUnicastMAC(feature[1:7]) {
				t.Fatalf("feature 0x09 MAC %x is not a local unicast address", feature[1:7])
			}
		case 0x20:
			if binary.LittleEndian.Uint32(feature[28:32]) != 0x15 {
				t.Fatalf("feature 0x20 firmware %x", feature[:46])
			}
		}
	}

	output := make([]byte, caps.OutputReportByteLength)
	output[0], output[1], output[2], output[39] = 0x02, 0x03, 0x14, 0x04
	output[3], output[4] = 0x22, 0x11
	if ok, _, err := procHidDSetOutputReport.Call(uintptr(handle), uintptr(unsafe.Pointer(&output[0])), uintptr(len(output))); ok == 0 {
		t.Fatalf("HidD_SetOutputReport: %v", err)
	}
	select {
	case feedback := <-device.Feedback():
		if !bytes.Equal(feedback.Raw, output) {
			t.Fatalf("output feedback %x, want %x", feedback.Raw, output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Feedback for the output report")
	}

	select {
	case <-device.Done():
		t.Fatal("device closed after a valid input report")
	case <-time.After(100 * time.Millisecond):
	}
}

var (
	modSetupAPI = windows.NewLazySystemDLL("setupapi.dll")
	modHID      = windows.NewLazySystemDLL("hid.dll")

	procSetupDiGetClassDevsW             = modSetupAPI.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces      = modSetupAPI.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW = modSetupAPI.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList     = modSetupAPI.NewProc("SetupDiDestroyDeviceInfoList")
	procHidDGetAttributes                = modHID.NewProc("HidD_GetAttributes")
	procHidDGetPreparsedData             = modHID.NewProc("HidD_GetPreparsedData")
	procHidDFreePreparsedData            = modHID.NewProc("HidD_FreePreparsedData")
	procHidPGetCaps                      = modHID.NewProc("HidP_GetCaps")
	procHidDGetInputReport               = modHID.NewProc("HidD_GetInputReport")
	procHidDGetFeature                   = modHID.NewProc("HidD_GetFeature")
	procHidDSetOutputReport              = modHID.NewProc("HidD_SetOutputReport")
)

var hidInterfaceGUID = windows.GUID{
	Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF,
	Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30},
}

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10
)

type spDeviceInterfaceData struct {
	Size               uint32
	InterfaceClassGUID windows.GUID
	Flags              uint32
	Reserved           uintptr
}

type spDeviceInterfaceDetailData struct {
	Size       uint32
	DevicePath [1]uint16
}

type hidAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

func openHIDInterface(t *testing.T, instanceID string, vendorID, productID uint16) windows.Handle {
	t.Helper()
	guid := hidInterfaceGUID
	set, _, err := procSetupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&guid)), 0, 0, digcfPresent|digcfDeviceInterface)
	if set == uintptr(windows.InvalidHandle) {
		t.Fatalf("SetupDiGetClassDevs: %v", err)
	}
	defer procSetupDiDestroyDeviceInfoList.Call(set)

	for index := uint32(0); ; index++ {
		data := spDeviceInterfaceData{Size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		ok, _, _ := procSetupDiEnumDeviceInterfaces.Call(set, 0, uintptr(unsafe.Pointer(&guid)), uintptr(index), uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			break
		}
		var required uint32
		procSetupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), 0, 0, uintptr(unsafe.Pointer(&required)), 0)
		if required == 0 {
			continue
		}
		buf := make([]byte, required)
		detail := (*spDeviceInterfaceDetailData)(unsafe.Pointer(&buf[0]))
		detail.Size = uint32(6)
		if unsafe.Sizeof(uintptr(0)) == 8 {
			detail.Size = 8
		}
		ok, _, _ = procSetupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(detail)), uintptr(required), 0, 0)
		if ok == 0 {
			continue
		}
		path := windows.UTF16PtrToString(&detail.DevicePath[0])
		if !strings.Contains(strings.ToLower(path), strings.ToLower(instanceID)) {
			continue
		}
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(pointer, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		attrs := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
		ok, _, _ = procHidDGetAttributes.Call(uintptr(handle), uintptr(unsafe.Pointer(&attrs)))
		if ok == 0 || attrs.VendorID != vendorID || attrs.ProductID != productID {
			windows.CloseHandle(handle)
			continue
		}
		return handle
	}
	t.Fatalf("no HID interface with instance ID %q", instanceID)
	return 0
}

func hidCaps(t *testing.T, handle windows.Handle) hidpCaps {
	t.Helper()
	var preparsed uintptr
	if ok, _, err := procHidDGetPreparsedData.Call(uintptr(handle), uintptr(unsafe.Pointer(&preparsed))); ok == 0 {
		t.Fatalf("HidD_GetPreparsedData: %v", err)
	}
	defer procHidDFreePreparsedData.Call(preparsed)
	var caps hidpCaps
	if status, _, _ := procHidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps))); status != 0 {
		t.Fatalf("HidP_GetCaps status %#x", status)
	}
	return caps
}

func waitInputReport(t *testing.T, handle windows.Handle, size int, want []byte, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	buf := make([]byte, size)
	for time.Now().Before(deadline) {
		buf[0] = reportInputID
		if ok, _, _ := procHidDGetInputReport.Call(uintptr(handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); ok != 0 && bytes.Equal(buf, want) {
			return append([]byte(nil), buf...)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("input report %x did not arrive within %s", want, timeout)
	return nil
}

func hidFeature(t *testing.T, handle windows.Handle, size int, reportID byte) []byte {
	t.Helper()
	buf := make([]byte, size)
	buf[0] = reportID
	if ok, _, err := procHidDGetFeature.Call(uintptr(handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); ok == 0 {
		t.Fatalf("HidD_GetFeature(0x%02x): %v", reportID, err)
	}
	return buf
}
