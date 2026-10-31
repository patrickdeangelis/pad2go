package hidreport

import "testing"

// parseSwitchDescriptor walks the HID items and returns the data-byte length
// of every input and output report, keyed by report ID.
func parseSwitchDescriptor(t *testing.T, descriptor []byte) (inputs, outputs map[byte]int) {
	t.Helper()
	inputs = map[byte]int{}
	outputs = map[byte]int{}
	var reportID byte
	var reportSize, reportCount int
	inputBits := map[byte]int{}
	outputBits := map[byte]int{}

	for i := 0; i < len(descriptor); {
		prefix := descriptor[i]
		i++
		size := int(prefix & 0x03)
		if size == 3 {
			size = 4
		}
		kind := (prefix >> 2) & 0x03
		tag := prefix >> 4
		if i+size > len(descriptor) {
			t.Fatalf("truncated item at %d", i)
		}
		value := 0
		for j := 0; j < size; j++ {
			value |= int(descriptor[i+j]) << (8 * j)
		}
		i += size

		if kind == 1 { // global
			switch tag {
			case 7:
				reportSize = value
			case 8:
				reportID = byte(value)
			case 9:
				reportCount = value
			}
			continue
		}
		if kind != 0 { // local items do not affect sizes
			continue
		}
		switch tag {
		case 8: // input
			inputBits[reportID] += reportSize * reportCount
		case 9: // output
			outputBits[reportID] += reportSize * reportCount
		}
	}
	for id, bits := range inputBits {
		inputs[id] = (bits + 7) / 8
	}
	for id, bits := range outputBits {
		outputs[id] = (bits + 7) / 8
	}
	return inputs, outputs
}

func TestBuildSwitchProReportSizes(t *testing.T) {
	inputs, outputs := parseSwitchDescriptor(t, BuildSwitchPro())

	for _, id := range []byte{0x30, 0x21, 0x81} {
		if got := inputs[id]; got != 63 {
			t.Errorf("input report 0x%02x has %d data bytes, want 63 (64 with ID)", id, got)
		}
	}
	for _, id := range []byte{0x01, 0x10, 0x80, 0x82} {
		if got := outputs[id]; got != 63 {
			t.Errorf("output report 0x%02x has %d data bytes, want 63 (64 with ID)", id, got)
		}
	}
}

func TestBuildSwitchProIsDeterministic(t *testing.T) {
	first, second := BuildSwitchPro(), BuildSwitchPro()
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("descriptors differ in length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("descriptor byte %d differs: 0x%02x vs 0x%02x", i, first[i], second[i])
		}
	}
}

func TestBuildSwitchProHeader(t *testing.T) {
	descriptor := BuildSwitchPro()
	want := []byte{0x05, 0x01, 0x15, 0x00, 0x09, 0x04, 0xA1, 0x01, 0x85, 0x30}
	if len(descriptor) < len(want) {
		t.Fatalf("descriptor is only %d bytes", len(descriptor))
	}
	for i := range want {
		if descriptor[i] != want[i] {
			t.Fatalf("descriptor[%d] = 0x%02x, want 0x%02x", i, descriptor[i], want[i])
		}
	}
}
