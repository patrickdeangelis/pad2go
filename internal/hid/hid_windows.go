//go:build windows

package hid

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"golang.org/x/sys/windows"
)

var (
	setup             = windows.NewLazySystemDLL("setupapi.dll")
	hidDLL            = windows.NewLazySystemDLL("hid.dll")
	procGetGUID       = hidDLL.NewProc("HidD_GetHidGuid")
	procGetFeature    = hidDLL.NewProc("HidD_GetFeature")
	procGetPreparsed  = hidDLL.NewProc("HidD_GetPreparsedData")
	procFreePreparsed = hidDLL.NewProc("HidD_FreePreparsedData")
	procGetCaps       = hidDLL.NewProc("HidP_GetCaps")
	procGetDetail     = setup.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procEnum          = setup.NewProc("SetupDiEnumDeviceInterfaces")
	procDestroy       = setup.NewProc("SetupDiDestroyDeviceInfoList")
	procCreate        = setup.NewProc("SetupDiGetClassDevsW")
)

const (
	present         = 2
	deviceInterface = 0x10
	invalidHandle   = windows.Handle(^uintptr(0))
)

type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}
type ifaceData struct {
	Size     uint32
	Class    guid
	Flags    uint32
	Reserved uintptr
}
type devInfoData struct {
	Size     uint32
	Class    guid
	DevInst  uint32
	Reserved uintptr
}
type BackendWin struct{}
type hidCaps struct {
	Usage, UsagePage                                                       uint16
	InputReportByteLength, OutputReportByteLength, FeatureReportByteLength uint16
	// HIDP_CAPS is 64 bytes: the rest holds the collection and cap counts we ignore.
	Reserved [27]uint16
}

// bluetoothHIDService is the GUID Windows puts in the path of Bluetooth HID devices.
const bluetoothHIDService = "{00001124-0000-1000-8000-00805f9b34fb}"

func DefaultBackend() Backend { return BackendWin{} }
func (BackendWin) List() ([]Info, error) {
	var g guid
	procGetGUID.Call(uintptr(unsafe.Pointer(&g)))
	h, _, e := procCreate.Call(uintptr(unsafe.Pointer(&g)), 0, 0, present|deviceInterface)
	if h == uintptr(invalidHandle) {
		return nil, e
	}
	defer procDestroy.Call(h)
	var out []Info
	for n := uint32(0); ; n++ {
		d := ifaceData{Size: uint32(unsafe.Sizeof(ifaceData{}))}
		ok, _, _ := procEnum.Call(h, 0, uintptr(unsafe.Pointer(&g)), uintptr(n), uintptr(unsafe.Pointer(&d)))
		if ok == 0 {
			break
		}
		var need uint32
		procGetDetail.Call(h, uintptr(unsafe.Pointer(&d)), 0, 0, uintptr(unsafe.Pointer(&need)), 0)
		buf := make([]byte, need)
		pathOffset, detailSize := 4, uint32(6)
		if unsafe.Sizeof(uintptr(0)) == 8 {
			detailSize = 8
		}
		*(*uint32)(unsafe.Pointer(&buf[0])) = detailSize
		di := devInfoData{Size: uint32(unsafe.Sizeof(devInfoData{}))}
		ok, _, _ = procGetDetail.Call(h, uintptr(unsafe.Pointer(&d)), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), uintptr(unsafe.Pointer(&need)), uintptr(unsafe.Pointer(&di)))
		if ok == 0 {
			continue
		}
		path := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(&buf[pathOffset])))
		vid, pid, parsed := parsePath(path)
		if parsed {
			out = append(out, Info{Path: path, VendorID: vid, ProductID: pid, Bluetooth: strings.Contains(strings.ToLower(path), bluetoothHIDService)})
		}
	}
	return out, nil
}
func (BackendWin) Open(i Info) (dualsense.Transport, error) {
	h, e := windows.CreateFile(windows.StringToUTF16Ptr(i.Path), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if e != nil {
		return nil, e
	}
	t := &winTransport{h: h}
	var data uintptr
	if r, _, _ := procGetPreparsed.Call(uintptr(h), uintptr(unsafe.Pointer(&data))); r != 0 {
		var caps hidCaps
		if status, _, _ := procGetCaps.Call(data, uintptr(unsafe.Pointer(&caps))); status == 0x00110000 {
			t.inputLen, t.outputLen = int(caps.InputReportByteLength), int(caps.OutputReportByteLength)
		}
		procFreePreparsed.Call(data)
	}
	return t, nil
}

type winTransport struct {
	mu                  sync.Mutex
	h                   windows.Handle
	inputLen, outputLen int
}

// Feature reads a HID feature report while the device handle is open.
func (t *winTransport) Feature(id byte, size int) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.h == 0 || size < 1 {
		return nil, fmt.Errorf("HID device closed or invalid feature size")
	}
	b := make([]byte, size)
	b[0] = id
	ok, _, err := procGetFeature.Call(uintptr(t.h), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if ok == 0 {
		return nil, fmt.Errorf("HID feature %#x: %w", id, err)
	}
	if b[0] != id {
		return nil, fmt.Errorf("HID feature: unexpected report %#x", b[0])
	}
	return b, nil
}

func (t *winTransport) Read(b []byte) (count int, retErr error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := windows.CloseHandle(event); retErr == nil {
			retErr = closeErr
		}
	}()
	o := windows.Overlapped{HEvent: event}
	var n uint32
	t.mu.Lock()
	h, inputLen := t.h, t.inputLen
	t.mu.Unlock()
	readBuf := b
	if inputLen > len(readBuf) {
		readBuf = make([]byte, inputLen)
	}
	err = windows.ReadFile(h, readBuf, &n, &o)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(h, &o, &n, true)
	}
	if len(readBuf) != len(b) {
		copy(b, readBuf)
		if int(n) > len(b) {
			n = uint32(len(b))
		}
	}
	return int(n), err
}
func (t *winTransport) Write(b []byte) (retErr error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := windows.CloseHandle(event); retErr == nil {
			retErr = closeErr
		}
	}()
	o := windows.Overlapped{HEvent: event}
	var n uint32
	t.mu.Lock()
	h, outputLen := t.h, t.outputLen
	t.mu.Unlock()
	if outputLen > 0 {
		padded := make([]byte, outputLen)
		copy(padded, b)
		b = padded
	}
	err = windows.WriteFile(h, b, &n, &o)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(h, &o, &n, true)
	}
	return err
}
func (t *winTransport) Close() error {
	t.mu.Lock()
	h := t.h
	if h == 0 {
		t.mu.Unlock()
		return nil
	}
	_ = windows.CancelIoEx(h, nil)
	e := windows.CloseHandle(h)
	t.h = 0
	t.mu.Unlock()
	return e
}
func (t *winTransport) MAC() string {
	t.mu.Lock()
	h := t.h
	t.mu.Unlock()
	if h == 0 {
		return ""
	}
	var b [20]byte
	b[0] = 9
	ok, _, _ := procGetFeature.Call(uintptr(h), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	if ok == 0 || b[0] != 9 {
		return ""
	}
	var any, all byte
	all = 0xff
	for _, v := range b[1:7] {
		any |= v
		all &= v
	}
	if any == 0 || all == 0xff {
		return ""
	}
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[6], b[5], b[4], b[3], b[2], b[1])
}
