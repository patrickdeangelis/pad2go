// Package dsu implements a CemuHook/DSU (cemuhook protocol v1001) motion
// server so emulators such as Cemu, Ryujinx, Dolphin and yuzu forks can read
// controller gyro and accelerometer data.
package dsu

import (
	"encoding/binary"
	"hash/crc32"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

const (
	protocolVersion = 1001

	msgVersion = 0x100000
	msgPorts   = 0x100001
	msgPadData = 0x100002
	headerSize = 16
	clientTTL  = 5 * time.Second
	maxPads    = 4
)

// Model values for the pad info block.
const (
	ModelDS4    = 2 // full gyro (Pro Controller / GameCube / merged)
	ModelJoyCon = 3
)

// Pad is one controller sample to publish.
type Pad struct {
	MAC     [6]byte
	Model   byte
	Battery byte // DSU battery enum; 0x05 = full
	Buttons uint32
	// Sticks in [-1,1], +Y up.
	LX, LY, RX, RY float64
	// Motion in DS4 axes: accel in g, gyro in deg/s (pitch, yaw, roll).
	Accel [3]float32
	Gyro  [3]float32
}

type client struct {
	allPads time.Time
	padIDs  [maxPads]time.Time
	padMACs map[[6]byte]time.Time
}

// Server is a DSU UDP server.
type Server struct {
	conn     *net.UDPConn
	log      *slog.Logger
	serverID uint32

	mu      sync.Mutex
	clients map[string]*client
	addrs   map[string]*net.UDPAddr
	slots   [maxPads]*slot
	counter uint32
}

type slot struct {
	mac     [6]byte
	model   byte
	battery byte
	seen    time.Time
}

// Listen starts a server on addr (e.g. "127.0.0.1:26760").
func Listen(addr string, log *slog.Logger) (*Server, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		conn: conn, log: log, serverID: rand.Uint32(),
		clients: map[string]*client{}, addrs: map[string]*net.UDPAddr{},
	}
	go s.serve()
	return s, nil
}

// Addr returns the bound address.
func (s *Server) Addr() net.Addr { return s.conn.LocalAddr() }

// Close stops the server.
func (s *Server) Close() error { return s.conn.Close() }

// Clients returns how many DSU clients (emulators) are currently subscribed.
func (s *Server) Clients() int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.clients {
		if c.alive(now) {
			n++
		}
	}
	return n
}

func (s *Server) serve() {
	buf := make([]byte, 1024)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		s.handle(buf[:n], addr)
	}
}

func (s *Server) handle(data []byte, addr *net.UDPAddr) {
	if len(data) < headerSize+4 || string(data[:4]) != "DSUC" {
		return
	}
	if binary.LittleEndian.Uint16(data[4:6]) > protocolVersion {
		return
	}
	size := int(binary.LittleEndian.Uint16(data[6:8]))
	if len(data) < size+headerSize {
		return
	}
	pkt := append([]byte(nil), data[:size+headerSize]...)
	want := binary.LittleEndian.Uint32(pkt[8:12])
	clear(pkt[8:12])
	if crc32.ChecksumIEEE(pkt) != want {
		return
	}
	switch binary.LittleEndian.Uint32(pkt[16:20]) {
	case msgVersion:
		out := make([]byte, 8)
		binary.LittleEndian.PutUint32(out, msgVersion)
		binary.LittleEndian.PutUint16(out[4:], protocolVersion)
		s.send(addr, out)
	case msgPorts:
		if len(pkt) < 24 {
			return
		}
		n := int(int32(binary.LittleEndian.Uint32(pkt[20:24])))
		if n < 0 || n > maxPads || len(pkt) < 24+n {
			return
		}
		for i := range n {
			s.send(addr, s.portInfo(int(pkt[24+i])))
		}
	case msgPadData:
		if len(pkt) < 28 {
			return
		}
		flags, id := pkt[20], pkt[21]
		var mac [6]byte
		copy(mac[:], pkt[22:28])
		now := time.Now()
		s.mu.Lock()
		key := addr.String()
		c := s.clients[key]
		if c == nil {
			c = &client{padMACs: map[[6]byte]time.Time{}}
			s.clients[key] = c
			s.addrs[key] = addr
			s.log.Info("DSU client subscribed", "client", key)
		}
		if flags == 0 {
			c.allPads = now
		}
		if flags&1 != 0 && int(id) < maxPads {
			c.padIDs[id] = now
		}
		if flags&2 != 0 {
			c.padMACs[mac] = now
		}
		s.mu.Unlock()
	}
}

func (s *Server) portInfo(id int) []byte {
	out := make([]byte, 16)
	binary.LittleEndian.PutUint32(out, msgPorts)
	out[4] = byte(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 0 || id >= maxPads {
		return out
	}
	if sl := s.slots[id]; sl != nil && time.Since(sl.seen) < clientTTL {
		out[5], out[6], out[7] = 2, sl.model, 2
		copy(out[8:14], sl.mac[:])
		out[14] = sl.battery
	}
	return out
}

func (s *Server) send(addr *net.UDPAddr, payload []byte) {
	pkt := make([]byte, headerSize+len(payload))
	copy(pkt, "DSUS")
	binary.LittleEndian.PutUint16(pkt[4:], protocolVersion)
	binary.LittleEndian.PutUint16(pkt[6:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(pkt[12:], s.serverID)
	copy(pkt[headerSize:], payload)
	binary.LittleEndian.PutUint32(pkt[8:], crc32.ChecksumIEEE(pkt))
	if _, err := s.conn.WriteToUDP(pkt, addr); err != nil {
		s.log.Debug("DSU send failed", "err", err)
	}
}

// slotFor returns the pad slot for mac, assigning the first free (or oldest) one.
func (s *Server) slotFor(p *Pad, now time.Time) int {
	free, oldest := -1, 0
	for i, sl := range s.slots {
		if sl != nil && sl.mac == p.MAC {
			return i
		}
		if sl == nil || now.Sub(sl.seen) > clientTTL {
			if free < 0 {
				free = i
			}
		} else if s.slots[oldest] != nil && sl.seen.Before(s.slots[oldest].seen) {
			oldest = i
		}
	}
	if free >= 0 {
		return free
	}
	return oldest
}

// Publish sends a pad sample to every subscribed client.
func (s *Server) Publish(p Pad) {
	now := time.Now()
	s.mu.Lock()
	id := s.slotFor(&p, now)
	s.slots[id] = &slot{mac: p.MAC, model: p.Model, battery: p.Battery, seen: now}
	var targets []*net.UDPAddr
	for key, c := range s.clients {
		switch {
		case now.Sub(c.allPads) < clientTTL, now.Sub(c.padIDs[id]) < clientTTL, now.Sub(c.padMACs[p.MAC]) < clientTTL:
			targets = append(targets, s.addrs[key])
		default:
			if !c.alive(now) {
				delete(s.clients, key)
				delete(s.addrs, key)
			}
		}
	}
	s.counter++
	counter := s.counter
	s.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	payload := encodePadData(byte(id), counter, p, now)
	for _, a := range targets {
		s.send(a, payload)
	}
}

func (c *client) alive(now time.Time) bool {
	if now.Sub(c.allPads) < clientTTL {
		return true
	}
	for _, t := range c.padIDs {
		if now.Sub(t) < clientTTL {
			return true
		}
	}
	for _, t := range c.padMACs {
		if now.Sub(t) < clientTTL {
			return true
		}
	}
	return false
}

func stickByte(v float64) byte {
	return byte(math.Round(127.5 + math.Max(-1, math.Min(1, v))*127.5))
}

func encodePadData(id byte, counter uint32, p Pad, now time.Time) []byte {
	out := make([]byte, 0, 84)
	out = binary.LittleEndian.AppendUint32(out, msgPadData)
	out = append(out, id, 2, p.Model, 2)
	out = append(out, p.MAC[:]...)
	out = append(out, p.Battery, 1)
	out = binary.LittleEndian.AppendUint32(out, counter)

	b := p.Buttons
	bit := func(mask uint32, v byte) byte {
		if b&mask != 0 {
			return v
		}
		return 0
	}
	btn1 := bit(protocol.BtnLeft, 0x80) | bit(protocol.BtnDown, 0x40) | bit(protocol.BtnRight, 0x20) |
		bit(protocol.BtnUp, 0x10) | bit(protocol.BtnPlus, 0x08) | bit(protocol.BtnRStick, 0x04) |
		bit(protocol.BtnLStick, 0x02) | bit(protocol.BtnMinus, 0x01)
	btn2 := bit(protocol.BtnY, 0x80) | bit(protocol.BtnB, 0x40) | bit(protocol.BtnA, 0x20) |
		bit(protocol.BtnX, 0x10) | bit(protocol.BtnR, 0x08) | bit(protocol.BtnL, 0x04) |
		bit(protocol.BtnZR, 0x02) | bit(protocol.BtnZL, 0x01)
	out = append(out, btn1, btn2, bit(protocol.BtnHome, 1), bit(protocol.BtnCapture, 1))
	// DSU sticks: 0-255, plus rightward and upward (the CemuHook spec; Eden
	// and other yuzu forks read them that way).
	out = append(out, stickByte(p.LX), stickByte(p.LY), stickByte(p.RX), stickByte(p.RY))
	// Analog D-pad L,D,R,U, Y,B,A,X, R1,L1 then R2,L2.
	analog := []uint32{protocol.BtnLeft, protocol.BtnDown, protocol.BtnRight, protocol.BtnUp,
		protocol.BtnY, protocol.BtnB, protocol.BtnA, protocol.BtnX, protocol.BtnR, protocol.BtnL,
		protocol.BtnZR, protocol.BtnZL}
	for _, m := range analog {
		out = append(out, bit(m, 255))
	}
	out = append(out, make([]byte, 12)...) // two empty touch points
	out = binary.LittleEndian.AppendUint64(out, uint64(now.UnixMicro()))
	for _, v := range p.Accel {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
	}
	for _, v := range p.Gyro {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
	}
	return out
}

// YawScale is the multiplier the cemuhook_sensitivity setting (1-5) applies
// to the yaw axis only, as in the original.
func YawScale(sensitivity int) float32 {
	if sensitivity <= 1 {
		return 1
	}
	return 1 + float32(sensitivity-1)/12
}

// BatteryLevel maps a percentage to the DSU battery enum.
func BatteryLevel(percent int) byte {
	switch {
	case percent < 0:
		return 0x00 // not applicable / unknown
	case percent <= 5:
		return 0x01
	case percent <= 10:
		return 0x02
	case percent <= 40:
		return 0x03
	case percent <= 80:
		return 0x04
	default:
		return 0x05
	}
}
