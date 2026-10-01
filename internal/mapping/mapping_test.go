package mapping

import (
	"math"
	"testing"

	p "github.com/patrickdeangelis/pad2go/internal/protocol"
)

const (
	vertical = false
	sideways = true
)

// single returns a pad with one controller joined as "c".
func single(t *testing.T, abxy string, k p.Kind, hold bool, remaps RemapSettings) *PlayerPad {
	t.Helper()
	rules, _ := NewRules(abxy, remaps)
	pad := rules.NewPlayerPad()
	if err := pad.Join("c", k, hold); err != nil {
		t.Fatal(err)
	}
	return pad
}

func update(t *testing.T, pad *PlayerPad, id string, in p.Input) Frame {
	t.Helper()
	f, ok := pad.Update(id, in)
	if !ok {
		t.Fatalf("update %q: not joined", id)
	}
	return f
}

func TestLayouts(t *testing.T) {
	in := p.Input{Buttons: p.BtnB | p.BtnX}
	if got := update(t, single(t, "Xbox", p.KindPro, vertical, RemapSettings{}), "c", in).Xbox.Buttons; got != XBA|XBY {
		t.Fatalf("positional: %04x", got)
	}
	if got := update(t, single(t, "Switch", p.KindPro, vertical, RemapSettings{}), "c", in).Xbox.Buttons; got != XBB|XBX {
		t.Fatalf("by label: %04x", got)
	}
}

func TestProButtonsTriggersSticks(t *testing.T) {
	pad := single(t, "Xbox", p.KindPro, vertical, RemapSettings{})
	x := update(t, pad, "c", p.Input{
		Buttons: p.BtnL | p.BtnR | p.BtnZL | p.BtnMinus | p.BtnPlus | p.BtnHome | p.BtnUp | p.BtnLStick,
		Left:    p.Stick{X: 1, Y: -1},
		Right:   p.Stick{X: 0.5, Y: 2},
	}).Xbox
	want := XBLB | XBRB | XBBack | XBStart | XBGuide | XBUp | XBLStick
	if x.Buttons != want || x.LeftTrigger != 255 || x.RightTrigger != 0 {
		t.Fatalf("buttons %04x want %04x, triggers %d/%d", x.Buttons, want, x.LeftTrigger, x.RightTrigger)
	}
	if x.LX != 32767 || x.LY != -32767 || x.RX != 16384 || x.RY != 32767 {
		t.Fatalf("sticks %+v", x)
	}
}

func TestGameCube(t *testing.T) {
	pad := single(t, "Xbox", p.KindGameCube, vertical, RemapSettings{})
	// Positional GameCube layout: B → X, X → B, A and Y stay.
	x := update(t, pad, "c", p.Input{Buttons: p.BtnA | p.BtnB | p.BtnX, AnalogTriggers: true, LeftTrigger: 100, RightTrigger: 7}).Xbox
	if x.Buttons != XBA|XBX|XBB || x.LeftTrigger != 100 || x.RightTrigger != 7 {
		t.Fatalf("%+v", x)
	}
	if b := update(t, pad, "c", p.Input{Buttons: p.BtnB}).Xbox.Buttons; b != XBX {
		t.Fatalf("B → %04x", b)
	}
}

func TestLeftJoyConVertical(t *testing.T) {
	pad := single(t, "Xbox", p.KindJoyConLeft, vertical, RemapSettings{})
	f := update(t, pad, "c", p.Input{
		Buttons: p.BtnUp | p.BtnRight | p.BtnZL | p.BtnMinus | p.BtnLStick | p.BtnCapture,
		Left:    p.Stick{X: 0.3, Y: 0.4},
	})
	want := p.BtnX | p.BtnA | p.BtnZR | p.BtnPlus | p.BtnRStick | p.BtnCapture
	if f.Controller.Buttons != want {
		t.Fatalf("buttons %v want %v", p.ButtonNames(f.Controller.Buttons), p.ButtonNames(want))
	}
	if f.Xbox.Buttons != XBY|XBB|XBStart|XBRStick|XBBack || f.Xbox.RightTrigger != 255 {
		t.Fatalf("xbox %+v", f.Xbox)
	}
	if f.Controller.Right != (p.Stick{X: 0.3, Y: 0.4}) || f.Controller.Left != (p.Stick{}) {
		t.Fatalf("sticks %+v", f.Controller)
	}
}

func TestLeftJoyConSideways(t *testing.T) {
	pad := single(t, "Xbox", p.KindJoyConLeft, sideways, RemapSettings{})
	f := update(t, pad, "c", p.Input{Buttons: p.BtnUp | p.BtnSLL | p.BtnSRL | p.BtnL, Left: p.Stick{Y: 1}})
	if want := p.BtnY | p.BtnZL | p.BtnZR; f.Controller.Buttons != want {
		t.Fatalf("buttons %v", p.ButtonNames(f.Controller.Buttons))
	}
	// Pushing the stick "up" on the Joy-Con points left when held sideways.
	if f.Controller.Left != (p.Stick{X: -1}) {
		t.Fatalf("stick %+v", f.Controller.Left)
	}
}

func TestRightJoyConSideways(t *testing.T) {
	pad := single(t, "Xbox", p.KindJoyConRight, sideways, RemapSettings{})
	f := update(t, pad, "c", p.Input{Buttons: p.BtnX | p.BtnSRR | p.BtnRStick, Right: p.Stick{Y: 1}})
	if want := p.BtnA | p.BtnZR | p.BtnLStick; f.Controller.Buttons != want {
		t.Fatalf("buttons %v", p.ButtonNames(f.Controller.Buttons))
	}
	if f.Xbox.Buttons != XBB|XBLStick || f.Xbox.LX != 32767 || f.Xbox.RX != 0 {
		t.Fatalf("xbox %+v", f.Xbox)
	}
}

func TestRightJoyConVerticalUnchanged(t *testing.T) {
	in := p.Input{Buttons: p.BtnA | p.BtnR, Right: p.Stick{X: 0.1, Y: 0.2}}
	if f := update(t, single(t, "Xbox", p.KindJoyConRight, vertical, RemapSettings{}), "c", in); f.Controller != in {
		t.Fatalf("got %+v", f.Controller)
	}
}

func TestRemapBypassesRotation(t *testing.T) {
	pad := single(t, "Xbox", p.KindJoyConRight, sideways, RemapSettings{Capt: "A"})
	// Capture → A is applied after rotation: A (right position) → Xbox B.
	if b := update(t, pad, "c", p.Input{Buttons: p.BtnCapture}).Xbox.Buttons; b != XBB {
		t.Fatalf("remapped capture → %04x", b)
	}
	// The physical A is rotated to the bottom position → Xbox A.
	if b := update(t, pad, "c", p.Input{Buttons: p.BtnA}).Xbox.Buttons; b != XBA {
		t.Fatalf("physical A → %04x", b)
	}
}

func TestRemapNoneAndWarnings(t *testing.T) {
	rules, warnings := NewRules("Xbox", RemapSettings{Home: "None", Capt: "Custom[Hold]:VK_F11", GR: "Default"})
	if len(warnings) != 1 {
		t.Fatalf("warnings %v", warnings)
	}
	pad := rules.NewPlayerPad()
	_ = pad.Join("c", p.KindPro, vertical)
	if b := update(t, pad, "c", p.Input{Buttons: p.BtnHome | p.BtnCapture}).Xbox.Buttons; b != XBBack {
		t.Fatalf("home disabled, capture kept: %04x", b)
	}
}

func pair(t *testing.T) *PlayerPad {
	t.Helper()
	rules, _ := NewRules("Xbox", RemapSettings{})
	pad := rules.NewPlayerPad()
	// Both configured sideways: that must not apply while they're paired.
	if err := pad.Join("L", p.KindJoyConLeft, sideways); err != nil {
		t.Fatal(err)
	}
	if err := pad.Join("R", p.KindJoyConRight, sideways); err != nil {
		t.Fatal(err)
	}
	return pad
}

func TestPairMergesWithoutRotation(t *testing.T) {
	pad := pair(t)
	update(t, pad, "L", p.Input{Buttons: p.BtnUp | p.BtnL, Left: p.Stick{Y: 1}})
	x := update(t, pad, "R", p.Input{Buttons: p.BtnA, Right: p.Stick{Y: 1}}).Xbox
	if x.Buttons != XBUp|XBLB|XBB || x.LY != 32767 || x.RY != 32767 {
		t.Fatalf("merged %+v", x)
	}
}

func TestPairMotionIsNotRotated(t *testing.T) {
	raw := p.Input{Gyro: [3]int16{100, 0, 0}}
	paired := update(t, pair(t), "R", raw).Motion
	upright := update(t, single(t, "Xbox", p.KindJoyConRight, vertical, RemapSettings{}), "c", raw).Motion
	if paired != upright {
		t.Fatalf("paired %+v, upright %+v", paired, upright)
	}
	rotated := update(t, single(t, "Xbox", p.KindJoyConRight, sideways, RemapSettings{}), "c", raw).Motion
	if rotated == upright {
		t.Fatal("a lone sideways Joy-Con's motion should rotate")
	}
}

func TestLeaveRestoresSingleOrientation(t *testing.T) {
	pad := pair(t)
	if empty := pad.Leave("L"); empty {
		t.Fatal("pad should still hold the right Joy-Con")
	}
	f := update(t, pad, "R", p.Input{Right: p.Stick{Y: 1}})
	if f.Xbox.LX != 32767 {
		t.Fatalf("lone sideways right Joy-Con should drive the rotated left stick: %+v", f.Xbox)
	}
	if !pad.Leave("R") {
		t.Fatal("pad should be empty")
	}
}

func TestJoinRules(t *testing.T) {
	rules, _ := NewRules("Xbox", RemapSettings{})
	pro := rules.NewPlayerPad()
	_ = pro.Join("P", p.KindPro, vertical)
	if pro.CanJoin(p.KindJoyConLeft) || pro.Join("L", p.KindJoyConLeft, vertical) != ErrCannotJoin {
		t.Fatal("a Pro Controller pad takes no one else")
	}
	jc := rules.NewPlayerPad()
	_ = jc.Join("L", p.KindJoyConLeft, vertical)
	if jc.CanJoin(p.KindJoyConLeft) || !jc.CanJoin(p.KindJoyConRight) || jc.CanJoin(p.KindPro) {
		t.Fatal("a lone left Joy-Con only pairs with a right one")
	}
	_ = jc.Join("R", p.KindJoyConRight, vertical)
	if jc.CanJoin(p.KindJoyConRight) {
		t.Fatal("a pair is full")
	}
	if _, ok := jc.Update("nobody", p.Input{}); ok {
		t.Fatal("unknown controller should be rejected")
	}
}

func TestMotion(t *testing.T) {
	pro := single(t, "Xbox", p.KindPro, vertical, RemapSettings{})
	// Lying flat, the raw Z axis reads -1 g; DS4 axes put gravity on Y.
	if m := update(t, pro, "c", p.Input{Accel: [3]int16{0, 0, -4096}}).Motion; m.Accel != [3]float32{0, 1, 0} {
		t.Fatalf("accel %v", m.Accel)
	}
	g := update(t, pro, "c", p.Input{Gyro: [3]int16{0, 0, -1000}}).Motion.Gyro
	if math.Abs(float64(g[1])-61) > 1e-3 {
		t.Fatalf("pro yaw %v", g)
	}
	side := single(t, "Xbox", p.KindJoyConRight, sideways, RemapSettings{})
	g = update(t, side, "c", p.Input{Gyro: [3]int16{100, 0, 0}}).Motion.Gyro
	if g[0] != 0 || math.Abs(float64(g[2])+5.35) > 1e-3 {
		t.Fatalf("sideways right Joy-Con gyro %v", g)
	}
}
