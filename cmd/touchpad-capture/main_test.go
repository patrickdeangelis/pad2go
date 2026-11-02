package main

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"net"
	"testing"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func TestParsePadDataTouchAndGyro(t *testing.T) {
	packet := makeDSUPadPacket()
	got, ok := parsePadData(packet)
	if !ok {
		t.Fatal("valid DSU pad packet was rejected")
	}
	if got.pad != 2 || got.counter != 0x10203040 {
		t.Fatalf("pad/counter = %d/%x", got.pad, got.counter)
	}
	if got.touch1 != (contactSample{active: 1, id: 9, x: 480, y: 540}) {
		t.Fatalf("touch 1 = %+v", got.touch1)
	}
	if got.touch2 != (contactSample{id: 10, x: 960, y: 270}) {
		t.Fatalf("touch 2 = %+v", got.touch2)
	}
	if got.gyro != [3]float32{1.25, -2.5, 3.75} {
		t.Fatalf("gyro = %v", got.gyro)
	}
	row := got.csvRow(time.Unix(10, 0), time.Unix(9, 0))
	if row[1] != "1000.000" || row[6] != "480" || row[7] != "540" || row[13] != "-2.500000" {
		t.Fatalf("CSV row fields = %v", row)
	}
}

func TestParsePadDataRejectsBadCRCAndWrongSize(t *testing.T) {
	packet := makeDSUPadPacket()
	packet[40] ^= 0xff
	if _, ok := parsePadData(packet); ok {
		t.Fatal("packet with invalid CRC was accepted")
	}
	if _, ok := parsePadData(make([]byte, 40)); ok {
		t.Fatal("short packet was accepted")
	}
}

func TestSendSubscribeAll(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	result := make(chan error, 1)
	go func() { result <- sendSubscribeAll(client) }()
	buf := make([]byte, 64)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	packet := buf[:n]
	if len(packet) != dsuHeaderSize+12 || string(packet[:4]) != "DSUC" || binary.LittleEndian.Uint16(packet[4:6]) != dsuVersion || binary.LittleEndian.Uint32(packet[16:20]) != msgPadData {
		t.Fatalf("unexpected subscription packet: %x", packet)
	}
	if binary.LittleEndian.Uint32(packet[20:24]) != 0 {
		t.Fatalf("subscription flags = %d; want all pads", binary.LittleEndian.Uint32(packet[20:24]))
	}
	check := append([]byte(nil), packet...)
	want := binary.LittleEndian.Uint32(check[8:12])
	clear(check[8:12])
	if crc32.ChecksumIEEE(check) != want {
		t.Fatal("subscription packet CRC is invalid")
	}
}

func TestCaptureClientReceivesPublishedTouchSamples(t *testing.T) {
	server, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := net.DialUDP("udp", nil, server.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := sendSubscribeAll(client); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for server.Clients() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Clients() == 0 {
		t.Fatal("DSU server did not register capture client")
	}
	server.Publish(dsu.Pad{
		MAC:   [6]byte{2, 0, 0, 0, 0, 1},
		Model: dsu.ModelDS4,
		Touch: [2]protocol.TouchContact{{Active: true, ID: 4, X: 480, Y: 540}},
		Gyro:  [3]float32{1.5, -2.5, 0.25},
	})
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	packet := make([]byte, 128)
	n, err := client.Read(packet)
	if err != nil {
		t.Fatal(err)
	}
	sample, ok := parsePadData(packet[:n])
	if !ok || sample.touch1 != (contactSample{active: 1, id: 4, x: 480, y: 540}) || sample.gyro != [3]float32{1.5, -2.5, 0.25} {
		t.Fatalf("captured sample = %+v, parsed=%v", sample, ok)
	}
}

func makeDSUPadPacket() []byte {
	body := make([]byte, padBodySize)
	binary.LittleEndian.PutUint32(body, msgPadData)
	body[4] = 2
	binary.LittleEndian.PutUint32(body[16:], 0x10203040)
	body[40], body[41] = 1, 9
	binary.LittleEndian.PutUint16(body[42:], 480)
	binary.LittleEndian.PutUint16(body[44:], 540)
	body[47] = 10
	binary.LittleEndian.PutUint16(body[48:], 960)
	binary.LittleEndian.PutUint16(body[50:], 270)
	for i, v := range []float32{1.25, -2.5, 3.75} {
		binary.LittleEndian.PutUint32(body[72+i*4:], math.Float32bits(v))
	}
	packet := make([]byte, dsuHeaderSize+len(body))
	copy(packet, "DSUS")
	binary.LittleEndian.PutUint16(packet[4:], dsuVersion)
	binary.LittleEndian.PutUint16(packet[6:], uint16(len(body)))
	binary.LittleEndian.PutUint32(packet[12:], 42)
	copy(packet[dsuHeaderSize:], body)
	binary.LittleEndian.PutUint32(packet[8:], crc32.ChecksumIEEE(packet))
	return packet
}
