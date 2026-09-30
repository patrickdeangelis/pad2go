package protocol

import "sort"

// Switch button bits as they appear in the 32-bit button field of an input report.
const (
	BtnY        uint32 = 0x00000001
	BtnX        uint32 = 0x00000002
	BtnB        uint32 = 0x00000004
	BtnA        uint32 = 0x00000008
	BtnSRR      uint32 = 0x00000010
	BtnSLR      uint32 = 0x00000020
	BtnR        uint32 = 0x00000040
	BtnZR       uint32 = 0x00000080
	BtnMinus    uint32 = 0x00000100
	BtnPlus     uint32 = 0x00000200
	BtnRStick   uint32 = 0x00000400
	BtnLStick   uint32 = 0x00000800
	BtnHome     uint32 = 0x00001000
	BtnCapture  uint32 = 0x00002000
	BtnC        uint32 = 0x00004000
	BtnDown     uint32 = 0x00010000
	BtnUp       uint32 = 0x00020000
	BtnRight    uint32 = 0x00040000
	BtnLeft     uint32 = 0x00080000
	BtnSRL      uint32 = 0x00100000
	BtnSLL      uint32 = 0x00200000
	BtnL        uint32 = 0x00400000
	BtnZL       uint32 = 0x00800000
	BtnGR       uint32 = 0x01000000
	BtnGL       uint32 = 0x02000000
	BtnPSLTouch uint32 = 0x04000000
	BtnPSRTouch uint32 = 0x08000000
	BtnPSCClick uint32 = 0x20000000
	BtnGCLClick uint32 = 0x40000000
	BtnGCRClick uint32 = 0x80000000
)

// ButtonsByName uses the names from the original config.yaml.
var ButtonsByName = map[string]uint32{
	"Y": BtnY, "X": BtnX, "B": BtnB, "A": BtnA,
	"SR_R": BtnSRR, "SL_R": BtnSLR, "R": BtnR, "ZR": BtnZR,
	"MINUS": BtnMinus, "PLUS": BtnPlus, "R_STK": BtnRStick, "L_STK": BtnLStick,
	"HOME": BtnHome, "Home": BtnHome, "CAPT": BtnCapture, "Capture": BtnCapture,
	"C": BtnC, "Chat": BtnC,
	"DOWN": BtnDown, "UP": BtnUp, "RIGHT": BtnRight, "LEFT": BtnLeft,
	"SR_L": BtnSRL, "SL_L": BtnSLL, "L": BtnL, "ZL": BtnZL,
	"GR": BtnGR, "GL": BtnGL,
	"PS_L_Touch": BtnPSLTouch, "PS_R_Touch": BtnPSRTouch, "PS_C_Click": BtnPSCClick,
	"GC_L_CLICK": BtnGCLClick, "GC_R_CLICK": BtnGCRClick,
}

var canonicalNames = map[uint32]string{
	BtnY: "Y", BtnX: "X", BtnB: "B", BtnA: "A",
	BtnSRR: "SR_R", BtnSLR: "SL_R", BtnR: "R", BtnZR: "ZR",
	BtnMinus: "MINUS", BtnPlus: "PLUS", BtnRStick: "R_STK", BtnLStick: "L_STK",
	BtnHome: "HOME", BtnCapture: "CAPT", BtnC: "C",
	BtnDown: "DOWN", BtnUp: "UP", BtnRight: "RIGHT", BtnLeft: "LEFT",
	BtnSRL: "SR_L", BtnSLL: "SL_L", BtnL: "L", BtnZL: "ZL",
	BtnGR: "GR", BtnGL: "GL",
	BtnPSLTouch: "PS_L_Touch", BtnPSRTouch: "PS_R_Touch", BtnPSCClick: "PS_C_Click",
	BtnGCLClick: "GC_L_CLICK", BtnGCRClick: "GC_R_CLICK",
}

// ButtonNames returns the canonical names of the bits set in buttons, sorted.
func ButtonNames(buttons uint32) []string {
	var names []string
	for bit, name := range canonicalNames {
		if buttons&bit != 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
