package mapping

import (
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	p "github.com/patrickdeangelis/pad2go/internal/protocol"
)

// dsReport builds a 64-byte DualSense USB input report with sticks centred and
// the hat released, then applies edit.
func dsReport(edit func(r []byte)) []byte {
	r := make([]byte, 64)
	r[0] = 0x01
	r[1], r[2], r[3], r[4] = 128, 128, 128, 128
	r[8] = 0x08
	r[53] = 0x08
	if edit != nil {
		edit(r)
	}
	return r
}

// dsPad returns a player pad with one DualSense joined as "ds".
func dsPad(t *testing.T, abxy string, remaps RemapSettings) *PlayerPad {
	t.Helper()
	rules, _ := NewRules(abxy, remaps)
	pad := rules.NewPlayerPad()
	if err := pad.Join("ds", p.KindDualSense, false); err != nil {
		t.Fatal(err)
	}
	return pad
}

// dsXbox parses one raw DualSense report and returns the resulting Xbox state.
func dsXbox(t *testing.T, pad *PlayerPad, report []byte) XboxState {
	t.Helper()
	in, ok := dualsense.Parse(report)
	if !ok {
		t.Fatalf("Parse rejected a %d-byte report", len(report))
	}
	f, ok := pad.Update("ds", in)
	if !ok {
		t.Fatal("DualSense not joined")
	}
	return f.Xbox
}

func TestDualSenseXboxButtons(t *testing.T) {
	cases := []struct {
		name string
		edit func(r []byte)
		want uint16
	}{
		{"Cross", func(r []byte) { r[8] |= 0x20 }, 0x1000},
		{"Circle", func(r []byte) { r[8] |= 0x40 }, 0x2000},
		{"Square", func(r []byte) { r[8] |= 0x10 }, 0x4000},
		{"Triangle", func(r []byte) { r[8] |= 0x80 }, 0x8000},
		{"L1", func(r []byte) { r[9] |= 0x01 }, 0x0100},
		{"R1", func(r []byte) { r[9] |= 0x02 }, 0x0200},
		{"L3", func(r []byte) { r[9] |= 0x40 }, 0x0040},
		{"R3", func(r []byte) { r[9] |= 0x80 }, 0x0080},
		{"Options", func(r []byte) { r[9] |= 0x20 }, 0x0010},
		{"Create", func(r []byte) { r[9] |= 0x10 }, 0x0020},
		{"PS", func(r []byte) { r[10] |= 0x01 }, 0x0400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pad := dsPad(t, "Xbox", RemapSettings{})
			if got := dsXbox(t, pad, dsReport(c.edit)).Buttons; got != c.want {
				t.Errorf("%s: buttons %#04x, want %#04x", c.name, got, c.want)
			}
		})
	}
}

func TestDualSenseIndependentRemaps(t *testing.T) {
	rules, _ := NewRules("Switch", RemapSettings{Home: "None"})
	if warnings := rules.SetDualSenseRemaps(map[string]string{"cross": "A", "circle": "B", "ps": "None"}); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	pad := rules.NewPlayerPad()
	if err := pad.Join("ds", p.KindDualSense, false); err != nil {
		t.Fatal(err)
	}
	got := dsXbox(t, pad, dsReport(func(r []byte) { r[8] |= 0x20; r[10] |= 1 }))
	if got.Buttons != XBB {
		t.Fatalf("cross + PS = %#x, want Xbox B only", got.Buttons)
	}
	got = dsXbox(t, pad, dsReport(func(r []byte) { r[8] |= 0x60 }))
	if got.Buttons != XBA|XBB {
		t.Fatalf("simultaneous swapped buttons = %#x", got.Buttons)
	}
}

func TestDualSenseTouchClickRemapping(t *testing.T) {
	rules, _ := NewRules("Xbox", RemapSettings{})
	if warnings := rules.SetDualSenseRemaps(map[string]string{"touch": "B"}); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	pad := rules.NewPlayerPad()
	if err := pad.Join("ds", p.KindDualSense, false); err != nil {
		t.Fatal(err)
	}
	got := dsXbox(t, pad, dsReport(func(r []byte) { r[10] |= 2 }))
	if got.Buttons != XBA {
		t.Fatalf("touch click output = %#x", got.Buttons)
	}
}

func TestDualSenseTriggerRemaps(t *testing.T) {
	for _, c := range []struct {
		name                string
		remaps              map[string]string
		left, right         byte
		pressCross          bool
		wantLeft, wantRight byte
		wantButtons         uint16
	}{
		{"default analog", nil, 80, 200, false, 80, 200, 0},
		{"swap analog", map[string]string{"l2": "ZR", "r2": "ZL"}, 80, 200, false, 200, 80, 0},
		{"trigger to button below threshold", map[string]string{"l2": "B"}, 127, 0, false, 0, 0, 0},
		{"trigger to button pressed", map[string]string{"l2": "B"}, 128, 0, false, 0, 0, XBA},
		{"button to trigger", map[string]string{"cross": "ZL"}, 20, 0, true, 255, 0, 0},
		{"trigger disabled", map[string]string{"r2": "None"}, 40, 200, false, 40, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			rules, _ := NewRules("Switch", RemapSettings{})
			if warnings := rules.SetDualSenseRemaps(c.remaps); len(warnings) != 0 {
				t.Fatal(warnings)
			}
			pad := rules.NewPlayerPad()
			if err := pad.Join("ds", p.KindDualSense, false); err != nil {
				t.Fatal(err)
			}
			x := dsXbox(t, pad, dsReport(func(r []byte) {
				r[5], r[6] = c.left, c.right
				if c.pressCross {
					r[8] |= 0x20
				}
			}))
			if x.LeftTrigger != c.wantLeft || x.RightTrigger != c.wantRight || x.Buttons != c.wantButtons {
				t.Fatalf("got %+v", x)
			}
		})
	}
}

func TestDualSenseXboxHat(t *testing.T) {
	want := []uint16{0x0001, 0x0009, 0x0008, 0x000A, 0x0002, 0x0006, 0x0004, 0x0005, 0x0000}
	pad := dsPad(t, "Xbox", RemapSettings{})
	for hat := range 9 {
		got := dsXbox(t, pad, dsReport(func(r []byte) { r[8] = byte(hat) })).Buttons
		if got != want[hat] {
			t.Errorf("hat %d: buttons %#04x, want %#04x", hat, got, want[hat])
		}
	}
}

func TestDualSenseXboxABXYModeIgnored(t *testing.T) {
	pad := dsPad(t, "Switch", RemapSettings{})
	cross := dsXbox(t, pad, dsReport(func(r []byte) { r[8] |= 0x20 })).Buttons
	if cross != 0x1000 {
		t.Errorf("DualSense Cross under abxy_mode Switch: %#04x, want 0x1000", cross)
	}
	circle := dsXbox(t, pad, dsReport(func(r []byte) { r[8] |= 0x40 })).Buttons
	if circle != 0x2000 {
		t.Errorf("DualSense Circle under abxy_mode Switch: %#04x, want 0x2000", circle)
	}

	// A Pro Controller under the same rules is unchanged: B is label-mapped to XBB.
	rules, _ := NewRules("Switch", RemapSettings{})
	pro := rules.NewPlayerPad()
	if err := pro.Join("pro", p.KindPro, false); err != nil {
		t.Fatal(err)
	}
	f, ok := pro.Update("pro", p.Input{Buttons: p.BtnB})
	if !ok || f.Xbox.Buttons != 0x2000 {
		t.Errorf("Pro Controller B under abxy_mode Switch: %#04x, want 0x2000", f.Xbox.Buttons)
	}
}

func TestDualSenseXboxSticks(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(r []byte)
		lx, ly int16
		rx, ry int16
	}{
		{"left X 255", func(r []byte) { r[1] = 255 }, 32767, 0, 0, 0},
		{"left X 0", func(r []byte) { r[1] = 0 }, -32767, 0, 0, 0},
		{"left Y 0 is up", func(r []byte) { r[2] = 0 }, 0, 32767, 0, 0},
		{"left Y 255 is down", func(r []byte) { r[2] = 255 }, 0, -32767, 0, 0},
		{"right X 255", func(r []byte) { r[3] = 255 }, 0, 0, 32767, 0},
		{"right Y 0 is up", func(r []byte) { r[4] = 0 }, 0, 0, 0, 32767},
		{"both centred", func(r []byte) { r[1], r[2], r[3], r[4] = 128, 128, 128, 128 }, 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pad := dsPad(t, "Xbox", RemapSettings{})
			x := dsXbox(t, pad, dsReport(c.edit))
			if x.LX != c.lx || x.LY != c.ly || x.RX != c.rx || x.RY != c.ry {
				t.Errorf("sticks L(%d,%d) R(%d,%d), want L(%d,%d) R(%d,%d)",
					x.LX, x.LY, x.RX, x.RY, c.lx, c.ly, c.rx, c.ry)
			}
		})
	}
}

func TestDualSenseXboxDeadzone(t *testing.T) {
	pad := dsPad(t, "Xbox", RemapSettings{})

	// Raw 133 is about 3.9% of full deflection on X: inside the 5% deadzone.
	for _, raw := range []byte{133, 123} {
		x := dsXbox(t, pad, dsReport(func(r []byte) { r[1] = raw }))
		if x.LX != 0 || x.LY != 0 {
			t.Errorf("raw X %d (about 4%%): L(%d,%d), want 0,0", raw, x.LX, x.LY)
		}
	}
	// Raw 136 is about 6.3%: outside it.
	for _, raw := range []byte{136, 120} {
		x := dsXbox(t, pad, dsReport(func(r []byte) { r[1] = raw }))
		if x.LX == 0 {
			t.Errorf("raw X %d (about 6%%): LX is 0, want non-zero", raw)
		}
	}
}

func TestDualSenseXboxTriggers(t *testing.T) {
	pad := dsPad(t, "Xbox", RemapSettings{})
	for _, v := range []byte{0, 128, 255} {
		x := dsXbox(t, pad, dsReport(func(r []byte) { r[5], r[6] = v, v }))
		if x.LeftTrigger != v || x.RightTrigger != v {
			t.Errorf("analog %d: triggers %d/%d, want %d/%d", v, x.LeftTrigger, x.RightTrigger, v, v)
		}
	}
	// The digital L2/R2 bits alone don't press the triggers.
	x := dsXbox(t, pad, dsReport(func(r []byte) { r[9] |= 0x04 | 0x08 }))
	if x.LeftTrigger != 0 || x.RightTrigger != 0 {
		t.Errorf("digital bits only: triggers %d/%d, want 0/0", x.LeftTrigger, x.RightTrigger)
	}
	if x.Buttons != 0 {
		t.Errorf("digital bits only: buttons %#04x, want none", x.Buttons)
	}
}

func TestDualSenseXboxUnmappedButtons(t *testing.T) {
	pad := dsPad(t, "Xbox", RemapSettings{})
	neutral := dsXbox(t, pad, dsReport(nil))
	for name, edit := range map[string]func(r []byte){
		"Mute":           func(r []byte) { r[10] |= 0x04 },
		"touchpad click": func(r []byte) { r[10] |= 0x02 },
	} {
		if got := dsXbox(t, pad, dsReport(edit)); got != neutral {
			t.Errorf("%s: %+v, want the neutral state %+v", name, got, neutral)
		}
	}
}

func TestDualSenseXboxPSIgnoresHomeRemap(t *testing.T) {
	for _, remap := range []RemapSettings{{Home: "A"}, {Home: "None"}, {Home: "ZL"}} {
		pad := dsPad(t, "Xbox", remap)
		got := dsXbox(t, pad, dsReport(func(r []byte) { r[10] |= 0x01 })).Buttons
		if got != 0x0400 {
			t.Errorf("home_mapping %q: PS gave %#04x, want Guide 0x0400", remap.Home, got)
		}
	}
}
