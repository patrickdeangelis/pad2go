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
