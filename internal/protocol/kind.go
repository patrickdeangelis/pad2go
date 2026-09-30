package protocol

// Kind is a controller's family, derived once from its product ID. Everything
// that varies by model (report layout, rumble characteristic, stick count,
// deadzone family, Joy-Con orientation) keys off Kind rather than raw PIDs.
type Kind int

const (
	KindUnknown Kind = iota
	KindJoyConLeft
	KindJoyConRight
	KindPro
	KindGameCube
)

// KindOf classifies a product ID.
func KindOf(pid uint16) Kind {
	switch pid {
	case JoyCon2LeftPID, JoyConLPID:
		return KindJoyConLeft
	case JoyCon2RightPID, JoyConRPID:
		return KindJoyConRight
	case ProController2PID, ProControllerPID:
		return KindPro
	case NSOGameCubeControllerPID:
		return KindGameCube
	}
	return KindUnknown
}

// IsJoyCon reports whether k is a single Joy-Con of either side.
func (k Kind) IsJoyCon() bool { return k == KindJoyConLeft || k == KindJoyConRight }

// ProLike reports whether k uses the two-stick, dual-actuator layout
// (Pro rumble characteristic, motor block sent twice).
func (k Kind) ProLike() bool { return k == KindPro || k == KindGameCube }

// DeadzoneFamily is the key used by the joystick_deadzone_percent setting.
func (k Kind) DeadzoneFamily() string {
	switch k {
	case KindJoyConLeft, KindJoyConRight:
		return "joycon"
	case KindGameCube:
		return "nso_gamecube_controller"
	}
	return "pro_controller"
}

func (k Kind) String() string {
	return [...]string{"unknown", "Joy-Con (L)", "Joy-Con (R)", "Pro Controller", "GameCube"}[k]
}
