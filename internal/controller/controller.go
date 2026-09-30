// Package controller drives a single Switch 2 controller over a GATT
// transport: initialization, calibration, pairing, LEDs, rumble and input.
package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/angelispatrick/switch2go/internal/protocol"
)

// Transport is the minimal GATT client a controller needs. Characteristics are
// addressed by lower-case UUID string.
type Transport interface {
	// Address is a stable identifier for the device (MAC, or a UUID on macOS).
	Address() string
	Write(uuid string, data []byte, withResponse bool) error
	Subscribe(uuid string, fn func([]byte)) error
	Disconnect() error
}

// CommandTimeout bounds how long a command waits for its response.
var CommandTimeout = 2 * time.Second

// Controller is a connected, initialized Switch 2 controller.
type Controller struct {
	t   Transport
	log *slog.Logger

	Info protocol.ControllerInfo
	kind protocol.Kind

	// Stick calibration per side; nil when the controller has no such stick.
	leftCal, rightCal *protocol.StickCalibration
	deadzone          float64

	cmdMu    sync.Mutex // serializes commands
	respMu   sync.Mutex
	expected byte
	respCh   chan []byte

	rumbleMu  sync.Mutex
	rumbleSeq uint8

	settleMu       sync.Mutex
	settled        bool
	settleDeadline time.Time

	inputMu sync.RWMutex
	onInput func(protocol.Input)
	parse   protocol.ParseOptions
}

// Options configures Initialize.
type Options struct {
	// ProductID from the advertisement; used to pick the init sequence before
	// controller info is read.
	AdvertisedPID uint16
	// GameCube trigger decoding.
	GCTriggerMode        string
	GCTriggerCalibration []int
	// Deadzone returns the radial stick deadzone (0-1) for a controller kind.
	// Nil means 3%.
	Deadzone func(protocol.Kind) float64
}

// New wraps a connected transport. Call Initialize before use.
func New(t Transport, log *slog.Logger) *Controller {
	if log == nil {
		log = slog.Default()
	}
	return &Controller{t: t, log: log.With("addr", t.Address())}
}

// Address returns the transport address.
func (c *Controller) Address() string { return c.t.Address() }

// Kind returns the controller family.
func (c *Controller) Kind() protocol.Kind { return c.kind }

// Name returns a human-readable model name.
func (c *Controller) Name() string {
	if n, ok := protocol.ControllerNames[c.Info.ProductID]; ok {
		return n
	}
	return fmt.Sprintf("Unknown (0x%04x)", c.Info.ProductID)
}

func (c *Controller) onResponse(data []byte) {
	c.respMu.Lock()
	ch, expected := c.respCh, c.expected
	c.respMu.Unlock()
	if ch == nil || len(data) == 0 || data[0] != expected {
		return
	}
	select {
	case ch <- append([]byte(nil), data...):
	default:
	}
}

// command sends a command and waits for its response payload.
func (c *Controller) command(ctx context.Context, cmd, sub byte, data []byte) ([]byte, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	ch := make(chan []byte, 1)
	c.respMu.Lock()
	c.expected, c.respCh = cmd, ch
	c.respMu.Unlock()
	defer func() {
		c.respMu.Lock()
		c.respCh = nil
		c.respMu.Unlock()
	}()

	if err := c.t.Write(protocol.CommandWriteUUID, protocol.EncodeCommand(cmd, sub, data), false); err != nil {
		return nil, fmt.Errorf("write command 0x%02x:%02x: %w", cmd, sub, err)
	}
	timer := time.NewTimer(CommandTimeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return protocol.DecodeResponse(cmd, resp)
	case <-timer.C:
		return nil, fmt.Errorf("command 0x%02x:%02x: response timeout", cmd, sub)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readMemory reads up to protocol.MaxMemoryRead bytes from controller memory.
func (c *Controller) readMemory(ctx context.Context, length byte, addr uint32) ([]byte, error) {
	arg, err := protocol.MemoryReadData(length, addr)
	if err != nil {
		return nil, err
	}
	payload, err := c.command(ctx, protocol.CmdMemory, protocol.SubMemoryRead, arg)
	if err != nil {
		return nil, err
	}
	return protocol.ParseMemoryRead(length, addr, payload)
}

// Initialize runs the connection handshake: command channel, SW2 init
// sequence, device info, input mode, stick calibration, input notifications
// and feature enable.
func (c *Controller) Initialize(ctx context.Context, opt Options) error {
	c.parse = protocol.ParseOptions{GCTriggerMode: opt.GCTriggerMode, GCTriggerCalibration: opt.GCTriggerCalibration}

	var err error
	for attempt := range 3 {
		if err = c.t.Subscribe(protocol.CommandResponseUUID, c.onResponse); err == nil {
			break
		}
		c.log.Warn("command notify failed, retrying", "attempt", attempt+1, "err", err)
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	if err != nil {
		return fmt.Errorf("enable command notifications: %w", err)
	}

	fails := 0
	for _, cmd := range protocol.InitSequenceFor(opt.AdvertisedPID) {
		if _, err := c.command(ctx, cmd.Cmd, cmd.Sub, cmd.Data); err != nil {
			fails++
			c.log.Warn("init command failed", "cmd", fmt.Sprintf("%02x:%02x", cmd.Cmd, cmd.Sub), "err", err)
			if fails >= 3 {
				return fmt.Errorf("init aborted after %d consecutive failures: %w", fails, err)
			}
			continue
		}
		fails = 0
	}

	for attempt := 0; ; attempt++ {
		raw, err := c.readMemory(ctx, 0x40, protocol.AddrControllerInfo)
		if err == nil {
			c.Info, err = protocol.ParseControllerInfo(raw)
		}
		if err == nil {
			break
		}
		if attempt == 2 {
			return fmt.Errorf("read controller info: %w", err)
		}
		c.log.Warn("read controller info failed, retrying", "err", err)
		if !sleepCtx(ctx, 500*time.Millisecond) {
			return ctx.Err()
		}
	}
	c.parse.ProductID = c.Info.ProductID
	c.kind = protocol.KindOf(c.Info.ProductID)
	c.deadzone = 0.03
	if opt.Deadzone != nil {
		c.deadzone = opt.Deadzone(c.kind)
	}

	if c.kind == protocol.KindGameCube || c.kind.IsJoyCon() {
		if err := c.t.Write(protocol.CommandWriteUUID, protocol.SetInputMode30, false); err != nil {
			c.log.Warn("set input mode 0x30 failed", "err", err)
		}
	}

	if err := c.readCalibration(ctx); err != nil {
		c.log.Warn("stick calibration read failed; using centered defaults", "err", err)
		def := protocol.DefaultStickCalibration()
		c.leftCal, c.rightCal = &def, &def
		if c.kind == protocol.KindJoyConRight {
			c.leftCal = nil
		}
		if c.kind == protocol.KindJoyConLeft {
			c.rightCal = nil
		}
	}

	c.settleMu.Lock()
	c.settled, c.settleDeadline = false, time.Now().Add(time.Second)
	c.settleMu.Unlock()
	if err := c.t.Subscribe(protocol.InputReportUUID, c.onReport); err != nil {
		return fmt.Errorf("enable input notifications: %w", err)
	}

	if c.kind == protocol.KindGameCube {
		if err := c.enableFeatures(ctx, 0x27); err != nil {
			c.log.Warn("enable features failed", "err", err)
		}
	}
	c.log.Info("controller initialized", "model", c.Name(), "serial", c.Info.SerialNumber)
	return nil
}

func (c *Controller) enableFeatures(ctx context.Context, flags byte) error {
	if _, err := c.command(ctx, protocol.CmdFeature, protocol.SubFeatureInit, protocol.PadFeature(flags)); err != nil {
		return err
	}
	if c.kind == protocol.KindGameCube {
		for _, pkt := range protocol.GameCubeIMUInit {
			if err := c.t.Write(protocol.CommandWriteUUID, pkt, false); err != nil {
				c.log.Warn("GameCube IMU init write failed", "err", err)
				break
			}
			sleepCtx(ctx, 50*time.Millisecond)
		}
	}
	_, err := c.command(ctx, protocol.CmdFeature, protocol.SubFeatureEnable, protocol.PadFeature(flags))
	return err
}

func (c *Controller) readCalibrationAt(ctx context.Context, user, factory uint32) ([]byte, error) {
	b, err := c.readMemory(ctx, 0x0b, user)
	if err != nil {
		return nil, err
	}
	if len(b) >= 3 && b[0] == 0xFF && b[1] == 0xFF && b[2] == 0xFF {
		return c.readMemory(ctx, 0x0b, factory)
	}
	return b, nil
}

func (c *Controller) readCalibration(ctx context.Context) error {
	b1, err := c.readCalibrationAt(ctx, protocol.AddrUserCalibrationJoystick1, protocol.AddrCalibrationJoystick1)
	if err != nil {
		return err
	}
	cal1 := protocol.ParseStickCalibration(b1)
	switch c.kind {
	case protocol.KindJoyConLeft:
		c.leftCal = &cal1
		return nil
	case protocol.KindJoyConRight:
		c.rightCal = &cal1
		return nil
	}
	b2, err := c.readCalibrationAt(ctx, protocol.AddrUserCalibrationJoystick2, protocol.AddrCalibrationJoystick2)
	if err != nil {
		return err
	}
	cal2 := protocol.ParseStickCalibration(b2)
	if c.kind == protocol.KindGameCube {
		if !cal1.Valid {
			cal1 = protocol.FixedStickCalibration()
		}
		if !cal2.Valid {
			cal2 = protocol.FixedStickCalibration()
		}
	}
	c.leftCal, c.rightCal = &cal1, &cal2
	return nil
}

// OnInput registers the handler for calibrated input (called from the BLE
// goroutine, one report at a time).
func (c *Controller) OnInput(fn func(protocol.Input)) {
	c.inputMu.Lock()
	c.onInput = fn
	c.inputMu.Unlock()
}

func (c *Controller) onReport(data []byte) {
	r, err := protocol.ParseReport(data, c.parse)
	if err != nil {
		return
	}
	// Settle gate: ignore input until the first neutral frame (or 1 s) so a
	// held wake button or connect-time garbage doesn't fire actions.
	c.settleMu.Lock()
	if !c.settled {
		if r.Buttons&protocol.PhysicalButtonMask == 0 || time.Now().After(c.settleDeadline) {
			c.settled = true
		} else {
			c.settleMu.Unlock()
			return
		}
	}
	c.settleMu.Unlock()

	c.inputMu.RLock()
	fn := c.onInput
	c.inputMu.RUnlock()
	if fn != nil {
		fn(c.calibrate(r))
	}
}

// calibrate applies stick calibration, Joy-Con gain and the deadzone.
func (c *Controller) calibrate(r protocol.Report) protocol.Input {
	in := protocol.Input{
		Buttons: r.Buttons, Accel: r.Accel, Gyro: r.Gyro, BatteryVoltage: r.BatteryVoltage,
	}
	gain := 1.0
	if c.kind.IsJoyCon() {
		gain = 1.05
	}
	if c.leftCal != nil {
		in.Left.X, in.Left.Y = c.leftCal.Apply(r.LeftStickRaw[0], r.LeftStickRaw[1], gain, c.deadzone)
	}
	if c.rightCal != nil {
		in.Right.X, in.Right.Y = c.rightCal.Apply(r.RightStickRaw[0], r.RightStickRaw[1], gain, c.deadzone)
	}
	if c.kind == protocol.KindGameCube {
		in.AnalogTriggers = true
		in.LeftTrigger, in.RightTrigger = r.LeftTrigger, r.RightTrigger
	}
	return in
}

// SetPlayerLEDs lights the player indicator (1-8).
func (c *Controller) SetPlayerLEDs(ctx context.Context, player int) error {
	_, err := c.command(ctx, protocol.CmdLEDs, protocol.SubLEDsSetPlayer, protocol.LEDData(player, false))
	return err
}

// Pair bonds the controller to hostMAC so it reconnects on a button press.
func (c *Controller) Pair(ctx context.Context, hostMAC uint64) error {
	steps := []struct {
		sub  byte
		data []byte
	}{
		{protocol.SubPairSetMAC, protocol.PairSetMACData(hostMAC)},
		{protocol.SubPairLTK1, protocol.PairLTK1},
		{protocol.SubPairLTK2, protocol.PairLTK2},
		{protocol.SubPairFinish, []byte{0}},
	}
	for _, s := range steps {
		if _, err := c.command(ctx, protocol.CmdPair, s.sub, s.data); err != nil {
			return fmt.Errorf("pair step %02x: %w", s.sub, err)
		}
	}
	return nil
}

// Rumble writes one rumble packet. The same frame is used for all three slots.
func (c *Controller) Rumble(v protocol.Vibration) error {
	return c.rumbleFrames([3]protocol.Vibration{v, v, v})
}

// rumbleFrames writes a packet with three distinct 5 ms frames.
func (c *Controller) rumbleFrames(frames [3]protocol.Vibration) error {
	c.rumbleMu.Lock()
	defer c.rumbleMu.Unlock()
	if c.kind == protocol.KindGameCube {
		on := frames[0].LFAmp > 0 || frames[0].HFAmp > 0
		return c.t.Write(protocol.CommandWriteUUID, protocol.GameCubeRumblePayload(on), false)
	}
	if !c.kind.ProLike() {
		for i := range frames {
			frames[i] = frames[i].LimitJoyConAmplitude()
		}
	}
	pkt := protocol.RumblePacket(c.rumbleSeq, frames, c.kind.ProLike())
	c.rumbleSeq++
	return c.t.Write(protocol.VibrationUUID(c.kind), pkt, false)
}

// ConnectHaptics plays the short "connected" feedback.
func (c *Controller) ConnectHaptics(ctx context.Context) {
	for _, step := range protocol.ConnectHaptics {
		if err := c.Rumble(step.Frame); err != nil {
			c.log.Debug("connect haptics failed", "err", err)
			_ = c.Rumble(protocol.SilentVibration())
			return
		}
		if step.HoldMs > 0 && !sleepCtx(ctx, time.Duration(step.HoldMs)*time.Millisecond) {
			_ = c.Rumble(protocol.SilentVibration())
			return
		}
	}
}

// Close disconnects the transport.
func (c *Controller) Close() error {
	c.OnInput(nil)
	return c.t.Disconnect()
}

// ErrNotSupported is returned by transports for unsupported operations.
var ErrNotSupported = errors.New("not supported")

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
