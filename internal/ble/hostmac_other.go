//go:build !windows && !linux && !nobluetooth

package ble

import (
	"errors"

	"tinygo.org/x/bluetooth"
)

func hostMAC(*bluetooth.Adapter) (uint64, error) {
	return 0, errors.New("reading the adapter address is not supported on this platform; set host_mac in the config")
}
