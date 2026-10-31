//go:build linux && integration

package hidpad

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestLinuxIntegration is the opt-in Linux acceptance test for AC 34.
// Run it on a host with a readable/writable /dev/uhid and hidraw:
//
//	go test -tags integration ./internal/virtualpad/hidpad -run TestLinuxIntegration -v
func TestLinuxIntegration(t *testing.T) {
	if err := unix.Access("/dev/uhid", unix.R_OK|unix.W_OK); err != nil {
		t.Skipf("/dev/uhid is not readable and writable: %v", err)
	}
	id := fmt.Sprintf("pad2go-%x", os.Getpid())
	device, err := Open(DualSenseWired, Options{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	hidDir := waitForHIDDevice(t, id, 3*time.Second)
	waitForInputDevices(t, hidDir, 3*time.Second)

	raw := openHidraw(t, hidDir)
	defer raw.Close()

	report := neutralReport()
	report[1], report[3] = 0x11, 0x22
	if err := device.SubmitReport(report); err != nil {
		t.Fatalf("submit input report: %v", err)
	}
	readHidrawReport(t, raw, report, 2*time.Second)

	output := make([]byte, 48)
	output[0], output[1], output[2], output[39] = 0x02, 0x03, 0x14, 0x04
	output[3], output[4] = 0x22, 0x11
	if _, err := raw.Write(output); err != nil {
		t.Fatalf("write hidraw output report: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case feedback, ok := <-device.Feedback():
			if !ok {
				t.Fatal("Feedback closed before the output report arrived")
			}
			if bytes.Equal(feedback.Raw, output) {
				select {
				case <-device.Done():
					t.Fatal("device closed after a valid input report")
				case <-time.After(100 * time.Millisecond):
				}
				return
			}
		case <-deadline:
			t.Fatal("the hidraw output report did not arrive on Feedback")
		}
	}
}

func waitForHIDDevice(t *testing.T, uniq string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		matches, _ := filepath.Glob("/sys/bus/hid/devices/0003:054C:0CE6.*")
		for _, dir := range matches {
			data, err := os.ReadFile(filepath.Join(dir, "uniq"))
			if err == nil && strings.TrimSpace(string(data)) == uniq {
				return dir
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no HID device with uniq %q; saw %v", uniq, matches)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForInputDevices(t *testing.T, hidDir string, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		names := inputDeviceNames(hidDir)
		if hasGamepad(names) && hasMotionSensors(names) {
			return names
		}
		if time.Now().After(deadline) {
			t.Fatalf("hid-playstation inputs %q, want a gamepad and motion sensors", names)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func inputDeviceNames(hidDir string) []string {
	paths, _ := filepath.Glob(filepath.Join(hidDir, "input", "input*", "name"))
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		name, err := os.ReadFile(path)
		if err == nil {
			names = append(names, strings.TrimSpace(string(name)))
		}
	}
	return names
}

func hasGamepad(names []string) bool {
	for _, name := range names {
		if !strings.Contains(strings.ToLower(name), "motion") {
			return true
		}
	}
	return false
}

func hasMotionSensors(names []string) bool {
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "motion") {
			return true
		}
	}
	return false
}

func openHidraw(t *testing.T, hidDir string) *os.File {
	t.Helper()
	want := filepath.Base(hidDir)
	paths, _ := filepath.Glob("/sys/class/hidraw/hidraw*")
	for _, path := range paths {
		target, err := filepath.EvalSymlinks(filepath.Join(path, "device"))
		if err != nil || filepath.Base(target) != want {
			continue
		}
		raw, err := os.OpenFile(filepath.Join("/dev", filepath.Base(path)), os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("open %s: %v (run as root or add a hidraw udev rule)", path, err)
		}
		return raw
	}
	t.Fatalf("no hidraw node found for %s", hidDir)
	return nil
}

func readHidrawReport(t *testing.T, raw *os.File, want []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	buf := make([]byte, len(want))
	for time.Now().Before(deadline) {
		if err := raw.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatalf("set hidraw read deadline: %v", err)
		}
		n, err := raw.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			t.Fatalf("read hidraw: %v", err)
		}
		if n == len(want) && bytes.Equal(buf, want) {
			return
		}
	}
	t.Fatalf("submitted report did not appear on hidraw within %s", timeout)
}
