//go:build !windows && !linux

package hidpad

import "fmt"

// errNoHost is a variable rather than a fresh error per call so callers keep
// their error checks on every platform.
var errNoHost = fmt.Errorf("%w: no platform host", ErrUnsupported)

func openPlatformHost() (Host, error) {
	return nil, errNoHost
}

// Check reports whether this platform can create a virtual HID device.
func Check() error {
	return errNoHost
}
