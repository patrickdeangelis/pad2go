package dualsense

import (
	"encoding/binary"
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestTriggerPresetsAndClose(t *testing.T) {
	for _, bt := range []bool{false, true} {
		tr := newFakeTransport()
		var d *Device
		if bt {
			d = NewBluetoothDevice(tr, "id", nil)
		} else {
			d = NewDevice(tr, "id", nil)
		}
		if err := d.SetTriggerPresets("resistance", "barrier"); err != nil {
			t.Fatal(err)
		}
		if err := d.Rumble(protocol.FromMotors(200, 100, 5)); err != nil {
			t.Fatal(err)
		}
		r := tr.lastWrite(t)
		shift := 0
		if bt {
			shift = 2
			if binary.LittleEndian.Uint32(r[74:]) != bluetoothCRC(0xa2, r[:74]) {
				t.Fatal("bad CRC")
			}
		}
		if r[1+shift]&0x0c != 0x0c || r[11+shift] != 0x25 || r[22+shift] != 0x21 {
			t.Fatalf("trigger report %x", r)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		r = tr.lastWrite(t)
		if r[11+shift] != 5 || r[22+shift] != 5 {
			t.Fatalf("trigger effects not disabled: %x", r)
		}
	}
	if _, err := triggerPreset("unknown"); err == nil {
		t.Fatal("unknown effect accepted")
	}
}
