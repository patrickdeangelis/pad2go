//go:build !windows && !linux

package hidpad

import "fmt"

func openPlatformHost() (Host, error) {
	return nil, fmt.Errorf("%w: no platform host", ErrUnsupported)
}

// Check reports whether this platform can create a virtual HID device.
func Check() error {
	return fmt.Errorf("%w: no platform host", ErrUnsupported)
}
