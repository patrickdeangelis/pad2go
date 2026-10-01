//go:build nobluetooth

// Package ble is built without Bluetooth support (the nobluetooth tag): a
// development build for trying the interface with -demo where CoreBluetooth
// may not be usable.
package ble

import (
	"context"
	"errors"

	"github.com/patrickdeangelis/pad2go/internal/controller"
	"github.com/patrickdeangelis/pad2go/internal/lifecycle"
)

var errDisabled = errors.New("built without Bluetooth support (nobluetooth tag)")

// Adapter is unavailable in this build.
type Adapter struct{}

// Enable always fails in this build.
func Enable() (*Adapter, error) { return nil, errDisabled }

func (*Adapter) HostMAC() (uint64, error) { return 0, errDisabled }

func (*Adapter) Scan(context.Context, func(lifecycle.Advert) bool) (lifecycle.Advert, error) {
	return lifecycle.Advert{}, errDisabled
}

func (*Adapter) Connect(context.Context, lifecycle.Advert, func()) (controller.Transport, error) {
	return nil, errDisabled
}
