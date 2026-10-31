//go:build !windows

package ui

import "errors"

func openBluetoothSettings() error {
	return errors.New("bluetooth settings shortcut is only available on Windows")
}
