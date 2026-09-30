package protocol

// Default HD-rumble carrier frequencies (9-bit encoded).
const (
	DefaultLFFreq = 0x0e1
	DefaultHFFreq = 0x1e1
	// MaxAmplitude is the 10-bit amplitude ceiling.
	MaxAmplitude = 1023
)

// Vibration is one 5-byte HD-rumble frame.
type Vibration struct {
	LFFreq   uint16 // 9 bits
	LFEnTone bool
	LFAmp    uint16 // 10 bits
	HFFreq   uint16 // 9 bits
	HFEnTone bool
	HFAmp    uint16 // 10 bits
}

// SilentVibration is a zero-amplitude frame at the default frequencies.
func SilentVibration() Vibration {
	return Vibration{LFFreq: DefaultLFFreq, HFFreq: DefaultHFFreq}
}

// Bytes packs the frame into 40 little-endian bits.
func (v Vibration) Bytes() [5]byte {
	var x uint64
	x |= uint64(v.LFFreq & 0x1FF)
	if v.LFEnTone {
		x |= 1 << 9
	}
	x |= uint64(v.LFAmp&0x3FF) << 10
	x |= uint64(v.HFFreq&0x1FF) << 20
	if v.HFEnTone {
		x |= 1 << 29
	}
	x |= uint64(v.HFAmp&0x3FF) << 30
	var b [5]byte
	for i := range b {
		b[i] = byte(x >> (8 * i))
	}
	return b
}

// LimitJoyConAmplitude keeps LF+HF within the Joy-Con's 10-bit budget while
// preserving the band balance.
func (v Vibration) LimitJoyConAmplitude() Vibration {
	lf, hf := min(int(v.LFAmp), MaxAmplitude), min(int(v.HFAmp), MaxAmplitude)
	if total := lf + hf; total > MaxAmplitude {
		lf = min(MaxAmplitude, int(float64(lf)*MaxAmplitude/float64(total)+0.5))
		hf = MaxAmplitude - lf
	}
	v.LFAmp, v.HFAmp = uint16(lf), uint16(hf)
	return v
}

// FromMotors converts Xbox-style large/small motor strengths (0-255) into a
// frame, as the original does for ViGEm feedback: amp = 800*motor/256, then
// scaled by strength/5 (config vibration strength, 0-10).
func FromMotors(large, small uint8, strength int) Vibration {
	scale := float64(strength) / 5
	lf := min(float64(800*int(large)/256)*scale, MaxAmplitude)
	hf := min(float64(800*int(small)/256)*scale, MaxAmplitude)
	v := SilentVibration()
	v.LFAmp, v.HFAmp = uint16(lf), uint16(hf)
	return v
}

// RumblePacket builds the payload written to the vibration characteristic.
// A packet carries three frames (one per ~5 ms slot) prefixed by 0x50|seq;
// the Pro Controller expects the motor block twice (left and right actuators).
func RumblePacket(seq uint8, frames [3]Vibration, proLike bool) []byte {
	block := make([]byte, 0, 16)
	block = append(block, 0x50+(seq&0x0F))
	for _, f := range frames {
		b := f.Bytes()
		block = append(block, b[:]...)
	}
	out := append([]byte{0x00}, block...)
	if proLike {
		out = append(out, block...)
	}
	return out
}

// VibrationUUID returns the vibration characteristic for a controller kind.
func VibrationUUID(k Kind) string {
	switch {
	case k.ProLike():
		return VibrationWriteProControllerUUID
	case k == KindJoyConLeft:
		return VibrationWriteJoyConLUUID
	default:
		return VibrationWriteJoyConRUUID
	}
}

// ConnectHaptics is the short "connected" buzz: a bass thump followed by a click.
var ConnectHaptics = []struct {
	Frame  Vibration
	HoldMs int
}{
	{Vibration{LFFreq: 0x060, LFAmp: 0x350, HFFreq: 0x0c0, HFAmp: 0x250}, 200},
	{SilentVibration(), 10},
	{Vibration{LFFreq: DefaultLFFreq, LFAmp: 0x030, HFFreq: 0x1e2, HFAmp: 0x300}, 120},
	{SilentVibration(), 0},
}
