package demo

import (
	"testing"

	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestDemoDualSenseTouchAndCalibration(t *testing.T) {
	for _, c := range []struct {
		frame    uint64
		contacts int
		click    bool
	}{{20, 1, false}, {150, 2, false}, {210, 0, true}} {
		r := demoDualSenseInput(c.frame)
		in, ok := dualsense.Parse(r[:])
		if !ok {
			t.Fatal("invalid demo report")
		}
		contacts := 0
		for _, p := range in.Touch {
			if p.Active {
				contacts++
			}
		}
		if contacts != c.contacts || (in.Buttons&protocol.BtnPSCClick != 0) != c.click {
			t.Fatalf("frame %d: touch=%+v buttons=%x", c.frame, in.Touch, in.Buttons)
		}
	}
	tr := &hidTransport{}
	data, err := tr.Feature(5, 41)
	if err != nil {
		t.Fatal(err)
	}
	cal, err := dualsense.ParseMotionCalibration(data, false)
	if err != nil {
		t.Fatal(err)
	}
	_, a := cal.Convert([3]int16{}, [3]int16{0, 0, -8192})
	if a[2] != -1 {
		t.Fatalf("demo acceleration %v", a)
	}
}
