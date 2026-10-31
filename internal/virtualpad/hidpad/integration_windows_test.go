//go:build windows && integration

package hidpad

import (
	"errors"
	"testing"
	"time"
)

// TestWindowsIntegration is the opt-in Windows acceptance test for AC 29.
// Run it only on a machine with the pinned WinUHid driver installed:
//
//	go test -tags integration ./internal/virtualpad/hidpad -run TestWindowsIntegration -v
func TestWindowsIntegration(t *testing.T) {
	device, err := Open(DualSenseWired, Options{})
	if err != nil {
		if errors.Is(err, ErrDriverMissing) || errors.Is(err, ErrPermission) {
			t.Skipf("WinUHid not available: %v", err)
		}
		t.Fatalf("open: %v", err)
	}
	defer device.Close()

	report := neutralReport()
	if err := device.SubmitReport(report); err != nil {
		t.Fatalf("submit input report: %v", err)
	}

	select {
	case <-device.Done():
		t.Fatal("device closed after a valid input report")
	case <-time.After(250 * time.Millisecond):
	}

	if err := device.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
