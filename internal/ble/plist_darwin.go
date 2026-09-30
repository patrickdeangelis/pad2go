//go:build darwin && !nobluetooth

package ble

// macOS terminates a process that uses CoreBluetooth without a Bluetooth
// usage description. Embedding an Info.plist in the executable's
// __TEXT,__info_plist section provides one while keeping a single binary.

// #cgo LDFLAGS: -Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/Info.plist
import "C"
