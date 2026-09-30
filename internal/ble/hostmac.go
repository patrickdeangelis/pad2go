//go:build (windows || linux) && !nobluetooth

package ble

import "tinygo.org/x/bluetooth"

func hostMAC(a *bluetooth.Adapter) (uint64, error) {
	addr, err := a.Address()
	if err != nil {
		return 0, err
	}
	// MAC is stored little-endian; the pairing protocol uses the integer value
	// of the printed "AA:BB:..." form.
	var v uint64
	for i := 5; i >= 0; i-- {
		v = v<<8 | uint64(addr.MAC[i])
	}
	return v, nil
}
