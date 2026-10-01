// Package mapping owns what one player slot shows the OS: which controllers
// feed it (a single controller or a Joy-Con pair), and how their calibrated
// input becomes one Xbox 360 report and a motion sample.
//
// The rules live here together because their order matters: remaps are
// applied first and bypass hold-mode rotation; a lone Joy-Con is rotated for
// its hold mode (sticks, buttons and IMU alike); a Joy-Con pair is merged and
// never rotated; the face-button layout is applied last.
package mapping

import (
	"errors"
	"fmt"
	"math"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
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

// XboxState is a full Xbox 360 report.
type XboxState struct {
	Buttons      uint16
	LeftTrigger  uint8
	RightTrigger uint8
	LX, LY       int16
	RX, RY       int16
}

// Motion is one controller's IMU sample in DS4 axes: accelerometer in g,
// gyro in deg/s (pitch, yaw, roll).
type Motion struct {
	Accel, Gyro [3]float32
}

// Frame is what one input report produces.
type Frame struct {
	// Xbox is the player slot's full report (both Joy-Cons for a pair).
	Xbox XboxState
	// Controller is the reporting controller's own input after remap and
	// hold-mode rotation.
	Controller protocol.Input
	// Motion is the reporting controller's IMU, rotated like its sticks.
	Motion Motion
}

// RemapSettings names the target of each remappable source button:
// "Default" (or "") keeps it, "None" disables it, a Switch button name
// redirects it.
type RemapSettings struct {
	Home, Capt, C, GL, GR, SLL, SRL, SLR, SRR string
}

// Rules is the compiled layout and remap configuration shared by every
// player pad.
type Rules struct {
	byLabel bool
	remaps  []remap
}

type remap struct {
	src, dst uint32 // dst 0 = disabled
}

// NewRules compiles the settings. abxyMode is "Xbox" (positional: Switch B →
// Xbox A) or "Switch" (by label). Remap targets that aren't Switch buttons
// (the original's keyboard/mouse/gyro actions) are unsupported: they fall
// back to Default and are reported in the warnings.
func NewRules(abxyMode string, s RemapSettings) (*Rules, []string) {
	r := &Rules{byLabel: abxyMode == "Switch"}
	var warnings []string
	add := func(key string, src uint32, target string) {
		switch target {
		case "", "Default":
			return
		case "None":
			r.remaps = append(r.remaps, remap{src, 0})
			return
		}
		dst, ok := protocol.ButtonsByName[target]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: unsupported mapping %q, using Default", key, target))
			return
		}
		r.remaps = append(r.remaps, remap{src, dst})
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
	return r, warnings
}

// PlayerPad is the state of one player slot. It is not safe for concurrent
// use: callers serialize Join, Leave and Update (and whatever they do with
// the returned frame) so reports reach the virtual pad in order.
type PlayerPad struct {
	rules   *Rules
	members []*member
}

type member struct {
	id       string
	kind     protocol.Kind
	sideways bool
	last     protocol.Input // after remap, for merging
}

// NewPlayerPad returns an empty player pad.
func (r *Rules) NewPlayerPad() *PlayerPad { return &PlayerPad{rules: r} }

// ErrCannotJoin is returned when a controller can't join a player pad.
var ErrCannotJoin = errors.New("controller cannot join this player pad")

// CanJoin reports whether a controller of kind k can join: any controller
// joins an empty pad; a Joy-Con joins a lone Joy-Con of the other side.
func (p *PlayerPad) CanJoin(k protocol.Kind) bool {
	switch len(p.members) {
	case 0:
		return true
	case 1:
		other := p.members[0].kind
		return k.IsJoyCon() && other.IsJoyCon() && k != other
	}
	return false
}

// Join adds a controller. sideways is its configured hold mode; it only takes
// effect while the Joy-Con is alone.
func (p *PlayerPad) Join(id string, k protocol.Kind, sideways bool) error {
	if !p.CanJoin(k) {
		return ErrCannotJoin
	}
	p.members = append(p.members, &member{id: id, kind: k, sideways: sideways})
	return nil
}

// Leave removes a controller and reports whether the pad is now empty.
func (p *PlayerPad) Leave(id string) (empty bool) {
	for i, m := range p.members {
		if m.id == id {
			p.members = append(p.members[:i], p.members[i+1:]...)
			break
		}
	}
	return len(p.members) == 0
}

// Update applies one controller's input and returns the resulting frame.
// ok is false if id hasn't joined.
func (p *PlayerPad) Update(id string, in protocol.Input) (f Frame, ok bool) {
	var m *member
	for _, mm := range p.members {
		if mm.id == id {
			m = mm
		}
	}
	if m == nil {
		return Frame{}, false
	}

	kept, extra := p.rules.remap(in.Buttons)
	st := in
	st.Buttons = kept
	alone := len(p.members) == 1
	sideways := alone && m.sideways && m.kind.IsJoyCon()
	if alone && m.kind.IsJoyCon() {
		st = orient(m.kind, sideways, st)
	}
	// Remapped buttons bypass rotation.
	st.Buttons |= extra
	m.last = st

	out := st
	if !alone {
		var left, right protocol.Input
		for _, mm := range p.members {
			if mm.kind == protocol.KindJoyConLeft {
				left = mm.last
			} else {
				right = mm.last
			}
		}
		out = merge(left, right)
	}
	return Frame{
		Xbox:       toXbox(out, p.rules.byLabel, alone && m.kind == protocol.KindGameCube),
		Controller: st,
		Motion:     motion(in.Accel, in.Gyro, m.kind, sideways),
	}, true
}

func (r *Rules) remap(buttons uint32) (kept, extra uint32) {
	kept = buttons
	for _, rm := range r.remaps {
		if buttons&rm.src != 0 {
			kept &^= rm.src
			extra |= rm.dst
		}
	}
	return kept, extra
}

func translate(src uint32, table [][2]uint32) uint32 {
	var out uint32
	for _, e := range table {
		if src&e[0] != 0 {
			out |= e[1]
		}
	}
	return out
}

// orient adapts a lone Joy-Con. Face buttons are expressed by position
// (A=right, B=bottom, X=top, Y=left) so the layout applies uniformly later.
//
// Vertical: the Joy-Con acts as the right half of a pad (a left Joy-Con's
// D-pad becomes the face buttons and its stick the right stick).
// Sideways: the stick is rotated and becomes the left stick, and SL/SR become
// ZL/ZR. For the right Joy-Con the original's "Switch" layout table is not a
// rotation of its "Xbox" table; this port uses the rotation for both.
func orient(k protocol.Kind, sideways bool, s protocol.Input) protocol.Input {
	b := s.Buttons
	switch {
	case k == protocol.KindJoyConLeft && !sideways:
		clear := protocol.BtnUp | protocol.BtnDown | protocol.BtnLeft | protocol.BtnRight |
			protocol.BtnL | protocol.BtnZL | protocol.BtnLStick | protocol.BtnMinus
		s.Buttons = b&^clear | translate(b, [][2]uint32{
			{protocol.BtnUp, protocol.BtnX}, {protocol.BtnDown, protocol.BtnB},
			{protocol.BtnLeft, protocol.BtnY}, {protocol.BtnRight, protocol.BtnA},
			{protocol.BtnL, protocol.BtnR}, {protocol.BtnZL, protocol.BtnZR},
			{protocol.BtnMinus, protocol.BtnPlus}, {protocol.BtnLStick, protocol.BtnRStick},
		})
		s.Right, s.Left = s.Left, protocol.Stick{}
	case k == protocol.KindJoyConLeft && sideways:
		clear := protocol.BtnUp | protocol.BtnDown | protocol.BtnLeft | protocol.BtnRight |
			protocol.BtnSLL | protocol.BtnSRL | protocol.BtnL | protocol.BtnZL | protocol.BtnMinus
		s.Buttons = b&^clear | translate(b, [][2]uint32{
			{protocol.BtnUp, protocol.BtnY}, {protocol.BtnDown, protocol.BtnA},
			{protocol.BtnLeft, protocol.BtnB}, {protocol.BtnRight, protocol.BtnX},
			{protocol.BtnSLL, protocol.BtnZL}, {protocol.BtnSRL, protocol.BtnZR},
			{protocol.BtnMinus, protocol.BtnPlus},
		})
		s.Left, s.Right = protocol.Stick{X: -s.Left.Y, Y: s.Left.X}, protocol.Stick{}
	case k == protocol.KindJoyConRight && sideways:
		clear := protocol.BtnX | protocol.BtnY | protocol.BtnA | protocol.BtnB |
			protocol.BtnSLR | protocol.BtnSRR | protocol.BtnR | protocol.BtnZR |
			protocol.BtnPlus | protocol.BtnRStick
		s.Buttons = b&^clear | translate(b, [][2]uint32{
			{protocol.BtnA, protocol.BtnB}, {protocol.BtnX, protocol.BtnA},
			{protocol.BtnB, protocol.BtnY}, {protocol.BtnY, protocol.BtnX},
			{protocol.BtnSLR, protocol.BtnZL}, {protocol.BtnSRR, protocol.BtnZR},
			{protocol.BtnPlus, protocol.BtnPlus}, {protocol.BtnRStick, protocol.BtnLStick},
		})
		s.Left, s.Right = protocol.Stick{X: s.Right.Y, Y: -s.Right.X}, protocol.Stick{}
	}
	// Right Joy-Con held vertically: unchanged.
	return s
}

func merge(left, right protocol.Input) protocol.Input {
	return protocol.Input{Buttons: left.Buttons | right.Buttons, Left: left.Left, Right: right.Right}
}

var commonButtons = [][2]uint32{
	{protocol.BtnL, uint32(XBLB)}, {protocol.BtnR, uint32(XBRB)},
	{protocol.BtnMinus, uint32(XBBack)}, {protocol.BtnPlus, uint32(XBStart)},
	{protocol.BtnLStick, uint32(XBLStick)}, {protocol.BtnRStick, uint32(XBRStick)},
	{protocol.BtnUp, uint32(XBUp)}, {protocol.BtnDown, uint32(XBDown)},
	{protocol.BtnLeft, uint32(XBLeft)}, {protocol.BtnRight, uint32(XBRight)},
	{protocol.BtnHome, uint32(XBGuide)}, {protocol.BtnCapture, uint32(XBBack)},
}

func faceTable(byLabel, gameCube bool) [][2]uint32 {
	switch {
	case byLabel:
		return [][2]uint32{
			{protocol.BtnA, uint32(XBA)}, {protocol.BtnB, uint32(XBB)},
			{protocol.BtnX, uint32(XBX)}, {protocol.BtnY, uint32(XBY)},
		}
	case gameCube:
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

func toXbox(s protocol.Input, byLabel, gameCube bool) XboxState {
	var out XboxState
	out.Buttons = uint16(translate(s.Buttons, faceTable(byLabel, gameCube)) | translate(s.Buttons, commonButtons))
	if s.AnalogTriggers {
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

// motion converts raw IMU samples to DS4 axes following the original: invert
// to a common frame, rotate for a sideways Joy-Con, reorder to pitch/yaw/roll.
func motion(accel, gyro [3]int16, k protocol.Kind, sideways bool) Motion {
	gx, gy, gz := float64(gyro[0]), -float64(gyro[1]), -float64(gyro[2])
	ax, ay, az := -float64(accel[0]), -float64(accel[1]), -float64(accel[2])
	if sideways {
		if k == protocol.KindJoyConRight {
			gx, gy = -gy, gx
			ax, ay = -ay, ax
		} else {
			gx, gy = gy, -gx
			ax, ay = ay, -ax
		}
	}
	// Empirically tuned deg/s-per-LSB multipliers from the original (a full
	// physical turn reads as 360° in emulators).
	gyroScale := 0.0535
	if k.ProLike() {
		gyroScale = 0.061
	}
	g := protocol.AccelLSBPerG
	return Motion{
		Accel: [3]float32{float32(ax / g), float32(az / g), float32(-ay / g)},
		Gyro:  [3]float32{float32(gx * gyroScale), float32(gz * gyroScale), float32(-gy * gyroScale)},
	}
}
