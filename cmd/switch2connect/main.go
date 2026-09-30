// Command switch2connect connects Nintendo Switch 2 Joy-Cons, Pro Controller 2
// and NSO GameCube controllers over Bluetooth LE and exposes them as virtual
// Xbox 360 controllers, with an optional CemuHook/DSU motion server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/angelispatrick/switch2connect-go/internal/app"
	"github.com/angelispatrick/switch2connect-go/internal/ble"
	"github.com/angelispatrick/switch2connect-go/internal/config"
	"github.com/angelispatrick/switch2connect-go/internal/controller"
	"github.com/angelispatrick/switch2connect-go/internal/dsu"
	"github.com/angelispatrick/switch2connect-go/internal/protocol"
	"github.com/angelispatrick/switch2connect-go/internal/virtualpad"
)

var version = "dev"

const usage = `Usage: switch2connect [flags] [command]

Commands:
  run       connect controllers and expose virtual gamepads (default)
  scan      list nearby Switch 2 controllers without connecting
  init      write a starter config file
  version   print the version

Flags:
`

func main() {
	fs := flag.NewFlagSet("switch2connect", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "path to the YAML config file")
	verbose := fs.Bool("v", false, "verbose (debug) logging")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cmd := fs.Arg(0)
	var err error
	switch cmd {
	case "", "run":
		err = run(ctx, *cfgPath, log)
	case "scan":
		err = scan(ctx)
	case "init":
		err = writeConfig(*cfgPath)
	case "version":
		fmt.Println("switch2connect", version)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func writeConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(config.Sample), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

func scan(ctx context.Context) error {
	ad, err := ble.Enable()
	if err != nil {
		return err
	}
	fmt.Println("Scanning for Switch 2 controllers (Ctrl+C to stop)...")
	seen := map[string]bool{}
	_, err = ad.ScanNext(ctx, func(f ble.Found) bool {
		if !seen[f.Addr()] {
			seen[f.Addr()] = true
			state := "paired to " + formatMAC(f.Adv.ReconnectMAC)
			if f.Adv.Pairing() {
				state = "pairing mode"
			}
			fmt.Printf("%-40s %-24s RSSI %4d  %s\n", f.Addr(), protocol.ControllerNames[f.Adv.ProductID], f.RSSI, state)
		}
		return false
	})
	return err
}

func formatMAC(v uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func run(ctx context.Context, cfgPath string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	backend, err := virtualpad.Open(cfg.Output)
	if err != nil {
		return err
	}
	defer backend.Close()
	log.Info("virtual gamepad backend", "backend", backend.Name())

	var dsuServer *dsu.Server
	if cfg.CemuhookEnabled() {
		addr := net.JoinHostPort(cfg.CemuhookHost, strconv.Itoa(cfg.CemuhookPort))
		dsuServer, err = dsu.Listen(addr, log)
		if err != nil {
			return fmt.Errorf("start DSU server: %w", err)
		}
		defer dsuServer.Close()
		log.Info("CemuHook/DSU motion server listening", "addr", dsuServer.Addr())
	}

	ad, err := ble.Enable()
	if err != nil {
		return err
	}

	var host uint64
	hostKnown := false
	if cfg.HostMAC != "" {
		host, _ = config.ParseMAC(cfg.HostMAC)
		hostKnown = true
	} else if host, err = ad.HostMAC(); err == nil {
		hostKnown = true
	} else {
		log.Warn("host Bluetooth address unknown; controllers will not be paired for button-press reconnect", "err", err)
	}
	if hostKnown {
		log.Info("host Bluetooth address", "mac", formatMAC(host))
	}

	a := app.New(cfg, backend, dsuServer, log)
	defer a.Close()

	var warnOnce sync.Map
	accept := func(f ble.Found) bool {
		if a.Connected(f.Addr()) {
			return false
		}
		if f.Adv.Pairing() || !hostKnown || f.Adv.ReconnectMAC == host || cfg.AcceptForeignControllers {
			return true
		}
		if _, dup := warnOnce.LoadOrStore(f.Addr(), true); !dup {
			log.Info("ignoring controller paired to another host (hold SYNC to pair, or set accept_foreign_controllers)",
				"addr", f.Addr(), "model", protocol.ControllerNames[f.Adv.ProductID])
		}
		return false
	}

	log.Info("press a button on a paired controller, or hold SYNC on an unpaired one")
	for ctx.Err() == nil {
		if a.Full() {
			if !sleep(ctx, time.Second) {
				break
			}
			continue
		}
		f, err := ad.ScanNext(ctx, accept)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Warn("scan failed; retrying", "err", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		if err := connect(ctx, ad, a, cfg, f, host, hostKnown, log); err != nil {
			log.Warn("connection failed; press a button or hold SYNC to retry", "addr", f.Addr(), "err", err)
		}
	}
	return ctx.Err()
}

func connect(ctx context.Context, ad *ble.Adapter, a *app.App, cfg *config.Config, f ble.Found, host uint64, hostKnown bool, log *slog.Logger) error {
	log.Info("connecting", "addr", f.Addr(), "model", protocol.ControllerNames[f.Adv.ProductID], "pairing", f.Adv.Pairing())
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	addr := f.Addr()
	t, err := ad.Connect(f, func() {
		log.Info("controller disconnected", "addr", addr)
		a.RemoveDevice(addr)
	})
	if err != nil {
		return err
	}
	c := controller.New(t, log)
	err = c.Initialize(ctx, controller.Options{
		AdvertisedPID:        f.Adv.ProductID,
		GCTriggerMode:        cfg.GCTriggerMode,
		GCTriggerCalibration: cfg.GCTriggerCalibration[addr],
	})
	if err == nil && f.Adv.Pairing() && hostKnown {
		if err = c.Pair(ctx, host); err == nil {
			log.Info("paired; the controller will now reconnect with a button press", "addr", addr)
		}
	}
	if err == nil {
		err = a.AddDevice(context.WithoutCancel(ctx), app.WrapController(c))
	}
	if err != nil {
		_ = c.Close()
		return err
	}
	if cfg.ConnectHaptics {
		go c.ConnectHaptics(context.Background())
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}
