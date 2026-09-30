// Package ble discovers and connects Switch 2 controllers using
// tinygo.org/x/bluetooth (WinRT on Windows, BlueZ on Linux, CoreBluetooth on
// macOS).
package ble

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"tinygo.org/x/bluetooth"

	"github.com/angelispatrick/pad2go/internal/controller"
	"github.com/angelispatrick/pad2go/internal/lifecycle"
	"github.com/angelispatrick/pad2go/internal/protocol"
)

// Adapter wraps the default Bluetooth adapter.
type Adapter struct {
	a *bluetooth.Adapter

	mu             sync.Mutex
	onDisconnected map[string]func()
	// addrs maps scanned address strings to radio addresses for Connect.
	addrs sync.Map
}

// Enable powers up the default adapter.
func Enable() (*Adapter, error) {
	ad := &Adapter{a: bluetooth.DefaultAdapter, onDisconnected: map[string]func(){}}
	ad.a.SetConnectHandler(func(d bluetooth.Device, connected bool) {
		if connected {
			return
		}
		key := d.Address.String()
		ad.mu.Lock()
		fn := ad.onDisconnected[key]
		delete(ad.onDisconnected, key)
		ad.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
	if err := ad.a.Enable(); err != nil {
		return nil, fmt.Errorf("enable Bluetooth adapter: %w", err)
	}
	return ad, nil
}

var _ lifecycle.Radio = (*Adapter)(nil)

// HostMAC returns the adapter's own address as used by the pairing protocol.
func (ad *Adapter) HostMAC() (uint64, error) { return hostMAC(ad.a) }

// Scan scans until accept approves a supported controller, then stops
// scanning and returns it.
func (ad *Adapter) Scan(ctx context.Context, accept func(lifecycle.Advert) bool) (lifecycle.Advert, error) {
	var (
		result lifecycle.Advert
		got    bool
		mu     sync.Mutex
	)
	stop := context.AfterFunc(ctx, func() { _ = ad.a.StopScan() })
	defer stop()
	err := ad.a.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		for _, md := range r.ManufacturerData() {
			if md.CompanyID != protocol.NintendoBLECompanyID {
				continue
			}
			adv, err := protocol.ParseAdvertisement(md.Data)
			if err != nil || !adv.Supported() {
				return
			}
			found := lifecycle.Advert{Addr: r.Address.String(), Adv: adv, RSSI: r.RSSI}
			mu.Lock()
			defer mu.Unlock()
			if got || !accept(found) {
				return
			}
			ad.addrs.Store(found.Addr, r.Address)
			result, got = found, true
			_ = a.StopScan()
			return
		}
	})
	if ctx.Err() != nil {
		return lifecycle.Advert{}, ctx.Err()
	}
	if err != nil {
		return lifecycle.Advert{}, err
	}
	mu.Lock()
	defer mu.Unlock()
	if !got {
		return lifecycle.Advert{}, errors.New("scan stopped")
	}
	return result, nil
}

// Connect opens a GATT connection to a scanned controller and discovers its
// characteristics. onDisconnect runs once when the link drops.
func (ad *Adapter) Connect(_ context.Context, adv lifecycle.Advert, onDisconnect func()) (controller.Transport, error) {
	v, ok := ad.addrs.Load(adv.Addr)
	if !ok {
		return nil, fmt.Errorf("connect %s: not seen while scanning", adv.Addr)
	}
	// Register first so a drop during service discovery is not missed.
	ad.mu.Lock()
	ad.onDisconnected[adv.Addr] = onDisconnect
	ad.mu.Unlock()
	fail := func(err error) (controller.Transport, error) {
		ad.mu.Lock()
		delete(ad.onDisconnected, adv.Addr)
		ad.mu.Unlock()
		return nil, err
	}
	dev, err := ad.a.Connect(v.(bluetooth.Address), bluetooth.ConnectionParams{})
	if err != nil {
		return fail(fmt.Errorf("connect %s: %w", adv.Addr, err))
	}
	t := &transport{ad: ad, dev: dev, addr: adv.Addr, chars: map[string]bluetooth.DeviceCharacteristic{}}
	services, err := dev.DiscoverServices(nil)
	if err != nil {
		_ = dev.Disconnect()
		return fail(fmt.Errorf("discover services: %w", err))
	}
	for _, s := range services {
		chars, err := s.DiscoverCharacteristics(nil)
		if err != nil {
			continue
		}
		for _, c := range chars {
			t.chars[strings.ToLower(c.UUID().String())] = c
		}
	}
	for _, need := range []string{protocol.InputReportUUID, protocol.CommandWriteUUID, protocol.CommandResponseUUID} {
		if _, ok := t.chars[need]; !ok {
			_ = dev.Disconnect()
			return fail(fmt.Errorf("characteristic %s not found; not a Switch 2 controller?", need))
		}
	}
	return t, nil
}

type transport struct {
	ad    *Adapter
	dev   bluetooth.Device
	addr  string
	chars map[string]bluetooth.DeviceCharacteristic
}

func (t *transport) Address() string { return t.addr }

func (t *transport) char(uuid string) (bluetooth.DeviceCharacteristic, error) {
	c, ok := t.chars[uuid]
	if !ok {
		return c, fmt.Errorf("characteristic %s: %w", uuid, controller.ErrNotSupported)
	}
	return c, nil
}

func (t *transport) Write(uuid string, data []byte, withResponse bool) error {
	c, err := t.char(uuid)
	if err != nil {
		return err
	}
	if withResponse {
		_, err = c.Write(data)
	} else {
		_, err = c.WriteWithoutResponse(data)
	}
	return err
}

func (t *transport) Subscribe(uuid string, fn func([]byte)) error {
	c, err := t.char(uuid)
	if err != nil {
		return err
	}
	return c.EnableNotifications(fn)
}

func (t *transport) Disconnect() error {
	t.ad.mu.Lock()
	delete(t.ad.onDisconnected, t.addr)
	t.ad.mu.Unlock()
	return t.dev.Disconnect()
}
