// Throwaway spike: physical DualSense touchpad or synthetic swipe -> DSU motion.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/dualsense"
	"github.com/patrickdeangelis/pad2go/internal/hid"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:26761", "separate DSU endpoint for Eden motion")
	live := flag.Bool("live", false, "read physical DualSense instead of synthetic swipe")
	list := flag.Bool("list", false, "list visible DualSense devices and exit")
	duration := flag.Duration("duration", 30*time.Second, "maximum run time")
	gestureFile := flag.String("gesture-file", "", "read synthetic contact from file: active(0/1) x y; overrides automatic swipe")
	flag.Parse()
	if *duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	backend := hid.DefaultBackend()
	var transport dualsense.Transport
	var bluetooth bool
	if *live || *list {
		infos, err := backend.List()
		if err != nil {
			return err
		}
		count := 0
		for _, info := range infos {
			if info.VendorID != dualsense.VendorID || info.ProductID != dualsense.ProductID {
				continue
			}
			count++
			fmt.Printf("DualSense bluetooth=%v\n", info.Bluetooth)
			if *list || transport != nil {
				continue
			}
			transport, err = backend.Open(info)
			if err != nil {
				return err
			}
			bluetooth = info.Bluetooth
		}
		if *list {
			fmt.Printf("devices=%d\n", count)
			return nil
		}
		if transport == nil {
			return fmt.Errorf("no visible DualSense; connect it or check HidHide visibility")
		}
		defer transport.Close()
		if bluetooth {
			if identity, ok := transport.(interface{ MAC() string }); ok {
				_ = identity.MAC()
			}
		}
	}
	s, err := dsu.Listen(*addr, nil)
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Printf("SPIKE DSU %s; live=%v; expires in %s\n", s.Addr(), *live, *duration)
	inputs := make(chan protocol.Input, 1)
	failures := make(chan error, 1)
	if transport != nil {
		go func() {
			buf := make([]byte, 128)
			for {
				n, err := transport.Read(buf)
				if err != nil {
					failures <- err
					return
				}
				if n < 0 || n > len(buf) {
					continue
				}
				in, ok := dualsense.Parse(buf[:n])
				if bluetooth {
					in, ok = dualsense.ParseBluetooth(buf[:n])
				}
				if ok {
					select {
					case inputs <- in:
					default:
					}
				}
			}
		}()
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(*duration)
	defer deadline.Stop()
	c := converter{}
	start, lastInput := time.Now(), time.Time{}
	var latest protocol.Input
	var reports, received, touches, moving int
	for {
		select {
		case <-stop:
			return nil
		case <-deadline.C:
			fmt.Printf("published=%d received=%d touch_reports=%d moving_ticks=%d subscribers=%d\n", reports, received, touches, moving, s.Clients())
			return nil
		case err := <-failures:
			return err
		case in := <-inputs:
			latest, lastInput = in, time.Now()
			received++
			if in.Touch[0].Active {
				touches++
			}
		case now := <-ticker.C:
			if *gestureFile != "" {
				data, e := os.ReadFile(*gestureFile)
				var active, x, y int
				latest.Touch[0] = protocol.TouchContact{}
				if e == nil {
					if n, _ := fmt.Sscanf(string(data), "%d %d %d", &active, &x, &y); n == 3 && x >= 0 && x <= 1920 && y >= 0 && y <= 1080 {
						latest.Touch[0] = protocol.TouchContact{Active: active == 1, ID: 1, X: uint16(x), Y: uint16(y)}
					}
				}
			} else if !*live { // Back-and-forth horizontal swipe; then release, repeat.
				phase := now.Sub(start).Seconds()
				phase -= float64(int(phase/4)) * 4
				x := 100 + phase*800
				if phase > 1 {
					x = 900 - (phase-1)*800
				}
				if x < 100 {
					x = 100
				}
				latest.Touch[0] = protocol.TouchContact{Active: phase < 2, ID: 1, X: uint16(x), Y: 500}
			} else if now.Sub(lastInput) > 100*time.Millisecond {
				latest.Touch = [2]protocol.TouchContact{}
			}
			motion := c.sample(latest.Touch, now)
			if motion.Gyro != [3]float32{} {
				moving++
			}
			s.Publish(dsu.Pad{MAC: [6]byte{2, 0, 0, 0, 0, 99}, Model: dsu.ModelDS4, Battery: 5,
				Touch: latest.Touch, Gyro: motion.Gyro, Accel: motion.Accel})
			reports++
		}
	}
}
