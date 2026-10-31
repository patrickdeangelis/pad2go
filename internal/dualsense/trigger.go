package dualsense

import (
	"encoding/binary"
	"fmt"
)

// triggerPreset encodes conservative manual presets in the DualSense's
// 11-byte trigger effect format (USB common payload).
func triggerPreset(name string) ([11]byte, error) {
	var effect [11]byte
	switch name {
	case "off":
		effect[0] = 0x05
	case "resistance":
		effect[0] = 0x21
		var zones uint16
		var forces uint32
		for i := 2; i < 10; i++ {
			zones |= 1 << i
			forces |= 2 << (3 * i)
		}
		binary.LittleEndian.PutUint16(effect[1:3], zones)
		binary.LittleEndian.PutUint32(effect[3:7], forces)
	case "barrier":
		effect[0] = 0x25
		binary.LittleEndian.PutUint16(effect[1:3], 1<<4|1<<7)
		effect[3] = 2
	default:
		return effect, fmt.Errorf("unknown trigger preset %q", name)
	}
	return effect, nil
}

// SetTriggerPresets applies one preset per trigger. Effects are explicitly
// disabled when the device closes.
func (d *Device) SetTriggerPresets(left, right string) error {
	l, err := triggerPreset(left)
	if err != nil {
		return err
	}
	r, err := triggerPreset(right)
	if err != nil {
		return err
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.Closed() {
		return ErrClosed
	}
	d.triggerLeft, d.triggerRight, d.haveTriggers = l, r, true
	d.mu.Lock()
	lm, rm, leds, have := d.left, d.right, d.leds, d.haveLEDs
	d.mu.Unlock()
	return d.writeOutput(lm, rm, leds, have)
}
