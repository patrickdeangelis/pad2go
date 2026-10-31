package dualsense

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

type calibrationTransport struct {
	*fakeTransport
	report []byte
}

func (t *calibrationTransport) Feature(id byte, size int) ([]byte, error) { return t.report, nil }

func TestMotionCalibration(t *testing.T) {
	r := make([]byte, 41)
	r[0] = 5
	put := func(i int, v int16) { binary.LittleEndian.PutUint16(r[i:], uint16(v)) }
	for i := range 3 {
		put(7+4*i, 1000)
		put(9+4*i, -1000)
		put(23+4*i, 8200)
		put(25+4*i, -8184)
	}
	put(19, 100)
	put(21, 100)
	binary.LittleEndian.PutUint32(r[37:], bluetoothCRC(0xa3, r[:37]))
	c, err := ParseMotionCalibration(r, true)
	if err != nil {
		t.Fatal(err)
	}
	gyro, accel := c.Convert([3]int16{1000, -500, 0}, [3]int16{8200, -8184, 8})
	if gyro != [3]float32{100, -50, 0} || accel != [3]float32{1, -1, 0} {
		t.Fatalf("gyro=%v accel=%v", gyro, accel)
	}
	r[37] ^= 1
	if _, err := ParseMotionCalibration(r, true); err == nil {
		t.Fatal("accepted invalid CRC")
	}
	put(7, 0)
	put(9, 0)
	if _, err := ParseMotionCalibration(r, false); err == nil {
		t.Fatal("accepted zero sensor span")
	}
}

func TestDeviceAppliesFactoryCalibration(t *testing.T) {
	r := make([]byte, 41)
	r[0] = 5
	put := func(i int, v int16) { binary.LittleEndian.PutUint16(r[i:], uint16(v)) }
	for i := range 3 {
		put(7+4*i, 1000)
		put(9+4*i, -1000)
		put(23+4*i, 8192)
		put(25+4*i, -8192)
	}
	put(19, 100)
	put(21, 100)
	tr := &calibrationTransport{fakeTransport: newFakeTransport(), report: r}
	d := NewDevice(tr, "test", nil)
	defer d.Close()
	inputs := make(chan protocol.Input, 1)
	d.OnInput(func(in protocol.Input) { inputs <- in })
	input := inputReport(nil)
	binary.LittleEndian.PutUint16(input[16:], 1000)
	binary.LittleEndian.PutUint16(input[22:], 8192)
	tr.reads <- input
	select {
	case got := <-inputs:
		if !got.DualSenseMotionCalibrated || got.DualSenseGyroDPS[0] != 100 || got.DualSenseAccelG[0] != 1 {
			t.Fatalf("motion = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no input")
	}
}
