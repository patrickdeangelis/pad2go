package dsu

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"net"
	"testing"
	"time"

	"github.com/angelispatrick/pad2go/internal/protocol"
)

func clientPacket(msgType uint32, body []byte) []byte {
	payload := binary.LittleEndian.AppendUint32(nil, msgType)
	payload = append(payload, body...)
	pkt := make([]byte, headerSize+len(payload))
	copy(pkt, "DSUC")
	binary.LittleEndian.PutUint16(pkt[4:], protocolVersion)
	binary.LittleEndian.PutUint16(pkt[6:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(pkt[12:], 1234)
	copy(pkt[headerSize:], payload)
	binary.LittleEndian.PutUint32(pkt[8:], crc32.ChecksumIEEE(pkt))
	return pkt
}

func readServer(t *testing.T, c *net.UDPConn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	pkt := buf[:n]
	if string(pkt[:4]) != "DSUS" {
		t.Fatalf("bad magic %q", pkt[:4])
	}
	if int(binary.LittleEndian.Uint16(pkt[6:8])) != n-headerSize {
		t.Fatalf("length field %d, packet %d", binary.LittleEndian.Uint16(pkt[6:8]), n)
	}
	crc := binary.LittleEndian.Uint32(pkt[8:12])
	check := append([]byte(nil), pkt...)
	clear(check[8:12])
	if crc32.ChecksumIEEE(check) != crc {
		t.Fatal("bad CRC")
	}
	return pkt[headerSize:]
}

func setup(t *testing.T) (*Server, *net.UDPConn) {
	t.Helper()
	s, err := Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c, err := net.DialUDP("udp", nil, s.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return s, c
}

func TestVersion(t *testing.T) {
	_, c := setup(t)
	c.Write(clientPacket(msgVersion, nil))
	resp := readServer(t, c)
	if binary.LittleEndian.Uint32(resp) != msgVersion || binary.LittleEndian.Uint16(resp[4:]) != protocolVersion {
		t.Fatalf("resp %x", resp)
	}
}

func TestBadCRCIgnored(t *testing.T) {
	_, c := setup(t)
	pkt := clientPacket(msgVersion, nil)
	pkt[8]++
	c.Write(pkt)
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := c.Read(make([]byte, 64)); err == nil {
		t.Fatal("packet with bad CRC should be ignored")
	}
}

func TestPadDataFlow(t *testing.T) {
	s, c := setup(t)
	mac := [6]byte{1, 2, 3, 4, 5, 6}
	pad := Pad{MAC: mac, Model: ModelDS4, Battery: 5, Buttons: protocol.BtnA | protocol.BtnZL, LX: 1, LY: 1,
		Accel: [3]float32{0, 1, 0}, Gyro: [3]float32{10, 20, 30}}

	// Unsubscribed: nothing is sent, but the slot becomes visible in port info.
	s.Publish(pad)
	c.Write(clientPacket(msgPorts, []byte{1, 0, 0, 0, 0}))
	info := readServer(t, c)
	if info[4] != 0 || info[5] != 2 || info[6] != ModelDS4 || [6]byte(info[8:14]) != mac {
		t.Fatalf("port info %x", info)
	}

	// Subscribe to all pads, then publish.
	c.Write(clientPacket(msgPadData, make([]byte, 8)))
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.clients)
		s.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Publish(pad)
	data := readServer(t, c)
	if len(data) != 84 {
		t.Fatalf("pad data length %d", len(data))
	}
	if binary.LittleEndian.Uint32(data) != msgPadData || data[4] != 0 || data[5] != 2 || [6]byte(data[8:14]) != mac || data[14] != 5 || data[15] != 1 {
		t.Fatalf("header %x", data[:16])
	}
	if data[20] != 0 || data[21] != 0x20|0x01 { // btn2: A→Circle, ZL→L2
		t.Fatalf("buttons %x %x", data[20], data[21])
	}
	if data[24] != 255 || data[25] != 0 { // LX full right, LY full up (DSU Y is inverted)
		t.Fatalf("sticks %v", data[24:28])
	}
	f := func(off int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(data[off:])) }
	accel := [3]float32{f(60), f(64), f(68)}
	gyro := [3]float32{f(72), f(76), f(80)}
	if accel != pad.Accel || gyro != pad.Gyro {
		t.Fatalf("motion accel %v gyro %v", accel, gyro)
	}
}

func TestSlotsReuseMAC(t *testing.T) {
	s, _ := setup(t)
	a := Pad{MAC: [6]byte{1}}
	b := Pad{MAC: [6]byte{2}}
	s.Publish(a)
	s.Publish(b)
	s.Publish(a)
	if s.slots[0].mac != a.MAC || s.slots[1].mac != b.MAC || s.slots[2] != nil {
		t.Fatal("slots should be stable per MAC")
	}
}

func TestYawScale(t *testing.T) {
	if YawScale(1) != 1 || YawScale(0) != 1 || math.Abs(float64(YawScale(5))-(1+4.0/12)) > 1e-6 {
		t.Fatal("yaw scale")
	}
}

func TestBatteryLevel(t *testing.T) {
	if BatteryLevel(100) != 5 || BatteryLevel(3) != 1 || BatteryLevel(50) != 4 {
		t.Fatal("battery mapping")
	}
}
