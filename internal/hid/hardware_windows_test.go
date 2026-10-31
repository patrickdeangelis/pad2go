//go:build windows

package hid

import (
	"os"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// Run explicitly when a physical controller is available; the ordinary test
// suite must not open hardware owned by the user.
func TestHardwareMotionCalibration(t *testing.T) {
	if os.Getenv("PAD2GO_HARDWARE_TEST") != "1" {
		t.Skip("set PAD2GO_HARDWARE_TEST=1 to inspect connected hardware")
	}
	b := BackendWin{}
	infos, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range infos {
		if info.VendorID != dualsense.VendorID || info.ProductID != dualsense.ProductID {
			continue
		}
		found = true
		t.Run(map[bool]string{true: "bluetooth", false: "usb"}[info.Bluetooth], func(t *testing.T) {
			transport, err := b.Open(info)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			feature := transport.(interface {
				Feature(byte, int) ([]byte, error)
			})
			report, err := feature.Feature(0x05, 41)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = dualsense.ParseMotionCalibration(report, info.Bluetooth); err != nil {
				t.Fatal(err)
			}
			if info.Bluetooth {
				if _, err = feature.Feature(0x09, 20); err != nil {
					t.Fatalf("enable full Bluetooth reports: %v", err)
				}
			}
			var device *dualsense.Device
			if info.Bluetooth {
				device = dualsense.NewBluetoothDevice(transport, "hardware-test", nil)
			} else {
				device = dualsense.NewDevice(transport, "hardware-test", nil)
			}
			// Closing a Device sends output, so close only its read transport for
			// this read-only hardware test.
			input := make(chan protocol.Input, 1)
			device.OnInput(func(in protocol.Input) {
				select {
				case input <- in:
				default:
				}
			})
			select {
			case in := <-input:
				if !in.DualSenseMotionCalibrated {
					t.Fatal("input did not include calibrated motion")
				}
				a := in.DualSenseAccelG
				magnitude := a[0]*a[0] + a[1]*a[1] + a[2]*a[2]
				t.Logf("calibrated acceleration magnitude squared: %.3f g²", magnitude)
				if magnitude < 0.1 || magnitude > 16 {
					t.Fatalf("implausible acceleration: %v", a)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no calibrated input reports")
			}
		})
	}
	if !found {
		t.Skip("no DualSense currently visible to Windows")
	}
}
