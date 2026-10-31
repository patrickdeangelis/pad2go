//go:build !windows

package hid

// DefaultBackend is the stub on platforms without a HID implementation.
func DefaultBackend() Backend { return stubBackend{} }
