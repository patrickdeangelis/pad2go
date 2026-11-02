package main

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"net"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestSwipeAngleIndependentOfReportRate(t *testing.T) {
	for _, hz := range []int{60, 125, 250} {
		c := converter{}
		start := time.Unix(100, 0)
		var angle float64
		for i := 0; i <= hz; i++ {
			motion := c.sample([2]protocol.TouchContact{{Active: true, ID: 1, X: uint16(i * 1920 / hz)}}, start.Add(time.Duration(i)*time.Second/time.Duration(hz)))
			angle += float64(motion.Gyro[1]) / float64(hz)
		}
		if math.Abs(angle-90) > .001 {
			t.Fatalf("hz=%d angle=%f", hz, angle)
		}
	}
}

func TestReleaseRecontactGapAndIdle(t *testing.T) {
	c := converter{}
	now := time.Unix(100, 0)
	steps := []struct {
		active bool
		id     byte
		x      uint16
		dt     time.Duration
		moving bool
	}{
		{true, 1, 100, time.Millisecond, false}, {true, 1, 200, 8 * time.Millisecond, true},
		{true, 1, 200, 8 * time.Millisecond, false}, {false, 1, 0, 8 * time.Millisecond, false},
		{true, 2, 1500, 8 * time.Millisecond, false}, {true, 3, 100, 8 * time.Millisecond, false},
		{true, 3, 1000, 300 * time.Millisecond, true}, {true, 3, 1100, 8 * time.Millisecond, true},
	}
	for i, s := range steps {
		now = now.Add(s.dt)
		motion := c.sample([2]protocol.TouchContact{{Active: s.active, ID: s.id, X: s.x}}, now)
		if (motion.Gyro != [3]float32{}) != s.moving {
			t.Fatalf("step %d gyro=%v", i, motion.Gyro)
		}
	}
}

func TestDiagonalScaleAndVerticalDirection(t *testing.T) {
	c := converter{}
	now := time.Unix(100, 0)
	c.sample([2]protocol.TouchContact{{Active: true, ID: 1}}, now)
	motion := c.sample([2]protocol.TouchContact{{Active: true, ID: 1, X: 1920, Y: 1080}}, now.Add(time.Millisecond))
	g := motion.Gyro
	if g[0] >= 0 || g[1] <= 0 || math.Abs(float64(g[0])*.001+60) > .01 || math.Abs(float64(g[1])*.001-90) > .01 {
		t.Fatalf("diagonal normalized displacement/direction %v", g)
	}
	if motion.Accel != [3]float32{} {
		t.Fatalf("synthetic mouse motion must have zero acceleration: %v", motion.Accel)
	}
}

func TestUSBTouchToRealDSUPacket(t *testing.T) {
	s, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client, err := net.DialUDP("udp", nil, s.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := make([]byte, 28)
	copy(request, "DSUC")
	binary.LittleEndian.PutUint16(request[4:], 1001)
	binary.LittleEndian.PutUint16(request[6:], 12)
	binary.LittleEndian.PutUint32(request[16:], 0x100002)
	if _, err = client.Write(requestWithCRC(request)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.Clients() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Clients() == 0 {
		t.Fatal("subscription not registered")
	}
	c := converter{}
	now := time.Unix(100, 0)
	report := make([]byte, 64)
	report[0] = 1
	report[37] = 0x80
	setTouch := func(x uint16) protocol.Input {
		report[33] = 1
		report[34] = byte(x)
		report[35] = byte(x >> 8)
		in, ok := dualsense.Parse(report)
		if !ok {
			t.Fatal("USB rejected")
		}
		return in
	}
	first := setTouch(100)
	c.sample(first.Touch, now)
	in := setTouch(200)
	motion := c.sample(in.Touch, now.Add(10*time.Millisecond))
	s.Publish(dsu.Pad{MAC: [6]byte{2, 0, 0, 0, 0, 99}, Model: dsu.ModelDS4, Gyro: motion.Gyro, Accel: motion.Accel, Touch: in.Touch})
	client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 128)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 100 || string(buf[:4]) != "DSUS" {
		t.Fatalf("invalid DSU packet: bytes=%d", n)
	}
	crc := binary.LittleEndian.Uint32(buf[8:])
	clear(buf[8:12])
	if crc32.ChecksumIEEE(buf[:n]) != crc {
		t.Fatal("CRC mismatch")
	}
	if buf[56] != 1 || binary.LittleEndian.Uint16(buf[58:60]) != 200 {
		t.Fatal("native touch did not survive the DSU transport")
	}
	for _, offset := range []int{76, 80, 84} {
		if math.Float32frombits(binary.LittleEndian.Uint32(buf[offset:offset+4])) != 0 {
			t.Fatal("synthetic pointer motion must not include acceleration")
		}
	}
	yaw := math.Float32frombits(binary.LittleEndian.Uint32(buf[92:96]))
	if math.Abs(float64(yaw)-468.75) > 0.01 {
		t.Fatalf("wire yaw=%f", yaw)
	}
}

func requestWithCRC(p []byte) []byte {
	binary.LittleEndian.PutUint32(p[8:], crc32.ChecksumIEEE(p))
	return p
}
