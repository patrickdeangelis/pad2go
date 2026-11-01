//go:build windows && !cgo

package virtualpad

import "fmt"

const platformName = "vigem"

func openPlatform() (Backend, error) {
	return nil, fmt.Errorf("%w: rebuild with cgo (CGO_ENABLED=1 and a MinGW gcc)", ErrNoCgo)
}

func diagnose() []Check {
	return []Check{{Name: "ViGEmBus", OK: false, Detail: "build sem ViGEm (precisa de cgo)", Help: "output"}}
}
