//go:build !windows && !linux

package hidpad

import "fmt"

func openPlatformHost() (Host, error) {
	return nil, fmt.Errorf("%w: no platform host", ErrUnsupported)
}
