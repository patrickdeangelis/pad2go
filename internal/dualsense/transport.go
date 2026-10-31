// Package dualsense drives a Sony DualSense over USB or Bluetooth HID: it decodes input
// reports into protocol.Input and encodes output reports for rumble and the
// player LEDs. The byte I/O itself is a Transport supplied by internal/hid.
package dualsense

// USB identifiers of the DualSense.
const (
	VendorID  uint16 = 0x054C
	ProductID uint16 = 0x0CE6
)

// Report sizes over USB, including the report-ID byte.
const (
	InputReportLen  = 64
	OutputReportLen = 63
)

// Transport is the raw HID byte pipe to one opened device.
type Transport interface {
	// Read blocks until one full input report arrives and returns its length.
	// It returns an error when the device is unplugged or closed.
	Read(buf []byte) (int, error)
	// Write sends one output report (first byte is the report ID).
	Write(report []byte) error
	Close() error
}
