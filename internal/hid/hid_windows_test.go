//go:build windows

package hid

import (
	"strings"
	"testing"
)

// The Windows backend talks to setupapi and hid.dll, so no fake can test it.
// This runs it for real: enumeration must not fail, and every interface it
// returns must carry the HID path prefix and a VID/PID parsed from that path.
func TestWindowsListEnumeratesHIDInterfaces(t *testing.T) {
	infos, err := DefaultBackend().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, i := range infos {
		if !strings.HasPrefix(strings.ToLower(i.Path), `\\?\hid#`) {
			t.Errorf("path %q lacks the hid# prefix (wrong DevicePath offset?)", i.Path)
		}
		if vid, pid, ok := parsePath(i.Path); !ok || vid != i.VendorID || pid != i.ProductID {
			t.Errorf("path %q: Info says %04x:%04x, parse gives %04x:%04x ok=%v", i.Path, i.VendorID, i.ProductID, vid, pid, ok)
		}
	}
	t.Logf("%d HID interfaces on this machine", len(infos))
}
