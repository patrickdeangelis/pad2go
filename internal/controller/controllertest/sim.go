// Package controllertest provides a simulated Switch 2 controller that
// satisfies controller.Transport, for tests and benchmarks that need the real
// controller handshake without Bluetooth.
package controllertest

import (
	"encoding/binary"
	"slices"
	"sync"

	"github.com/angelispatrick/pad2go/internal/protocol"
)

// Write is one recorded write to a characteristic.
type Write struct {
	UUID string
	Data []byte
}

// Sim answers commands like a real controller: every command gets a success
// response (or an error status if marked failing), memory reads return
// Memory's contents, and raw writes (input mode, rumble) are only recorded.
type Sim struct {
	Addr   string
	Memory map[uint32][]byte
	// Silent drops all responses (to exercise timeouts).
	Silent bool

	mu       sync.Mutex
	failing  map[[2]byte]bool
	subs     map[string]func([]byte)
	writes   []Write
	closed   bool
	onClosed func()
}

func packStick(x, y int) []byte {
	v := x | y<<12
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

// DefaultStickCalibration is the 11-byte calibration block the Sim serves:
// center 2000, range ±1500 on both axes.
var DefaultStickCalibration = append(append(append(packStick(2000, 2000), packStick(1500, 1500)...), packStick(1500, 1500)...), 0, 0)

// New simulates a controller with product ID pid at addr. The user
// calibration for stick 1 is erased, so the factory block is used.
func New(addr string, pid uint16) *Sim {
	info := make([]byte, 0x40)
	copy(info[2:], "SERIAL00000001")
	binary.LittleEndian.PutUint16(info[18:], protocol.NintendoVendorID)
	binary.LittleEndian.PutUint16(info[20:], pid)
	return &Sim{
		Addr: addr,
		Memory: map[uint32][]byte{
			protocol.AddrControllerInfo:           info,
			protocol.AddrUserCalibrationJoystick1: {0xFF, 0xFF, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0},
			protocol.AddrCalibrationJoystick1:     DefaultStickCalibration,
			protocol.AddrUserCalibrationJoystick2: DefaultStickCalibration,
		},
		failing: map[[2]byte]bool{},
		subs:    map[string]func([]byte){},
	}
}

// Fail makes a command answer with an error status.
func (s *Sim) Fail(cmd, sub byte) {
	s.mu.Lock()
	s.failing[[2]byte{cmd, sub}] = true
	s.mu.Unlock()
}

// OnClosed runs fn when the transport is disconnected by the host.
func (s *Sim) OnClosed(fn func()) {
	s.mu.Lock()
	s.onClosed = fn
	s.mu.Unlock()
}

func (s *Sim) Address() string { return s.Addr }

func (s *Sim) Subscribe(uuid string, fn func([]byte)) error {
	s.mu.Lock()
	s.subs[uuid] = fn
	s.mu.Unlock()
	return nil
}

func (s *Sim) Disconnect() error {
	s.mu.Lock()
	s.closed = true
	fn := s.onClosed
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

// Closed reports whether the host disconnected.
func (s *Sim) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Notify delivers a notification on uuid, as the controller would.
func (s *Sim) Notify(uuid string, data []byte) {
	s.mu.Lock()
	fn := s.subs[uuid]
	s.mu.Unlock()
	if fn != nil {
		fn(data)
	}
}

// SendReport delivers a raw input report.
func (s *Sim) SendReport(data []byte) { s.Notify(protocol.InputReportUUID, data) }

func (s *Sim) Write(uuid string, data []byte, _ bool) error {
	s.mu.Lock()
	s.writes = append(s.writes, Write{uuid, slices.Clone(data)})
	fail := len(data) >= 4 && s.failing[[2]byte{data[0], data[3]}]
	silent := s.Silent
	s.mu.Unlock()
	if silent || uuid != protocol.CommandWriteUUID || len(data) < 8 || data[1] != 0x91 {
		return nil
	}
	cmd, sub := data[0], data[3]
	resp := []byte{cmd, 0x01, 0, 0, 0, 0, 0, 0}
	if fail {
		resp[1] = 0x04
	}
	if cmd == protocol.CmdMemory && sub == protocol.SubMemoryRead {
		arg := data[8:]
		length, addr := arg[0], binary.LittleEndian.Uint32(arg[4:8])
		mem := make([]byte, int(length))
		s.mu.Lock()
		copy(mem, s.Memory[addr])
		s.mu.Unlock()
		resp = append(resp, arg[:8]...)
		resp = append(resp, mem...)
	}
	go s.Notify(protocol.CommandResponseUUID, resp)
	return nil
}

// Writes returns every write so far.
func (s *Sim) Writes() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes)
}

// Commands returns the (command, subcommand) pairs sent, in order.
func (s *Sim) Commands() [][2]byte {
	var out [][2]byte
	for _, w := range s.Writes() {
		if w.UUID == protocol.CommandWriteUUID && len(w.Data) >= 8 && w.Data[1] == 0x91 {
			out = append(out, [2]byte{w.Data[0], w.Data[3]})
		}
	}
	return out
}

// LastWrite returns the most recent write to uuid, or nil.
func (s *Sim) LastWrite(uuid string) []byte {
	w := s.Writes()
	for i := len(w) - 1; i >= 0; i-- {
		if w[i].UUID == uuid {
			return w[i].Data
		}
	}
	return nil
}

// Report builds a standard (Joy-Con 2 / Pro Controller 2) raw input report.
// Sticks are raw 12-bit values; the Sim's calibration centers them at 2000.
func Report(buttons uint32, left, right [2]int) []byte {
	b := make([]byte, 64)
	binary.LittleEndian.PutUint32(b[4:], buttons)
	copy(b[10:], packStick(left[0], left[1]))
	copy(b[13:], packStick(right[0], right[1]))
	binary.LittleEndian.PutUint16(b[31:], 3900)
	return b
}

// Center is the raw stick position the Sim's calibration treats as neutral.
var Center = [2]int{2000, 2000}
