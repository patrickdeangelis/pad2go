package dualsense

// Output report 0x02 offsets (report-ID byte = offset 0).
const (
	outID        = 0x02
	outFlag0     = 1
	outFlag1     = 2
	outRightMtr  = 3 // light, high-frequency motor
	outLeftMtr   = 4 // heavy, low-frequency motor
	outPlayerLED = 44
)

// valid_flag0 bits: classic vibration enable and classic rumble haptics select.
const flag0Rumble = 0x01 | 0x02

// valid_flag1 bit: player-LED byte valid.
const flag1PlayerLED = 0x10

// outputReport builds the 63-byte USB output report. The motors are written
// with their valid bits set; the player LEDs only when setLEDs is true.
func outputReport(rumbleLeft, rumbleRight, leds uint8, setLEDs bool) [OutputReportLen]byte {
	var r [OutputReportLen]byte
	r[0] = outID
	r[outFlag0] = flag0Rumble
	r[outLeftMtr] = rumbleLeft
	r[outRightMtr] = rumbleRight
	if setLEDs {
		r[outFlag1] |= flag1PlayerLED
		r[outPlayerLED] = leds
	}
	return r
}

// PlayerLEDs returns the player-LED byte for a player slot (1..8). Bit N
// lights LED N; slots 5 to 8 all show the full bar, and anything else is off.
func PlayerLEDs(player int) byte {
	switch player {
	case 1:
		return 0x04
	case 2:
		return 0x0A
	case 3:
		return 0x15
	case 4:
		return 0x1B
	case 5, 6, 7, 8:
		return 0x1F
	}
	return 0x00
}
