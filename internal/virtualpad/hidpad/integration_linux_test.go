//go:build linux && integration

package hidpad

import (
	"os"
	"testing"
	"time"
)

// TestLinuxIntegration is the opt-in Linux acceptance test for AC 34.
// Run it on a host with a readable/writable /dev/uhid:
//
//	go test -tags integration ./internal/virtualpad/hidpad -run TestLinuxIntegration -v
func TestLinuxIntegration(t *testing.T) {
	if _, err := os.Stat("/dev/uhid"); err != nil {
		t.Skipf("/dev/uhid unavailable: %v", err)
	}
	device, err := Open(DualSenseWired, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	if err := device.SubmitReport(neutralReport()); err != nil {
		t.Fatalf("submit input report: %v", err)
	}
	select {
	case <-device.Done():
		t.Fatal("device closed after a valid input report")
	case <-time.After(250 * time.Millisecond):
	}
}
