package dualsense

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

// ErrClosed is returned by Rumble and SetPlayerLEDs after Close.
var ErrClosed = errors.New("dualsense: device closed")

// Device is one opened DualSense. It reads input reports in its own goroutine
// and writes rumble and player LEDs on request.
type Device struct {
	t                         Transport
	id                        string
	log                       *slog.Logger
	bt                        bool
	deadzone                  float64
	calibration               *MotionCalibration
	seq                       byte     // guarded by wmu
	btLEDReady                bool     // guarded by wmu
	lightbar                  [3]byte  // guarded by wmu
	haveLightbar              bool     // guarded by wmu
	playerLEDsOff             bool     // guarded by wmu
	playerSlot                int      // guarded by wmu
	triggerLeft, triggerRight [11]byte // guarded by wmu
	haveTriggers              bool     // guarded by wmu
	validReports              atomic.Uint64
	invalidReports            atomic.Uint64
	lastValid                 atomic.Int64

	// wmu serializes writes so the motor and LED state written with each
	// report is the state that was last set.
	wmu sync.Mutex

	mu          sync.Mutex // guards the fields below
	cb          func(protocol.Input)
	closed      bool
	haveLEDs    bool
	leds        byte
	left, right uint8 // last motor values, so an LED update doesn't stop rumble

	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
}

// NewDevice wraps an opened transport and starts reading its input reports.
// id is the stable identifier returned by Address. A nil log discards logs.
func NewDevice(t Transport, id string, log *slog.Logger, deadzone ...float64) *Device {
	return newDevice(t, id, log, false, deadzone)
}

// NewBluetoothDevice creates a device using full Bluetooth report framing.
func NewBluetoothDevice(t Transport, id string, log *slog.Logger, deadzone ...float64) *Device {
	return newDevice(t, id, log, true, deadzone)
}

func newDevice(t Transport, id string, log *slog.Logger, bluetooth bool, configured []float64) *Device {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dz := stickDeadzone
	if len(configured) > 0 {
		dz = math.Max(0, math.Min(0.5, configured[0]))
	}
	d := &Device{t: t, id: id, log: log, bt: bluetooth, deadzone: dz, done: make(chan struct{})}
	if features, ok := t.(interface {
		Feature(byte, int) ([]byte, error)
	}); ok {
		data, err := features.Feature(0x05, 41)
		if err == nil {
			var calibration MotionCalibration
			calibration, err = ParseMotionCalibration(data, bluetooth)
			if err == nil {
				d.calibration = &calibration
			}
		}
		if err != nil {
			log.Warn("dualsense: motion calibration unavailable", "id", id, "err", err)
		}
	}
	go d.readLoop()
	return d
}

// Address returns the device's stable identifier.
func (d *Device) Address() string { return d.id }

// Kind returns protocol.KindDualSense.
func (d *Device) Kind() protocol.Kind { return protocol.KindDualSense }

// Name returns "DualSense".
func (d *Device) Name() string { return "DualSense" }

// TransportName identifies the active physical link for the interface.
func (d *Device) TransportName() string {
	if d.bt {
		return "Bluetooth"
	}
	return "USB"
}

// Diagnostics returns observed reports since this HID connection opened.
func (d *Device) Diagnostics() protocol.HIDDiagnostics {
	age := int64(-1)
	if last := d.lastValid.Load(); last != 0 {
		age = max(0, time.Since(time.Unix(0, last)).Milliseconds())
	}
	return protocol.HIDDiagnostics{Valid: d.validReports.Load(), Invalid: d.invalidReports.Load(), LastValidMillis: age, Calibrated: d.calibration != nil}
}

// OnInput sets the callback for each valid input report. It is called from the
// read goroutine.
func (d *Device) OnInput(f func(protocol.Input)) {
	d.mu.Lock()
	d.cb = f
	d.mu.Unlock()
}

// Done is closed when the transport read fails (the device was unplugged) or
// after Close.
func (d *Device) Done() <-chan struct{} { return d.done }

// Closed reports whether Close was called. It stays false if the device was
// unplugged without a Close.
func (d *Device) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// SetPlayerLEDs lights the player LEDs for slot player (1..8).
func (d *Device) SetPlayerLEDs(ctx context.Context, player int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.Closed() {
		return ErrClosed
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	// Close can run after the first closed check but before this write lock.
	if d.Closed() {
		return ErrClosed
	}
	if d.bt && !d.btLEDReady {
		reset := bluetoothResetLEDs(d.seq)
		d.seq = (d.seq + 1) & 0x0f
		if err := d.t.Write(reset[:]); err != nil {
			return err
		}
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		d.btLEDReady = true
	}
	led := PlayerLEDs(player)
	d.playerSlot = player
	if d.playerLEDsOff {
		led = 0
	}
	d.mu.Lock()
	left, right := d.left, d.right
	d.mu.Unlock()
	if err := d.writeOutput(left, right, led, true); err != nil {
		return err
	}
	d.mu.Lock()
	d.leds, d.haveLEDs = led, true
	d.mu.Unlock()
	return nil
}

// SetLighting sets lightbar RGB, brightness (0-100), and player indicators.
// Call after SetPlayerLEDs, which releases the Bluetooth startup animation.
func (d *Device) SetLighting(rgb [3]byte, brightness byte, playerLEDsOff bool) error {
	if brightness > 100 {
		return errors.New("dualsense: brightness must be 0-100")
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.Closed() {
		return ErrClosed
	}
	for i, c := range rgb {
		d.lightbar[i] = byte((int(c)*int(brightness) + 50) / 100)
	}
	d.haveLightbar, d.playerLEDsOff = true, playerLEDsOff
	d.mu.Lock()
	left, right, have := d.left, d.right, d.haveLEDs
	leds := PlayerLEDs(d.playerSlot)
	if playerLEDsOff {
		leds = 0
	}
	d.leds = leds
	d.mu.Unlock()
	return d.writeOutput(left, right, leds, have)
}

// Rumble drives the motors from a HD-rumble frame. It inverts protocol.FromMotors
// at the default strength, so the frame's amplitudes map back to the 0-255 motor
// values they came from.
func (d *Device) Rumble(v protocol.Vibration) error {
	if d.Closed() {
		return ErrClosed
	}
	return d.setMotors(motor(v.LFAmp), motor(v.HFAmp))
}

// Close stops the motors and turns off player LEDs, then closes the
// transport. It is safe to call more than once.
func (d *Device) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.mu.Unlock()
		d.wmu.Lock()
		if d.haveTriggers {
			d.triggerLeft, _ = triggerPreset("off")
			d.triggerRight, _ = triggerPreset("off")
		}
		d.mu.Lock()
		haveLEDs := d.haveLEDs
		d.left, d.right, d.leds = 0, 0, 0
		d.mu.Unlock()
		err := d.writeOutput(0, 0, 0, haveLEDs)
		d.wmu.Unlock()
		if err != nil {
			d.log.Debug("dualsense: stop output on close", "id", d.id, "err", err)
		}
		d.closeErr = d.t.Close()
		d.finish()
	})
	return d.closeErr
}

// setMotors writes one output report with the given motor values and the last
// LED value, if any.
func (d *Device) setMotors(left, right uint8) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.Closed() {
		return ErrClosed
	}
	d.mu.Lock()
	d.left, d.right = left, right
	leds, haveLEDs := d.leds, d.haveLEDs
	d.mu.Unlock()
	return d.writeOutput(left, right, leds, haveLEDs)
}

// writeOutput is called with wmu held.
func (d *Device) writeOutput(left, right, leds uint8, haveLEDs bool) error {
	if d.bt {
		r := bluetoothOutputReport(left, right, leds, haveLEDs, d.seq)
		if d.haveTriggers {
			r[3] |= 0x0c
			copy(r[13:24], d.triggerRight[:])
			copy(r[24:35], d.triggerLeft[:])
		}
		if d.haveLightbar {
			r[outFlag1+2] |= 0x04
			copy(r[47:50], d.lightbar[:])
		}
		if d.haveLightbar || d.haveTriggers {
			binary.LittleEndian.PutUint32(r[74:], bluetoothCRC(0xa2, r[:74]))
		}
		d.seq = (d.seq + 1) & 0x0f
		return d.t.Write(r[:])
	}
	r := outputReport(left, right, leds, haveLEDs)
	if d.haveTriggers {
		r[1] |= 0x0c
		copy(r[11:22], d.triggerRight[:])
		copy(r[22:33], d.triggerLeft[:])
	}
	if d.haveLightbar {
		r[outFlag1] |= 0x04
		copy(r[45:48], d.lightbar[:])
	}
	return d.t.Write(r[:])
}

// motor converts a 10-bit HD-rumble amplitude to a 0-255 motor value.
func motor(amp uint16) uint8 {
	return uint8(min(255, math.Round(float64(amp)*256/800)))
}

func (d *Device) readLoop() {
	defer d.finish()
	// A failed read can mean the controller disappeared without an explicit
	// Device.Close; release the underlying HID handle in that case as well.
	defer d.t.Close()
	size := InputReportLen
	if d.bt {
		size = bluetoothReportLen
	}
	buf := make([]byte, size)
	for {
		n, err := d.t.Read(buf)
		if err != nil {
			if !d.Closed() {
				d.log.Debug("dualsense: read ended", "id", d.id, "err", err)
			}
			return
		}
		var in protocol.Input
		var ok bool
		if d.bt {
			in, ok = parseBluetoothWithDeadzone(buf[:n], d.deadzone)
		} else {
			in, ok = parseWithDeadzone(buf[:n], d.deadzone)
		}
		if !ok {
			d.invalidReports.Add(1)
			continue
		}
		d.validReports.Add(1)
		d.lastValid.Store(time.Now().UnixNano())
		if d.calibration != nil {
			in.DualSenseGyroDPS, in.DualSenseAccelG = d.calibration.Convert(in.DualSenseGyroRaw, in.DualSenseAccelRaw)
			in.DualSenseMotionCalibrated = true
		}
		d.mu.Lock()
		cb := d.cb
		d.mu.Unlock()
		if cb != nil {
			cb(in)
		}
	}
}

func (d *Device) finish() {
	d.doneOnce.Do(func() { close(d.done) })
}
