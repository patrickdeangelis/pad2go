package protocol

import (
	"encoding/binary"
	"fmt"
)

// InitCommand is one step of the SW2 initialization sequence.
type InitCommand struct {
	Cmd, Sub byte
	Data     []byte
}

// SW2InitSequence is sent after the command-response notification is enabled.
// Feature select 0x94 enables only motion+mouse+magnetometer; enabling every
// feature makes Joy-Cons stream phantom ZL/ZR bits.
var SW2InitSequence = []InitCommand{
	{0x03, 0x0d, []byte{0x01, 0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	{0x07, 0x01, nil},
	{0x16, 0x01, nil},
	{0x15, 0x03, []byte{0x00}},
	{0x0c, 0x02, []byte{0x94, 0x00, 0x00, 0x00}},
	{0x11, 0x03, nil},
	{0x0a, 0x08, []byte{0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x35, 0x00, 0x46, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
	{0x0c, 0x04, []byte{0x94, 0x00, 0x00, 0x00}},
	{0x03, 0x0a, []byte{0x09, 0x00, 0x00, 0x00}},
	{0x10, 0x01, nil},
	{0x01, 0x0c, nil},
	{0x01, 0x01, []byte{0x00, 0x00, 0x00, 0x00}},
	{0x09, 0x07, []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
}

// InitSequenceFor returns the init sequence for a product. The Pro Controller 2
// rejects 01:01 (status 4) while everything else succeeds without it.
func InitSequenceFor(pid uint16) []InitCommand {
	if pid != ProController2PID {
		return SW2InitSequence
	}
	out := make([]InitCommand, 0, len(SW2InitSequence))
	for _, c := range SW2InitSequence {
		if c.Cmd == 0x01 && c.Sub == 0x01 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// SetInputMode30 switches GameCube and Joy-Con 2 controllers to input report
// format 3 (0x30), which keeps status bits out of the button field. It is
// written raw to the command characteristic (no response expected).
var SetInputMode30 = []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x30}

// GameCubeIMUInit is written raw before enabling features on the NSO GameCube controller.
var GameCubeIMUInit = [][]byte{
	{0x11, 0x91, 0x01, 0x03, 0x00, 0x00, 0x00, 0x00},
	{0x0A, 0x91, 0x01, 0x08, 0x00, 0x14, 0x00, 0x00,
		0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0x35, 0x00, 0x46, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
}

// Pairing long-term keys sent during bonding.
var (
	PairLTK1 = []byte{0x00, 0xea, 0xbd, 0x47, 0x13, 0x89, 0x35, 0x42, 0xc6, 0x79, 0xee, 0x07, 0xf2, 0x53, 0x2c, 0x6c, 0x31}
	PairLTK2 = []byte{0x00, 0x40, 0xb0, 0x8a, 0x5f, 0xcd, 0x1f, 0x9b, 0x41, 0x12, 0x5c, 0xac, 0xc6, 0x3f, 0x38, 0xa0, 0x73}
)

// EncodeCommand builds a command buffer: cmd, 0x91, 0x01, sub, 0x00, len, 0x00, 0x00, data...
func EncodeCommand(cmd, sub byte, data []byte) []byte {
	buf := make([]byte, 0, 8+len(data))
	buf = append(buf, cmd, 0x91, 0x01, sub, 0x00, byte(len(data)), 0x00, 0x00)
	return append(buf, data...)
}

// DecodeResponse validates a command response and returns its payload (bytes after the 8-byte header).
func DecodeResponse(cmd byte, resp []byte) ([]byte, error) {
	if len(resp) < 8 || resp[0] != cmd || resp[1] != 0x01 {
		return nil, fmt.Errorf("unexpected response to command 0x%02x: % x", cmd, resp)
	}
	return resp[8:], nil
}

// PadFeature returns a 4-byte little-endian feature/argument block holding v.
func PadFeature(v byte) []byte { return []byte{v, 0, 0, 0} }

// LEDData returns the argument for CmdLEDs/SubLEDsSetPlayer.
func LEDData(player int, reversed bool) []byte {
	if player > 8 {
		player = 8
	}
	if player < 1 {
		player = 1
	}
	v := LEDPattern[player]
	if reversed {
		v = reverseBits(v, 4)
	}
	return PadFeature(v)
}

func reverseBits(n byte, bits int) byte {
	var r byte
	for range bits {
		r = r<<1 | n&1
		n >>= 1
	}
	return r
}

// MemoryReadData returns the argument for CmdMemory/SubMemoryRead.
func MemoryReadData(length byte, address uint32) ([]byte, error) {
	if length > MaxMemoryRead {
		return nil, fmt.Errorf("maximum read size is 0x%x bytes", MaxMemoryRead)
	}
	buf := []byte{length, 0x7e, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(buf[4:], address)
	return buf, nil
}

// ParseMemoryRead checks a memory-read response payload and returns the data bytes.
func ParseMemoryRead(length byte, address uint32, payload []byte) ([]byte, error) {
	if len(payload) < 8 || payload[0] != length || binary.LittleEndian.Uint32(payload[4:8]) != address {
		return nil, fmt.Errorf("unexpected memory read response: % x", payload)
	}
	return payload[8:], nil
}

// PairSetMACData is the argument of CmdPair/SubPairSetMAC for a host MAC.
func PairSetMACData(host uint64) []byte {
	buf := []byte{0x00, 0x02}
	buf = append(buf, macLE(host)...)
	return append(buf, macLE(host)...)
}

func macLE(v uint64) []byte {
	b := make([]byte, 6)
	for i := range b {
		b[i] = byte(v >> (8 * i))
	}
	return b
}

// GameCubeRumblePayload is the command-channel on/off rumble packet used by the
// NSO GameCube controller, which has a single ERM motor.
func GameCubeRumblePayload(on bool) []byte {
	var v byte
	if on {
		v = 1
	}
	return []byte{0x0A, 0x91, 0x01, 0x02, 0x00, 0x04, 0x00, 0x00, v, 0x00, 0x00, 0x00}
}
