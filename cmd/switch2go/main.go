// Command switch2go connects Nintendo Switch 2 Joy-Cons, Pro Controller 2
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
	"syscall"

	"github.com/angelispatrick/switch2go/internal/app"
	"github.com/angelispatrick/switch2go/internal/ble"
	"github.com/angelispatrick/switch2go/internal/config"
	"github.com/angelispatrick/switch2go/internal/dsu"
	"github.com/angelispatrick/switch2go/internal/lifecycle"
	"github.com/angelispatrick/switch2go/internal/protocol"
	"github.com/angelispatrick/switch2go/internal/virtualpad"
)

var version = "dev"

const usage = `Usage: switch2go [flags] [command]

Commands:
  run       connect controllers and expose virtual gamepads (default)
  scan      list nearby Switch 2 controllers without connecting
  init      write a starter config file
  version   print the version

Flags:
`

func main() {
	fs := flag.NewFlagSet("switch2go", flag.ExitOnError)
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
		fmt.Println("switch2go", version)
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
	_, err = ad.Scan(ctx, func(a lifecycle.Advert) bool {
		if !seen[a.Addr] {
			seen[a.Addr] = true
			state := "paired to " + lifecycle.FormatMAC(a.Adv.ReconnectMAC)
			if a.Adv.Pairing() {
				state = "pairing mode"
			}
			fmt.Printf("%-40s %-24s RSSI %4d  %s\n", a.Addr, protocol.ControllerNames[a.Adv.ProductID], a.RSSI, state)
		}
		return false
	})
	return err
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

	radio, err := ble.Enable()
	if err != nil {
		return err
	}
	a := app.New(cfg, backend, dsuServer, log)
	defer a.Close()
	m := &lifecycle.Manager{Radio: radio, App: a, Config: cfg, Log: log}
	return m.Run(ctx)
}
