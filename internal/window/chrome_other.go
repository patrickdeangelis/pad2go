//go:build !darwin && !nowebview

package window

import "unsafe"

func nativeChrome(unsafe.Pointer) {}
