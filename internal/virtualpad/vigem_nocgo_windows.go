//go:build windows && !cgo

package virtualpad

import "errors"

const platformName = "vigem"

func openPlatform() (Backend, error) {
	return nil, errors.New("this build has no ViGEm client: rebuild with cgo (CGO_ENABLED=1 and a MinGW gcc)")
}

func diagnose() []Check {
	return []Check{{Name: "ViGEmBus", OK: false, Detail: "não verificado (build sem cgo)", Help: "output"}}
}
