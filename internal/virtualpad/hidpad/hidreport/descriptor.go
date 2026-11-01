// Package hidreport builds HID report descriptors from their declared fields.
package hidreport

type itemType byte

const (
	mainItem itemType = iota
	globalItem
	localItem
)

type descriptorBuilder struct {
	data []byte
}

func (b *descriptorBuilder) item(kind itemType, tag byte, value uint32, size int) {
	sizeCode := byte(size)
	if size == 4 {
		sizeCode = 3
	}
	b.data = append(b.data, tag<<4|byte(kind<<2)|sizeCode)
	for i := 0; i < size; i++ {
		b.data = append(b.data, byte(value>>(8*i)))
	}
}

func (b *descriptorBuilder) usagePage(page uint16) {
	if page <= 0xFF {
		b.item(globalItem, 0, uint32(page), 1)
		return
	}
	b.item(globalItem, 0, uint32(page), 2)
}

func (b *descriptorBuilder) usage(usage uint16) {
	if usage <= 0xFF {
		b.item(localItem, 0, uint32(usage), 1)
		return
	}
	b.item(localItem, 0, uint32(usage), 2)
}

func (b *descriptorBuilder) usageMinimum(usage uint16) {
	if usage <= 0xFF {
		b.item(localItem, 1, uint32(usage), 1)
		return
	}
	b.item(localItem, 1, uint32(usage), 2)
}

func (b *descriptorBuilder) usageMaximum(usage uint16) {
	if usage <= 0xFF {
		b.item(localItem, 2, uint32(usage), 1)
		return
	}
	b.item(localItem, 2, uint32(usage), 2)
}

func (b *descriptorBuilder) collection(kind byte) {
	b.item(mainItem, 10, uint32(kind), 1)
}

func (b *descriptorBuilder) endCollection() {
	b.item(mainItem, 12, 0, 0)
}

func (b *descriptorBuilder) reportID(id byte) {
	b.item(globalItem, 8, uint32(id), 1)
}

func (b *descriptorBuilder) reportSize(size byte) {
	b.item(globalItem, 7, uint32(size), 1)
}

func (b *descriptorBuilder) reportCount(count byte) {
	b.item(globalItem, 9, uint32(count), 1)
}

func (b *descriptorBuilder) input(flags byte)   { b.item(mainItem, 8, uint32(flags), 1) }
func (b *descriptorBuilder) output(flags byte)  { b.item(mainItem, 9, uint32(flags), 1) }
func (b *descriptorBuilder) feature(flags byte) { b.item(mainItem, 11, uint32(flags), 1) }

// BuildDualSense constructs the wired DualSense report descriptor using HID
// items. It does not contain or decode a descriptor byte-array fixture.
func BuildDualSense() []byte {
	b := descriptorBuilder{data: make([]byte, 0, 273)}
	b.usagePage(0x01)
	b.usage(0x05)
	b.item(mainItem, 10, 1, 1)

	b.reportID(0x01)
	for _, usage := range []uint16{0x30, 0x31, 0x32, 0x35, 0x33, 0x34} {
		b.usage(usage)
	}
	b.item(globalItem, 1, 0, 1)
	b.item(globalItem, 2, 0xFF, 2)
	b.reportSize(8)
	b.reportCount(6)
	b.input(0x02)

	b.usagePage(0xFF00)
	b.usage(0x20)
	b.reportCount(1)
	b.input(0x02)

	b.usagePage(0x01)
	b.usage(0x39)
	b.item(globalItem, 1, 0, 1)
	b.item(globalItem, 2, 7, 1)
	b.item(globalItem, 3, 0, 1)
	b.item(globalItem, 4, 315, 2)
	b.item(globalItem, 6, 0x14, 1)
	b.reportSize(4)
	b.reportCount(1)
	b.input(0x42)
	b.item(globalItem, 6, 0, 1)

	b.usagePage(0x09)
	b.item(localItem, 1, 1, 1)
	b.item(localItem, 2, 15, 1)
	b.item(globalItem, 1, 0, 1)
	b.item(globalItem, 2, 1, 1)
	b.reportSize(1)
	b.reportCount(15)
	b.input(0x02)

	b.usagePage(0xFF00)
	b.usage(0x21)
	b.reportCount(13)
	b.input(0x02)

	b.usagePage(0xFF00)
	b.usage(0x22)
	b.item(globalItem, 1, 0, 1)
	b.item(globalItem, 2, 0xFF, 2)
	b.reportSize(8)
	b.reportCount(52)
	b.input(0x02)

	b.reportID(0x02)
	b.usage(0x23)
	b.reportCount(47)
	b.output(0x02)

	for _, report := range featureReports {
		b.reportID(report.id)
		b.usage(report.usage)
		b.reportCount(report.length)
		b.feature(0x02)
	}
	b.item(mainItem, 12, 0, 0)
	return b.data
}

// BuildSwitchPro constructs the report descriptor for a Switch Pro
// Controller. It follows the field layout of the controller's published
// descriptor: report 0x30 carries 18 buttons, four 16-bit axes, a hat and 52
// opaque bytes; reports 0x21 and 0x81 and the output reports are 63 opaque
// bytes. Every report is 64 bytes including its ID, which is what SDL's
// HIDAPI Switch driver and USB HID clients expect.
func BuildSwitchPro() []byte {
	b := descriptorBuilder{data: make([]byte, 0, 190)}
	b.usagePage(0x01)
	b.item(globalItem, 1, 0, 1) // logical minimum 0
	b.usage(0x04)               // Joystick
	b.collection(0x01)

	b.reportID(0x30)
	b.usagePage(0x09)
	b.usageMinimum(0x01)
	b.usageMaximum(0x0E)
	b.item(globalItem, 2, 1, 1) // logical maximum 1
	b.reportSize(1)
	b.reportCount(14)
	b.input(0x02)
	b.reportSize(1)
	b.reportCount(2)
	b.input(0x03) // two pad bits, completing the button bytes

	b.usagePage(0x01)
	b.usage(0x30) // X
	b.usage(0x31) // Y
	b.usage(0x32) // Z
	b.usage(0x35) // Rz
	b.item(globalItem, 2, 0xFFFF, 2)
	b.reportSize(16)
	b.reportCount(4)
	b.input(0x02)

	b.usage(0x39) // Hat switch
	b.item(globalItem, 2, 7, 1)
	b.item(globalItem, 3, 0, 1)    // physical minimum
	b.item(globalItem, 4, 315, 2)  // physical maximum
	b.item(globalItem, 6, 0x14, 1) // unit: rotation in degrees
	b.reportSize(4)
	b.reportCount(1)
	b.input(0x02)

	b.usagePage(0x09)
	b.usageMinimum(0x0F)
	b.usageMaximum(0x12)
	b.item(globalItem, 2, 1, 1)
	b.reportSize(1)
	b.reportCount(4)
	b.input(0x02)

	b.reportSize(8)
	b.reportCount(52)
	b.input(0x03)

	b.usagePage(0xFF00)
	for i, id := range []byte{0x21, 0x81} {
		b.reportID(id)
		b.usage(uint16(0x01 + i))
		b.reportSize(8)
		b.reportCount(63)
		b.input(0x03)
	}
	for i, id := range []byte{0x01, 0x10, 0x80, 0x82} {
		b.reportID(id)
		b.usage(uint16(0x03 + i))
		b.reportSize(8)
		b.reportCount(63)
		b.output(0x83)
	}
	b.endCollection()
	return b.data
}

type featureReport struct {
	id     byte
	length byte
	usage  uint16
}

var featureReports = [...]featureReport{
	{0x05, 40, 0x33}, {0x08, 47, 0x34}, {0x09, 19, 0x24}, {0x0A, 26, 0x25},
	{0x20, 63, 0x26}, {0x21, 4, 0x27}, {0x22, 63, 0x40}, {0x80, 63, 0x28},
	{0x81, 63, 0x29}, {0x82, 9, 0x2A}, {0x83, 63, 0x2B}, {0x84, 63, 0x2C},
	{0x85, 2, 0x2D}, {0xA0, 1, 0x2E}, {0xE0, 63, 0x2F}, {0xF0, 63, 0x30},
	{0xF1, 63, 0x31}, {0xF2, 15, 0x32}, {0xF4, 63, 0x35}, {0xF5, 3, 0x36},
}
