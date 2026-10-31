//go:build linux

package hidpad

import "fmt"

func openPlatformHost() (Host, error) {
	return nil, fmt.Errorf("%w: /dev/uhid transport is not implemented in this slice", ErrUnsupported)
}
