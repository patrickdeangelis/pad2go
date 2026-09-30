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

	"github.com/angelispatrick/switch2connect-go/internal/controller"
	"github.com/angelispatrick/switch2connect-go/internal/protocol"
)

// Adapter wraps the default Bluetooth adapter.
type Adapter struct {
	a *bluetooth.Adapter

	mu             sync.Mutex
	onDisconnected map[string]func()
}

// Found is a supported controller seen while scanning.
type Found struct {
	Address bluetooth.Address
	Adv     protocol.Advertisement
	RSSI    int16
}

// Addr returns the address as a string.
func (f Found) Addr() string { return f.Address.String() }

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

// HostMAC returns the adapter's own address as used by the pairing protocol.
func (ad *Adapter) HostMAC() (uint64, error) { return hostMAC(ad.a) }

// ScanNext scans until accept returns true for a supported controller, then
// stops scanning and returns it.
func (ad *Adapter) ScanNext(ctx context.Context, accept func(Found) bool) (Found, error) {
	var (
		result Found
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
			f := Found{Address: r.Address, Adv: adv, RSSI: r.RSSI}
			mu.Lock()
			defer mu.Unlock()
			if got || !accept(f) {
				return
			}
			result, got = f, true
			_ = a.StopScan()
			return
		}
	})
	if ctx.Err() != nil {
		return Found{}, ctx.Err()
	}
	if err != nil {
		return Found{}, err
	}
	mu.Lock()
	defer mu.Unlock()
	if !got {
		return Found{}, errors.New("scan stopped")
	}
	return result, nil
}

// Connect opens a GATT connection and discovers the controller's characteristics.
// onDisconnect runs once when the link drops.
func (ad *Adapter) Connect(f Found, onDisconnect func()) (controller.Transport, error) {
	dev, err := ad.a.Connect(f.Address, bluetooth.ConnectionParams{})
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", f.Addr(), err)
	}
	t := &transport{ad: ad, dev: dev, addr: f.Addr(), chars: map[string]bluetooth.DeviceCharacteristic{}}
	services, err := dev.DiscoverServices(nil)
	if err != nil {
		_ = dev.Disconnect()
		return nil, fmt.Errorf("discover services: %w", err)
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
			return nil, fmt.Errorf("characteristic %s not found; not a Switch 2 controller?", need)
		}
	}
	ad.mu.Lock()
	ad.onDisconnected[f.Addr()] = onDisconnect
	ad.mu.Unlock()
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
