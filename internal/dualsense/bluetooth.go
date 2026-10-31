package dualsense

import (
	"encoding/binary"
	"hash/crc32"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// Bluetooth report framing follows the USB common payload. The CRC includes
// the HID transaction prefix as specified by the Linux hid-playstation driver.
// https://github.com/torvalds/linux/blob/master/drivers/hid/hid-playstation.c
const bluetoothReportLen = 78

func bluetoothCRC(prefix byte, report []byte) uint32 {
	crc := crc32.Update(0, crc32.IEEETable, []byte{prefix})
	return crc32.Update(crc, crc32.IEEETable, report)
}

// ParseBluetooth accepts only complete, CRC-valid full Bluetooth reports.
func ParseBluetooth(report []byte) (protocol.Input, bool) {
	return parseBluetoothWithDeadzone(report, stickDeadzone)
}

func parseBluetoothWithDeadzone(report []byte, deadzone float64) (protocol.Input, bool) {
	if len(report) != bluetoothReportLen || report[0] != 0x31 {
		return protocol.Input{}, false
	}
	if bluetoothCRC(0xa1, report[:74]) != binary.LittleEndian.Uint32(report[74:]) {
		return protocol.Input{}, false
	}
	var usb [InputReportLen]byte
	usb[0] = 0x01
	copy(usb[1:], report[2:65])
	return parseWithDeadzone(usb[:], deadzone)
}

// bluetoothResetLEDs releases the controller's startup light animation.
func bluetoothResetLEDs(sequence byte) [bluetoothReportLen]byte {
	var report [bluetoothReportLen]byte
	report[0], report[1], report[2] = 0x31, (sequence&0x0f)<<4, 0x10
	report[41], report[44] = 0x02, 0x02
	binary.LittleEndian.PutUint32(report[74:], bluetoothCRC(0xa2, report[:74]))
	return report
}

// bluetoothOutputReport wraps the shared USB output payload in Bluetooth
// framing. Sequence advances modulo 16 for consecutive writes.
func bluetoothOutputReport(left, right, leds uint8, setLEDs bool, sequence byte) [bluetoothReportLen]byte {
	var report [bluetoothReportLen]byte
	report[0], report[1], report[2] = 0x31, (sequence&0x0f)<<4, 0x10
	usb := outputReport(left, right, leds, setLEDs)
	copy(report[3:50], usb[1:48])
	binary.LittleEndian.PutUint32(report[74:], bluetoothCRC(0xa2, report[:74]))
	return report
}
