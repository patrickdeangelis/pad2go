//go:build linux

package hidpad

import (
	"io"
	"os"
)

func openPlatformHost() (Host, error) {
	return newUHIDHost(openUHIDDevice), nil
}

func openUHIDDevice() (io.ReadWriteCloser, error) {
	f, err := os.OpenFile("/dev/uhid", os.O_RDWR, 0)
	if err != nil {
		return nil, classifyUHIDOpenError(err)
	}
	return f, nil
}

// Check reports whether /dev/uhid can be opened, without creating a device.
func Check() error {
	f, err := openUHIDDevice()
	if err != nil {
		return err
	}
	return f.Close()
}
