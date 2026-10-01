//go:build linux

package virtualpad

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/patrickdeangelis/pad2go/internal/mapping"
)

// findEventNode locates the evdev node for the pad, creating it under /dev if
// missing (containers don't get new device nodes automatically).
func findEventNode(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob("/sys/class/input/event*")
		for _, m := range matches {
			name, _ := os.ReadFile(filepath.Join(m, "device/name"))
			if strings.TrimSpace(string(name)) != "pad2go Xbox 360 Controller" {
				continue
			}
			devnum, err := os.ReadFile(filepath.Join(m, "dev"))
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(strings.TrimSpace(string(devnum)), ":")
			major, _ := strconv.Atoi(parts[0])
			minor, _ := strconv.Atoi(parts[1])
			if node := filepath.Join("/dev/input", filepath.Base(m)); fileExists(node) {
				return node
			}
			node := filepath.Join("/dev", filepath.Base(m))
			t.Cleanup(func() { os.Remove(node) })
			if err := unix.Mknod(node, unix.S_IFCHR|0o600, int(unix.Mkdev(uint32(major), uint32(minor)))); err != nil {
				t.Fatalf("mknod: %v", err)
			}
			return node
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("event node for virtual pad not found")
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Requires /dev/uinput and root; run with S2C_UINPUT_IT=1.
func TestUinputEndToEnd(t *testing.T) {
	if os.Getenv("S2C_UINPUT_IT") == "" {
		t.Skip("set S2C_UINPUT_IT=1 to run against a real /dev/uinput")
	}
	b, err := Open("uinput")
	if err != nil {
		t.Fatal(err)
	}
	rumble := make(chan [2]uint8, 4)
	pad, err := b.NewPad(func(l, s uint8) { rumble <- [2]uint8{l, s} })
	if err != nil {
		t.Fatal(err)
	}
	defer pad.Close()

	ev, err := os.OpenFile(findEventNode(t), os.O_RDWR, 0)
	if errors.Is(err, os.ErrPermission) {
		t.Skipf("virtual pad created, but this environment blocks evdev access: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	// Input: A pressed and left stick fully up.
	if err := pad.Update(mapping.XboxState{Buttons: mapping.XBA, LY: 32767}); err != nil {
		t.Fatal(err)
	}
	evSize := timevalSize + 8
	buf := make([]byte, evSize*32)
	_ = ev.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := ev.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	gotA, gotY := false, false
	for off := 0; off+evSize <= n; off += evSize {
		e := buf[off+timevalSize:]
		typ, code := binary.LittleEndian.Uint16(e), binary.LittleEndian.Uint16(e[2:])
		val := int32(binary.LittleEndian.Uint32(e[4:]))
		if typ == evKey && code == btnA && val == 1 {
			gotA = true
		}
		if typ == evAbs && code == absY && val == -32767 {
			gotY = true
		}
	}
	if !gotA || !gotY {
		t.Fatalf("events missing: A=%v Y=%v (% x)", gotA, gotY, buf[:n])
	}

	// Force feedback: upload a rumble effect via EVIOCSFF and play it.
	effect := make([]byte, ffEffectSize)
	binary.LittleEndian.PutUint16(effect[0:], ffRumble)
	binary.LittleEndian.PutUint16(effect[2:], 0xFFFF) // id -1: allocate
	binary.LittleEndian.PutUint16(effect[16:], 0xFFFF)
	binary.LittleEndian.PutUint16(effect[18:], 0x8000)
	eviocsff := ioc(iocWrite, 0x80, ffEffectSize)&^('U'<<8) | 'E'<<8
	if err := ioctl(ev.Fd(), eviocsff, uintptr(unsafe.Pointer(&effect[0]))); err != nil {
		t.Fatalf("EVIOCSFF: %v", err)
	}
	id := binary.LittleEndian.Uint16(effect[2:])
	play := make([]byte, evSize)
	binary.LittleEndian.PutUint16(play[timevalSize:], evFF)
	binary.LittleEndian.PutUint16(play[timevalSize+2:], id)
	binary.LittleEndian.PutUint32(play[timevalSize+4:], 1)
	if _, err := ev.Write(play); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-rumble:
		if got != [2]uint8{255, 128} {
			t.Fatalf("rumble %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rumble callback")
	}
}
