//go:build windows && !nobluetooth

package ble

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"tinygo.org/x/bluetooth"
)

// The WinRT stack behind tinygo's adapter can't report the local address, so
// ask the classic Bluetooth API (bthprops) for the first radio's.
var (
	bthprops           = windows.NewLazySystemDLL("bthprops.cpl")
	procFindFirstRadio = bthprops.NewProc("BluetoothFindFirstRadio")
	procFindRadioClose = bthprops.NewProc("BluetoothFindRadioClose")
	procGetRadioInfo   = bthprops.NewProc("BluetoothGetRadioInfo")
)

// radioInfo mirrors BLUETOOTH_RADIO_INFO.
type radioInfo struct {
	size          uint32
	address       uint64 // BLUETOOTH_ADDRESS: the printed "AA:BB:..." value
	name          [248]uint16
	classOfDevice uint32
	lmpSubversion uint16
	manufacturer  uint16
}

func hostMAC(*bluetooth.Adapter) (uint64, error) {
	params := uint32(4) // BLUETOOTH_FIND_RADIO_PARAMS{dwSize}
	var radio windows.Handle
	find, _, err := procFindFirstRadio.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&radio)))
	if find == 0 {
		return 0, fmt.Errorf("no Bluetooth radio found: %w", err)
	}
	defer procFindRadioClose.Call(find)
	defer func() { _ = windows.CloseHandle(radio) }()
	info := radioInfo{size: uint32(unsafe.Sizeof(radioInfo{}))}
	if r, _, _ := procGetRadioInfo.Call(uintptr(radio), uintptr(unsafe.Pointer(&info))); r != 0 {
		return 0, fmt.Errorf("BluetoothGetRadioInfo: %w", windows.Errno(r))
	}
	return info.address & 0xFFFFFFFFFFFF, nil
}
