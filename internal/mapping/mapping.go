// Package mapping turns Switch controller state into an Xbox 360 pad state:
// extra-button remaps, single Joy-Con orientation, Joy-Con merging and the
// Switch→Xbox button translation.
package mapping

import (
	"fmt"
	"math"

	"github.com/angelispatrick/switch2connect-go/internal/protocol"
)

// Xbox 360 (XUSB) button bits.
const (
	XBUp     uint16 = 0x0001
	XBDown   uint16 = 0x0002
	XBLeft   uint16 = 0x0004
	XBRight  uint16 = 0x0008
	XBStart  uint16 = 0x0010
	XBBack   uint16 = 0x0020
	XBLStick uint16 = 0x0040
	XBRStick uint16 = 0x0080
	XBLB     uint16 = 0x0100
	XBRB     uint16 = 0x0200
	XBGuide  uint16 = 0x0400
	XBA      uint16 = 0x1000
	XBB      uint16 = 0x2000
	XBX      uint16 = 0x4000
	XBY      uint16 = 0x8000
)

// Stick is a normalized stick position; +Y is up.
type Stick struct{ X, Y float64 }

// State is the per-controller input after calibration.
type State struct {
	Buttons           uint32
	Left, Right       Stick
	LeftTrigger       uint8 // analog (GameCube); 0 means use ZL digital
	RightTrigger      uint8
	HasAnalogTriggers bool
}

// XboxState is a full Xbox 360 report.
type XboxState struct {
	Buttons      uint16
	LeftTrigger  uint8
	RightTrigger uint8
	LX, LY       int16
	RX, RY       int16
}

// Remapper applies the home/capt/c/gl/gr/sl/sr remap settings.
type Remapper struct {
	rules []rule
}

type rule struct {
	src, dst uint32 // dst 0 = drop
}

// RemapSettings names the target of each remappable source button.
type RemapSettings struct {
	Home, Capt, C, GL, GR, SLL, SRL, SLR, SRR string
}

// NewRemapper builds a remapper. "Default" and "" keep the button as-is;
// "None" disables it; a Switch button name redirects it. Other values (the
// original's keyboard/mouse/gyro actions) are not supported and are reported
// in the returned warnings.
func NewRemapper(s RemapSettings) (*Remapper, []string) {
	var r Remapper
	var warnings []string
	add := func(key string, src uint32, target string) {
		switch target {
		case "", "Default":
			return
		case "None":
			r.rules = append(r.rules, rule{src, 0})
			return
		}
		dst, ok := protocol.ButtonsByName[target]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: unsupported mapping %q, using Default", key, target))
			return
		}
		r.rules = append(r.rules, rule{src, dst})
	}
	add("home_mapping", protocol.BtnHome, s.Home)
	add("capt_mapping", protocol.BtnCapture, s.Capt)
	add("c_mapping", protocol.BtnC, s.C)
	add("gl_mapping", protocol.BtnGL, s.GL)
	add("gr_mapping", protocol.BtnGR, s.GR)
	add("sll_mapping", protocol.BtnSLL, s.SLL)
	add("srl_mapping", protocol.BtnSRL, s.SRL)
	add("slr_mapping", protocol.BtnSLR, s.SLR)
	add("srr_mapping", protocol.BtnSRR, s.SRR)
	return &r, warnings
}

// Apply returns the buttons with remapped sources removed, and the remapped
// targets separately. Remapped targets bypass Joy-Con orientation, as in the
// original.
func (r *Remapper) Apply(buttons uint32) (kept, extra uint32) {
	kept = buttons
	for _, rl := range r.rules {
		if buttons&rl.src != 0 {
			kept &^= rl.src
			extra |= rl.dst
		}
	}
	return kept, extra
}

// Side identifies a single Joy-Con.
type Side int

const (
	LeftJoyCon Side = iota
	RightJoyCon
)

func remap(src uint32, table [][2]uint32) uint32 {
	var out uint32
	for _, e := range table {
		if src&e[0] != 0 {
			out |= e[1]
		}
	}
	return out
}

// OrientSingleJoyCon adapts a lone Joy-Con. Face buttons are expressed by
// position (A=right, B=bottom, X=top, Y=left) so the ABXY layout choice is
// applied uniformly later.
//
// Vertical: the Joy-Con acts as the right half of a pad (a left Joy-Con's
// D-pad becomes the face buttons and its stick the right stick).
// Horizontal: held sideways; the stick is rotated and becomes the left stick,
// and SL/SR become ZL/ZR.
func OrientSingleJoyCon(side Side, horizontal bool, s State) State {
	b := s.Buttons
	switch {
	case side == LeftJoyCon && !horizontal:
		clear := protocol.BtnUp | protocol.BtnDown | protocol.BtnLeft | protocol.BtnRight |
			protocol.BtnL | protocol.BtnZL | protocol.BtnLStick | protocol.BtnMinus
		s.Buttons = b&^clear | remap(b, [][2]uint32{
			{protocol.BtnUp, protocol.BtnX}, {protocol.BtnDown, protocol.BtnB},
			{protocol.BtnLeft, protocol.BtnY}, {protocol.BtnRight, protocol.BtnA},
			{protocol.BtnL, protocol.BtnR}, {protocol.BtnZL, protocol.BtnZR},
			{protocol.BtnMinus, protocol.BtnPlus}, {protocol.BtnLStick, protocol.BtnRStick},
		})
		s.Right, s.Left = s.Left, Stick{}
	case side == LeftJoyCon && horizontal:
		clear := protocol.BtnUp | protocol.BtnDown | protocol.BtnLeft | protocol.BtnRight |
			protocol.BtnSLL | protocol.BtnSRL | protocol.BtnL | protocol.BtnZL | protocol.BtnMinus
		s.Buttons = b&^clear | remap(b, [][2]uint32{
			{protocol.BtnUp, protocol.BtnY}, {protocol.BtnDown, protocol.BtnA},
			{protocol.BtnLeft, protocol.BtnB}, {protocol.BtnRight, protocol.BtnX},
			{protocol.BtnSLL, protocol.BtnZL}, {protocol.BtnSRL, protocol.BtnZR},
			{protocol.BtnMinus, protocol.BtnPlus},
		})
		s.Left, s.Right = Stick{-s.Left.Y, s.Left.X}, Stick{}
	case side == RightJoyCon && horizontal:
		// The original's "Switch" layout table for this case is not a rotation
		// of its "Xbox" table; this port uses the rotation for both.
		clear := protocol.BtnX | protocol.BtnY | protocol.BtnA | protocol.BtnB |
			protocol.BtnSLR | protocol.BtnSRR | protocol.BtnR | protocol.BtnZR |
			protocol.BtnPlus | protocol.BtnRStick
		s.Buttons = b&^clear | remap(b, [][2]uint32{
			{protocol.BtnA, protocol.BtnB}, {protocol.BtnX, protocol.BtnA},
			{protocol.BtnB, protocol.BtnY}, {protocol.BtnY, protocol.BtnX},
			{protocol.BtnSLR, protocol.BtnZL}, {protocol.BtnSRR, protocol.BtnZR},
			{protocol.BtnPlus, protocol.BtnPlus}, {protocol.BtnRStick, protocol.BtnLStick},
		})
		s.Left, s.Right = Stick{s.Right.Y, -s.Right.X}, Stick{}
	}
	// Right Joy-Con, vertical: unchanged.
	return s
}

// Merge combines a left and right Joy-Con into one state.
func Merge(left, right State) State {
	return State{
		Buttons: left.Buttons | right.Buttons,
		Left:    left.Left,
		Right:   right.Right,
	}
}

// Layout chooses how Switch face buttons map to Xbox face buttons.
type Layout int

const (
	// Positional keeps physical positions (Switch B, bottom → Xbox A).
	Positional Layout = iota
	// ByLabel matches printed letters (Switch A → Xbox A).
	ByLabel
)

// ParseLayout maps abxy_mode ("Xbox" → Positional, "Switch" → ByLabel).
func ParseLayout(abxyMode string) Layout {
	if abxyMode == "Switch" {
		return ByLabel
	}
	return Positional
}

var commonButtons = [][2]uint32{
	{protocol.BtnL, uint32(XBLB)}, {protocol.BtnR, uint32(XBRB)},
	{protocol.BtnMinus, uint32(XBBack)}, {protocol.BtnPlus, uint32(XBStart)},
	{protocol.BtnLStick, uint32(XBLStick)}, {protocol.BtnRStick, uint32(XBRStick)},
	{protocol.BtnUp, uint32(XBUp)}, {protocol.BtnDown, uint32(XBDown)},
	{protocol.BtnLeft, uint32(XBLeft)}, {protocol.BtnRight, uint32(XBRight)},
	{protocol.BtnHome, uint32(XBGuide)}, {protocol.BtnCapture, uint32(XBBack)},
}

func faceTable(layout Layout, gameCube bool) [][2]uint32 {
	if layout == ByLabel {
		return [][2]uint32{
			{protocol.BtnA, uint32(XBA)}, {protocol.BtnB, uint32(XBB)},
			{protocol.BtnX, uint32(XBX)}, {protocol.BtnY, uint32(XBY)},
		}
	}
	if gameCube {
		// GameCube: big A stays A; B (lower-left) → X, X (right) → B, Y (top) → Y.
		return [][2]uint32{
			{protocol.BtnA, uint32(XBA)}, {protocol.BtnB, uint32(XBX)},
			{protocol.BtnX, uint32(XBB)}, {protocol.BtnY, uint32(XBY)},
		}
	}
	return [][2]uint32{
		{protocol.BtnA, uint32(XBB)}, {protocol.BtnB, uint32(XBA)},
		{protocol.BtnX, uint32(XBY)}, {protocol.BtnY, uint32(XBX)},
	}
}

// ToXbox converts a (merged/oriented) state to an Xbox 360 report.
func ToXbox(s State, layout Layout, gameCube bool) XboxState {
	var out XboxState
	out.Buttons = uint16(remap(s.Buttons, faceTable(layout, gameCube)) | remap(s.Buttons, commonButtons))
	if s.HasAnalogTriggers {
		out.LeftTrigger, out.RightTrigger = s.LeftTrigger, s.RightTrigger
	} else {
		if s.Buttons&protocol.BtnZL != 0 {
			out.LeftTrigger = 255
		}
		if s.Buttons&protocol.BtnZR != 0 {
			out.RightTrigger = 255
		}
	}
	out.LX, out.LY = axisToInt16(s.Left.X), axisToInt16(s.Left.Y)
	out.RX, out.RY = axisToInt16(s.Right.X), axisToInt16(s.Right.Y)
	return out
}

func axisToInt16(v float64) int16 {
	v = math.Max(-1, math.Min(1, v))
	return int16(math.Round(v * 32767))
}
