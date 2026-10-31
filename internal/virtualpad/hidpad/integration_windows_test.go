//go:build windows && integration

package hidpad

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
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
	if runtime.GOARCH == "386" {
		t.Skip("WinUHid does not support 386")
	}
	mac := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	pairing := make([]byte, 20)
	pairing[0] = 0x09
	copy(pairing[1:7], mac)
	device, err := Open(DualSenseWired, Options{Features: map[byte][]byte{0x09: pairing}})
	if err != nil {
		if errors.Is(err, ErrDriverMissing) || errors.Is(err, ErrPermission) {
			t.Skipf("WinUHid not available: %v", err)
		}
		t.Fatalf("open: %v", err)
	}
	defer device.Close()

	handle := openHIDInterface(t, mac, 0x054C, 0x0CE6, 5*time.Second)
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
		feature := hidFeatureOrFail(t, handle, 64, reportID)
		if feature[0] != reportID {
			t.Fatalf("feature 0x%02x returned report ID 0x%02x", reportID, feature[0])
		}
		switch reportID {
		case 0x05:
			if binary.LittleEndian.Uint16(feature[7:9]) != 8192 {
				t.Fatalf("feature 0x05 calibration %x", feature[:41])
			}
		case 0x09:
			if !bytes.Equal(feature[1:7], mac) {
				t.Fatalf("feature 0x09 MAC %x, want %x", feature[1:7], mac)
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
	if err := writeHIDReport(handle, output); err != nil {
		t.Fatalf("write output report: %v", err)
	}
	waitFeedback(t, device, output, 2*time.Second)

	select {
	case <-device.Done():
		t.Fatal("device closed after a valid input report")
	case <-time.After(100 * time.Millisecond):
	}
	assertCloseWithPendingRead(t, device, mac)
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
	procHidDGetFeature                   = modHID.NewProc("HidD_GetFeature")
)

var hidInterfaceGUID = windows.GUID{
	Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF,
	Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30},
}

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10
	hidpStatusSuccess    = 0x00110000
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

func openHIDInterface(t *testing.T, mac []byte, vendorID, productID uint16, timeout time.Duration) windows.Handle {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if handle := findHIDInterface(mac, vendorID, productID); handle != 0 {
			return handle
		}
		if time.Now().After(deadline) {
			t.Fatalf("no HID interface with MAC %x", mac)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func findHIDInterface(mac []byte, vendorID, productID uint16) windows.Handle {
	guid := hidInterfaceGUID
	set, _, _ := procSetupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&guid)), 0, 0, digcfPresent|digcfDeviceInterface)
	if set == uintptr(windows.InvalidHandle) {
		return 0
	}
	defer procSetupDiDestroyDeviceInfoList.Call(set)

	for index := uint32(0); ; index++ {
		data := spDeviceInterfaceData{Size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		ok, _, _ := procSetupDiEnumDeviceInterfaces.Call(set, 0, uintptr(unsafe.Pointer(&guid)), uintptr(index), uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			return 0
		}
		var required uint32
		procSetupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), 0, 0, uintptr(unsafe.Pointer(&required)), 0)
		if required == 0 {
			continue
		}
		buf := make([]byte, required)
		detail := (*spDeviceInterfaceDetailData)(unsafe.Pointer(&buf[0]))
		detail.Size = 6
		if unsafe.Sizeof(uintptr(0)) == 8 {
			detail.Size = 8
		}
		ok, _, _ = procSetupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(detail)), uintptr(required), 0, 0)
		if ok == 0 {
			continue
		}
		path := windows.UTF16PtrToString(&detail.DevicePath[0])
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			continue
		}
		handle, err := windows.CreateFile(pointer, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err != nil {
			continue
		}
		attrs := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
		ok, _, _ = procHidDGetAttributes.Call(uintptr(handle), uintptr(unsafe.Pointer(&attrs)))
		if ok&0xff == 0 || attrs.VendorID != vendorID || attrs.ProductID != productID {
			windows.CloseHandle(handle)
			continue
		}
		feature, err := hidFeature(handle, 64, 0x09)
		if err == nil && len(feature) >= 7 && bytes.Equal(feature[1:7], mac) {
			return handle
		}
		windows.CloseHandle(handle)
	}
}

func hidCaps(t *testing.T, handle windows.Handle) hidpCaps {
	t.Helper()
	var preparsed uintptr
	if ok, _, err := procHidDGetPreparsedData.Call(uintptr(handle), uintptr(unsafe.Pointer(&preparsed))); ok&0xff == 0 {
		t.Fatalf("HidD_GetPreparsedData: %v", err)
	}
	defer procHidDFreePreparsedData.Call(preparsed)
	var caps hidpCaps
	status, _, _ := procHidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps)))
	if uint32(status) != hidpStatusSuccess {
		t.Fatalf("HidP_GetCaps status %#x", status)
	}
	return caps
}

func waitInputReport(t *testing.T, handle windows.Handle, size int, want []byte, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("input report %x did not arrive within %s", want, timeout)
		}
		wait := 500 * time.Millisecond
		if remaining < wait {
			wait = remaining
		}
		got, err := readHIDReport(handle, size, wait)
		if err == nil && bytes.Equal(got, want) {
			return got
		}
		if err != nil && !errors.Is(err, syscall.Errno(1460)) {
			t.Logf("HID read: %v", err)
		}
	}
}

func waitFeedback(t *testing.T, device Device, want []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case feedback, ok := <-device.Feedback():
			if !ok {
				t.Fatal("Feedback closed before the output report arrived")
			}
			if bytes.Equal(feedback.Raw, want) {
				return
			}
		case <-deadline:
			t.Fatalf("output report %x did not arrive on Feedback", want)
		}
	}
}

func assertCloseWithPendingRead(t *testing.T, device Device, mac []byte) {
	t.Helper()
	handle := openHIDInterface(t, mac, 0x054C, 0x0CE6, 2*time.Second)
	defer windows.CloseHandle(handle)
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		_, _ = readHIDReport(handle, 64, 100*time.Millisecond)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = readHIDReport(handle, 64, 3*time.Second)
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Close with a pending HID read took %s", elapsed)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Closing the handle after this function unblocks the read.
	}
}

func hidFeatureOrFail(t *testing.T, handle windows.Handle, size int, reportID byte) []byte {
	t.Helper()
	feature, err := hidFeature(handle, size, reportID)
	if err != nil {
		t.Fatalf("HidD_GetFeature(0x%02x): %v", reportID, err)
	}
	return feature
}

func hidFeature(handle windows.Handle, size int, reportID byte) ([]byte, error) {
	buf := make([]byte, size)
	buf[0] = reportID
	ok, _, err := procHidDGetFeature.Call(uintptr(handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ok&0xff == 0 {
		return nil, err
	}
	return buf, nil
}

func readHIDReport(handle windows.Handle, size int, timeout time.Duration) ([]byte, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	buf := make([]byte, size)
	var returned uint32
	err = windows.ReadFile(handle, buf, &returned, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		status, waitErr := windows.WaitForSingleObject(event, uint32(timeout/time.Millisecond))
		if waitErr != nil {
			return nil, waitErr
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			_ = windows.CancelIoEx(handle, &overlapped)
			_ = windows.GetOverlappedResult(handle, &overlapped, &returned, true)
			return nil, syscall.Errno(1460)
		}
		err = windows.GetOverlappedResult(handle, &overlapped, &returned, false)
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), buf[:returned]...), nil
}

func writeHIDReport(handle windows.Handle, report []byte) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var written uint32
	err = windows.WriteFile(handle, report, &written, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		status, waitErr := windows.WaitForSingleObject(event, 2000)
		if waitErr != nil {
			_ = windows.CancelIoEx(handle, &overlapped)
			return waitErr
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			_ = windows.CancelIoEx(handle, &overlapped)
			_ = windows.GetOverlappedResult(handle, &overlapped, &written, true)
			return syscall.Errno(1460)
		}
		err = windows.GetOverlappedResult(handle, &overlapped, &written, false)
	}
	return err
}
