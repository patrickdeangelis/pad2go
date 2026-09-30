//go:build !windows && !linux

package virtualpad

import "errors"

const platformName = ""

func openPlatform() (Backend, error) {
	return nil, errors.New("virtual gamepads are only supported on Windows (ViGEmBus) and Linux (uinput)")
}

func diagnose() []Check { return nil }
