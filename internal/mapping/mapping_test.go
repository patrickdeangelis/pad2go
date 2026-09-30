package mapping

import (
	"testing"

	p "github.com/angelispatrick/switch2connect-go/internal/protocol"
)

func TestToXboxPositionalAndLabel(t *testing.T) {
	s := State{Buttons: p.BtnB | p.BtnX}
	if got := ToXbox(s, Positional, false).Buttons; got != XBA|XBY {
		t.Fatalf("positional: %04x", got)
	}
	if got := ToXbox(s, ByLabel, false).Buttons; got != XBB|XBX {
		t.Fatalf("by label: %04x", got)
	}
	// GameCube positional: B → X, X → B.
	if got := ToXbox(s, Positional, true).Buttons; got != XBX|XBB {
		t.Fatalf("gamecube: %04x", got)
	}
	if got := ToXbox(State{Buttons: p.BtnA}, Positional, true).Buttons; got != XBA {
		t.Fatalf("gamecube A: %04x", got)
	}
}

func TestToXboxCommonButtonsTriggersSticks(t *testing.T) {
	s := State{
		Buttons: p.BtnL | p.BtnR | p.BtnZL | p.BtnMinus | p.BtnPlus | p.BtnHome | p.BtnUp | p.BtnLStick,
		Left:    Stick{1, -1},
		Right:   Stick{0.5, 2},
	}
	x := ToXbox(s, Positional, false)
	want := XBLB | XBRB | XBBack | XBStart | XBGuide | XBUp | XBLStick
	if x.Buttons != want {
		t.Fatalf("buttons %04x want %04x", x.Buttons, want)
	}
	if x.LeftTrigger != 255 || x.RightTrigger != 0 {
		t.Fatalf("triggers %d/%d", x.LeftTrigger, x.RightTrigger)
	}
	if x.LX != 32767 || x.LY != -32767 || x.RX != 16384 || x.RY != 32767 {
		t.Fatalf("sticks %+v", x)
	}
	a := ToXbox(State{HasAnalogTriggers: true, LeftTrigger: 100, Buttons: p.BtnZL}, Positional, true)
	if a.LeftTrigger != 100 {
		t.Fatalf("analog trigger %d", a.LeftTrigger)
	}
}

func TestOrientLeftVertical(t *testing.T) {
	in := State{Buttons: p.BtnUp | p.BtnRight | p.BtnZL | p.BtnMinus | p.BtnLStick | p.BtnCapture, Left: Stick{0.3, 0.4}}
	out := OrientSingleJoyCon(LeftJoyCon, false, in)
	want := p.BtnX | p.BtnA | p.BtnZR | p.BtnPlus | p.BtnRStick | p.BtnCapture
	if out.Buttons != want {
		t.Fatalf("buttons %v want %v", p.ButtonNames(out.Buttons), p.ButtonNames(want))
	}
	if out.Right != (Stick{0.3, 0.4}) || out.Left != (Stick{}) {
		t.Fatalf("sticks %+v", out)
	}
}

func TestOrientLeftHorizontal(t *testing.T) {
	in := State{Buttons: p.BtnUp | p.BtnSLL | p.BtnSRL | p.BtnL, Left: Stick{0, 1}}
	out := OrientSingleJoyCon(LeftJoyCon, true, in)
	if want := p.BtnY | p.BtnZL | p.BtnZR; out.Buttons != want {
		t.Fatalf("buttons %v", p.ButtonNames(out.Buttons))
	}
	// Pushing the stick "up" on the Joy-Con points left when held sideways.
	if out.Left != (Stick{-1, 0}) {
		t.Fatalf("stick %+v", out.Left)
	}
}

func TestOrientRightHorizontal(t *testing.T) {
	in := State{Buttons: p.BtnX | p.BtnSRR | p.BtnRStick, Right: Stick{0, 1}}
	out := OrientSingleJoyCon(RightJoyCon, true, in)
	if want := p.BtnA | p.BtnZR | p.BtnLStick; out.Buttons != want {
		t.Fatalf("buttons %v", p.ButtonNames(out.Buttons))
	}
	if out.Left != (Stick{1, 0}) || out.Right != (Stick{}) {
		t.Fatalf("sticks %+v", out)
	}
}

func TestOrientRightVerticalUnchanged(t *testing.T) {
	in := State{Buttons: p.BtnA | p.BtnR, Right: Stick{0.1, 0.2}}
	if out := OrientSingleJoyCon(RightJoyCon, false, in); out != in {
		t.Fatalf("got %+v", out)
	}
}

func TestMerge(t *testing.T) {
	l := State{Buttons: p.BtnL | p.BtnUp, Left: Stick{1, 0}, Right: Stick{9, 9}}
	r := State{Buttons: p.BtnA, Right: Stick{0, 1}, Left: Stick{9, 9}}
	m := Merge(l, r)
	if m.Buttons != p.BtnL|p.BtnUp|p.BtnA || m.Left != (Stick{1, 0}) || m.Right != (Stick{0, 1}) {
		t.Fatalf("%+v", m)
	}
}

func TestRemapper(t *testing.T) {
	r, warnings := NewRemapper(RemapSettings{
		Home: "None", Capt: "Custom[Hold]:VK_F11", GL: "A", GR: "Default", SRL: "ZR",
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings %v", warnings)
	}
	kept, extra := r.Apply(p.BtnHome | p.BtnCapture | p.BtnGL | p.BtnGR | p.BtnB)
	if kept != p.BtnCapture|p.BtnGR|p.BtnB || extra != p.BtnA {
		t.Fatalf("kept %v extra %v", p.ButtonNames(kept), p.ButtonNames(extra))
	}
}

func TestParseLayout(t *testing.T) {
	if ParseLayout("Switch") != ByLabel || ParseLayout("Xbox") != Positional || ParseLayout("") != Positional {
		t.Fatal("layout parsing")
	}
}
