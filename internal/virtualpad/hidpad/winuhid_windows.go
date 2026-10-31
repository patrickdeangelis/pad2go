//go:build windows

package hidpad

import "fmt"

func openPlatformHost() (Host, error) {
	return nil, fmt.Errorf("%w: WinUHid transport is not implemented in this slice", ErrUnsupported)
}
