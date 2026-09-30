// Package demo provides a simulated Bluetooth radio for `pad2go -demo`: a
// Joy-Con pair, a Pro Controller 2 and a GameCube controller that advertise,
// connect through the real controller handshake and stream animated input.
// A controller disconnected by the host advertises again a few seconds later,
// like one whose button is pressed.
package demo

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/angelispatrick/pad2go/internal/controller"
	"github.com/angelispatrick/pad2go/internal/controller/controllertest"
	"github.com/angelispatrick/pad2go/internal/lifecycle"
	"github.com/angelispatrick/pad2go/internal/protocol"
)

// HostMAC is the simulated adapter address.
const HostMAC = 0xA0B1C2D3E4F5

type device struct {
	addr    string
	pid     uint16
	paired  bool    // advertises as bonded to this host (no pairing step)
	voltage float64 // battery
	delay   time.Duration
}

// Radio is a simulated lifecycle.Radio.
type Radio struct {
	// Reconnect is how long a disconnected controller waits before
	// advertising again (default 5 s).
	Reconnect time.Duration

	mu      sync.Mutex
	devices []*device
	visible map[string]bool
}

// NewRadio returns a radio with four simulated controllers. They start
// advertising one after another.
func NewRadio() *Radio {
	r := &Radio{Reconnect: 5 * time.Second, visible: map[string]bool{}}
	r.devices = []*device{
		{addr: "D0:00:00:00:00:01", pid: protocol.JoyCon2LeftPID, voltage: 3.42, delay: 500 * time.Millisecond},
		{addr: "D0:00:00:00:00:02", pid: protocol.JoyCon2RightPID, voltage: 3.95, delay: 3 * time.Second},
		{addr: "D0:00:00:00:00:03", pid: protocol.ProController2PID, voltage: 4.05, paired: true, delay: 6 * time.Second},
		{addr: "D0:00:00:00:00:04", pid: protocol.NSOGameCubeControllerPID, voltage: 3.7, paired: true, delay: 9 * time.Second},
	}
	for _, d := range r.devices {
		r.advertiseAfter(d, d.delay)
	}
	return r
}

func (r *Radio) advertiseAfter(d *device, after time.Duration) {
	time.AfterFunc(after, func() {
		r.mu.Lock()
		r.visible[d.addr] = true
		r.mu.Unlock()
	})
}

// HostMAC implements lifecycle.Radio.
func (r *Radio) HostMAC() (uint64, error) { return HostMAC, nil }

// Scan implements lifecycle.Radio.
func (r *Radio) Scan(ctx context.Context, accept func(lifecycle.Advert) bool) (lifecycle.Advert, error) {
	for {
		r.mu.Lock()
		var adverts []lifecycle.Advert
		for _, d := range r.devices {
			if !r.visible[d.addr] {
				continue
			}
			var reconnect uint64
			if d.paired {
				reconnect = HostMAC
			}
			adverts = append(adverts, lifecycle.Advert{Addr: d.addr, RSSI: -48, Adv: protocol.Advertisement{
				VendorID: protocol.NintendoVendorID, ProductID: d.pid, ReconnectMAC: reconnect}})
		}
		r.mu.Unlock()
		for _, a := range adverts {
			if accept(a) {
				return a, nil
			}
		}
		select {
		case <-ctx.Done():
			return lifecycle.Advert{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Connect implements lifecycle.Radio.
func (r *Radio) Connect(_ context.Context, a lifecycle.Advert, _ func()) (controller.Transport, error) {
	var d *device
	r.mu.Lock()
	for _, dd := range r.devices {
		if dd.addr == a.Addr {
			d = dd
		}
	}
	r.visible[a.Addr] = false
	r.mu.Unlock()

	sim := controllertest.New(d.addr, d.pid)
	stop := make(chan struct{})
	var once sync.Once
	sim.OnClosed(func() {
		once.Do(func() { close(stop) })
		// Once paired, it comes back bonded to this host.
		r.mu.Lock()
		d.paired = true
		r.mu.Unlock()
		r.advertiseAfter(d, r.Reconnect)
	})
	go feed(sim, d, stop)
	return sim, nil
}

// feed streams ~60 Hz input: a neutral frame first (so the settle gate
// opens), then circling sticks, cycling buttons, trigger sweeps and gyro.
func feed(sim *controllertest.Sim, d *device, stop <-chan struct{}) {
	t := time.NewTicker(16 * time.Millisecond)
	defer t.Stop()
	start := time.Now()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		el := time.Since(start).Seconds()
		if el < 1.5 {
			sim.SendReport(report(d, 0, 0))
			continue
		}
		sim.SendReport(report(d, el, 1))
	}
}

var cycle = []uint32{
	protocol.BtnA, protocol.BtnB, protocol.BtnX, protocol.BtnY, protocol.BtnL, protocol.BtnR,
	protocol.BtnUp, protocol.BtnRight, protocol.BtnDown, protocol.BtnLeft, protocol.BtnZL, protocol.BtnZR,
}

func stick(b []byte, x, y float64) {
	rx, ry := 2000+int(x*1400), 2000+int(y*1400)
	v := rx | ry<<12
	b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
}

func report(d *device, t, amp float64) []byte {
	b := make([]byte, 64)
	btn := uint32(0)
	if amp > 0 && math.Mod(t, 0.5) < 0.3 {
		btn = cycle[int(t*2)%len(cycle)]
	}
	x, y := amp*math.Sin(t*1.6)*0.8, amp*math.Cos(t*1.6)*0.8
	gyro := [3]int16{int16(amp * 900 * math.Sin(t)), int16(amp * 600 * math.Cos(t*0.7)), int16(amp * 300 * math.Sin(t*1.3))}
	if d.pid == protocol.NSOGameCubeControllerPID {
		// GameCube layout: b1 B/A/Y/X/R/Z/Start, b2 D-pad/L/ZL, analog triggers.
		switch btn {
		case protocol.BtnA:
			b[2] = 0x02
		case protocol.BtnB:
			b[2] = 0x01
		case protocol.BtnX:
			b[2] = 0x08
		case protocol.BtnY:
			b[2] = 0x04
		case protocol.BtnUp:
			b[3] = 0x08
		case protocol.BtnDown:
			b[3] = 0x01
		}
		stick(b[5:], x, y)
		stick(b[8:], -y, x)
		b[12] = byte(36 + amp*100*(1+math.Sin(t)))
		b[13] = byte(36 + amp*100*(1+math.Cos(t)))
		for i, v := range gyro {
			binary.LittleEndian.PutUint16(b[40+2*i:], uint16(v))
		}
		binary.LittleEndian.PutUint16(b[34+4:], uint16(0xFFFF&-4096))
		return b
	}
	binary.LittleEndian.PutUint32(b[4:], btn)
	stick(b[10:], x, y)
	stick(b[13:], -y, x)
	binary.LittleEndian.PutUint16(b[31:], uint16(d.voltage*1000))
	binary.LittleEndian.PutUint16(b[52:], uint16(0xFFFF&-4096)) // gravity on raw Z
	for i, v := range gyro {
		binary.LittleEndian.PutUint16(b[54+2*i:], uint16(v))
	}
	return b
}
