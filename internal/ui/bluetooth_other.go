//go:build !windows

package ui

import "errors"

func openBluetoothSettings() error {
	return errors.New("Bluetooth settings shortcut is only available on Windows")
}
