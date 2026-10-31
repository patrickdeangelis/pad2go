package hid

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/dualsense"
)

type Info struct {
	Path                string
	VendorID, ProductID uint16
	// Bluetooth marks a device reached over Bluetooth HID.
	Bluetooth bool
}
type Backend interface {
	List() ([]Info, error)
	Open(Info) (dualsense.Transport, error)
}

const DefaultInterval = time.Second

type target interface {
	AddDevice(context.Context, app.Device) error
	Full() bool
	Connected(string) bool
	RemoveDevice(string)
}
type scanned interface {
	Done() <-chan struct{}
	Closed() bool
}

type Scanner struct {
	Backend     Backend
	App         target
	Interval    time.Duration
	Log         *slog.Logger
	Deadzone    float64
	HasDeadzone bool
}

func (s *Scanner) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	active := map[string]app.Device{}
	activeBT := map[string]bool{}
	ignored := map[string]bool{}
	ignoredIDs := map[string]bool{}
	pathIDs := map[string]string{}
	scan := func() {
		infos, err := s.Backend.List()
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("list HID devices", "err", err)
			}
			return
		}
		present := map[string]bool{}
		for _, i := range infos {
			if i.VendorID == dualsense.VendorID && i.ProductID == dualsense.ProductID {
				present[i.Path] = true
			}
		}
		// Release ignored identities whose previously known paths disappeared
		// before inspecting newly appeared paths. Otherwise a replug with a new
		// path can keep its own identity ignored forever.
		for path := range ignored {
			if !present[path] {
				delete(ignored, path)
			}
		}
		for path := range pathIDs {
			if !present[path] {
				delete(pathIDs, path)
			}
		}
		for id := range ignoredIDs {
			found := false
			for _, pathID := range pathIDs {
				if pathID == id {
					found = true
					break
				}
			}
			if !found {
				delete(ignoredIDs, id)
			}
		}
		// Remove closed devices before looking at other links of the same pad.
		for path, d := range active {
			x, ok := d.(scanned)
			if !ok {
				continue
			}
			if x.Closed() && present[path] {
				ignored[path], ignoredIDs[d.Address()] = true, true
				s.App.RemoveDevice(d.Address())
				delete(active, path)
				delete(activeBT, path)
				continue
			}
			select {
			case <-x.Done():
				s.App.RemoveDevice(d.Address())
				delete(active, path)
				delete(activeBT, path)
			default:
			}
		}
		// Inspect USB first when the same physical controller exposes both links.
		slices.SortStableFunc(infos, func(a, b Info) int {
			if a.Bluetooth == b.Bluetooth {
				return 0
			}
			if a.Bluetooth {
				return 1
			}
			return -1
		})
		for _, i := range infos {
			if i.VendorID != dualsense.VendorID || i.ProductID != dualsense.ProductID {
				continue
			}
			if ignored[i.Path] {
				continue
			}
			if _, ok := active[i.Path]; ok {
				continue
			}
			// A new USB link may replace an already attached Bluetooth link,
			// even when every player slot is occupied.
			if s.App.Full() && (i.Bluetooth || len(activeBT) == 0) {
				continue
			}
			tr, e := s.Backend.Open(i)
			if e != nil {
				if s.Log != nil {
					s.Log.Warn("open DualSense", "path", i.Path, "err", e)
				}
				continue
			}
			id := i.Path
			if mac, ok := tr.(interface{ MAC() string }); ok {
				if address := mac.MAC(); address != "" {
					id = address
				}
			}
			// Never merge two unknown identities or expose the same controller
			// twice when transport identity cannot be established.
			if id == i.Path && (i.Bluetooth || len(activeBT) != 0) {
				_ = tr.Close()
				if s.Log != nil {
					s.Log.Warn("cannot identify DualSense transport", "path", i.Path)
				}
				continue
			}
			pathIDs[i.Path] = id
			if ignoredIDs[id] {
				_ = tr.Close()
				continue
			}
			if !i.Bluetooth {
				for path := range activeBT {
					if d := active[path]; d != nil && d.Address() == id {
						s.App.RemoveDevice(id)
						_ = d.Close()
						delete(active, path)
						delete(activeBT, path)
					}
				}
			}
			if s.App.Connected(id) {
				_ = tr.Close()
				continue
			}
			var dev *dualsense.Device
			var dz []float64
			if s.HasDeadzone {
				dz = []float64{s.Deadzone}
			}
			if i.Bluetooth {
				dev = dualsense.NewBluetoothDevice(tr, id, s.Log, dz...)
			} else {
				dev = dualsense.NewDevice(tr, id, s.Log, dz...)
			}
			if e = s.App.AddDevice(ctx, dev); e != nil {
				_ = dev.Close()
				continue
			}
			active[i.Path] = dev
			if i.Bluetooth {
				activeBT[i.Path] = true
			}
			go func(path string, d app.Device) {
				<-ctx.Done()
				if x, ok := d.(scanned); ok {
					select {
					case <-x.Done():
					case <-ctx.Done():
					}
					if !x.Closed() {
						s.App.RemoveDevice(d.Address())
					}
				}
			}(i.Path, dev)
		}
	}
	scan()
	for {
		select {
		case <-ctx.Done():
			for _, d := range active {
				_ = d.Close()
			}
			return
		case <-ticker.C:
			scan()
		}
	}
}
