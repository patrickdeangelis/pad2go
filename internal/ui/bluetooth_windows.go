//go:build windows

package ui

import "golang.org/x/sys/windows"

func openBluetoothSettings() error {
	return windows.ShellExecute(0, nil, windows.StringToUTF16Ptr("ms-settings:bluetooth"), nil, nil, 1)
}
