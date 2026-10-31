package hid

import "errors"

var errUnsupported = errors.New("HID backend is unavailable on this platform")
