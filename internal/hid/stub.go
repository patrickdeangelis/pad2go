package hid

import "github.com/patrickdeangelis/pad2go/internal/dualsense"

// stubBackend is the backend of platforms without a HID implementation yet
// (Linux and macOS for now): it lists nothing and opens nothing. It has
// no build constraint so every platform can test it.
type stubBackend struct{}

func (stubBackend) List() ([]Info, error)                  { return nil, nil }
func (stubBackend) Open(Info) (dualsense.Transport, error) { return nil, errUnsupported }
