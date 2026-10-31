package hidpad

// Rumble contains the two DualSense motor strengths.
type Rumble struct {
	Left  uint8
	Right uint8
}

// RGB is a lightbar color.
type RGB struct {
	Red   uint8
	Green uint8
	Blue  uint8
}

// Feedback is one copied host output report and its recognized fields.
type Feedback struct {
	Raw        []byte
	Known      bool
	Flag0      byte
	Flag1      byte
	Flag2      byte
	Rumble     *Rumble
	Lightbar   *RGB
	PlayerLEDs *uint8
}

// ControlEvent is a non-blocking notification of an output or SET_FEATURE.
type ControlEvent struct {
	Kind     HostEventKind
	ReportID byte
	Data     []byte
	Err      error
}

func decodeFeedback(report []byte) Feedback {
	feedback := Feedback{Raw: append([]byte(nil), report...)}
	if len(report) == 0 || report[0] != reportOutputID || len(report) < 48 || len(report) > 63 {
		return feedback
	}
	feedback.Known = true
	feedback.Flag0 = report[1]
	feedback.Flag1 = report[2]
	feedback.Flag2 = report[39]

	if feedback.Flag0&0x03 != 0 || feedback.Flag2&0x04 != 0 {
		feedback.Rumble = &Rumble{Right: report[3], Left: report[4]}
	}
	if feedback.Flag1&0x04 != 0 {
		feedback.Lightbar = &RGB{Red: report[45], Green: report[46], Blue: report[47]}
	}
	if feedback.Flag1&0x10 != 0 {
		playerLEDs := report[44]
		feedback.PlayerLEDs = &playerLEDs
	}
	return feedback
}
