// Command pad2go connects Nintendo Switch 2 Joy-Cons, Pro Controller 2
// and NSO GameCube controllers over Bluetooth LE and exposes them as virtual
// Xbox 360 controllers, with an optional CemuHook/DSU motion server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/ble"
	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/demo"
	"github.com/patrickdeangelis/pad2go/internal/lifecycle"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/service"
	"github.com/patrickdeangelis/pad2go/internal/ui"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
	"github.com/patrickdeangelis/pad2go/internal/window"
)

var version = "dev"

const usage = `Usage: pad2go [flags] [command]

Commands:
  run       connect controllers and open the Pad2Go window (default)
  scan      list nearby Switch 2 controllers without connecting
  init      write a starter config file
  version   print the version

Flags:
`

func main() {
	fs := flag.NewFlagSet("pad2go", flag.ExitOnError)
	cfgFlag := fs.String("config", "", "path to the YAML config file (default: ./config.yaml if present, else the user config folder)")
	verbose := fs.Bool("v", false, "verbose (debug) logging")
	headless := fs.Bool("headless", false, "run without a window; serve the interface only (see -open)")
	open := fs.Bool("open", false, "with -headless: open the interface in the default browser")
	demo := fs.Bool("demo", false, "simulate controllers instead of using Bluetooth (to try the interface)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	cfgPath, err := configPath(*cfgFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	// Launched from Finder/Explorer there is no console: also log to a file
	// next to the config.
	var out io.Writer = os.Stderr
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(cfgPath), "pad2go.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		defer f.Close()
		out = io.MultiWriter(f, os.Stderr) // file first: a GUI build may have no stderr
	}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch fs.Arg(0) {
	case "", "run":
		err = run(ctx, cfgPath, log, runOptions{headless: *headless, open: *open, demo: *demo})
	case "scan":
		err = scan(ctx)
	case "init":
		err = writeConfig(cfgPath)
	case "version":
		fmt.Println("pad2go", version)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(err.Error())
		os.Exit(1)
	}
}

// configPath resolves the config file: an explicit flag, else ./config.yaml
// when it exists (the original's layout), else <user config dir>/pad2go.
// A GUI launch starts in "/" (macOS) or the install folder, so the working
// directory can't be the default.
func configPath(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return filepath.Abs("config.yaml")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the user config folder: %w (use -config)", err)
	}
	dir = filepath.Join(dir, "pad2go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
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

type runOptions struct {
	headless, open, demo bool
}

func run(ctx context.Context, cfgPath string, log *slog.Logger, o runOptions) error {
	log.Info("pad2go "+version, "config", cfgPath)
	opt := service.Options{
		ConfigPath: cfgPath, Version: version, Log: log, OpenBackend: virtualpad.Open,
		Radio: func() (lifecycle.Radio, error) { return ble.Enable() },
	}
	if o.demo {
		log.Info("demo mode: simulated controllers, no Bluetooth or virtual gamepads")
		opt.Radio = func() (lifecycle.Radio, error) { return demo.NewRadio(), nil }
		opt.OpenBackend = func(string) (virtualpad.Backend, error) { return virtualpad.Null{}, nil }
	}
	svc, err := service.New(opt)
	if err != nil {
		return err
	}

	// The window needs the interface, so a disabled ui_port still serves it
	// on a random local port.
	port := svc.UIPort()
	if port == 0 && !o.headless {
		port = -1
	}
	var url string
	if port != 0 {
		ln, err := ui.Listen(max(port, 0))
		if err != nil && port > 0 {
			if existing := runningInstance(port); existing != "" {
				// Another pad2go owns the controllers: just show its interface.
				if o.headless {
					return fmt.Errorf("pad2go is already running at %s", existing)
				}
				log.Info("pad2go is already running; opening its window", "url", existing)
				win := window.New("Pad2Go", existing, 1100, 860)
				win.Run()
				return nil
			}
			if !o.headless {
				log.Warn("interface port busy; using a random port", "port", port, "err", err)
				ln, err = ui.Listen(0)
			}
		}
		if err != nil {
			return fmt.Errorf("start the interface on port %d: %w (change ui_port in %s)", port, err, cfgPath)
		}
		actual := ln.Addr().(*net.TCPAddr).Port
		srv := &http.Server{Handler: ui.Handler(svc, actual, log), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("interface server stopped", "err", err)
			}
		}()
		defer srv.Close()
		url = fmt.Sprintf("http://127.0.0.1:%d/", actual)
		log.Info("interface", "url", url)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	if o.headless {
		if o.open && url != "" {
			if err := window.OpenBrowser(url); err != nil {
				log.Warn("could not open the browser", "err", err)
			}
		}
		return <-done
	}

	// The native window owns the main thread until it is closed; closing it
	// quits pad2go, and so does Ctrl+C or the service stopping.
	win := window.New("Pad2Go", url, 1100, 860)
	go func() {
		select {
		case <-ctx.Done():
		case err := <-done:
			done <- err
		}
		win.Close()
	}()
	win.Run()
	cancel()
	select {
	case err := <-done:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-time.After(5 * time.Second):
		return errors.New("timed out stopping")
	}
}

// runningInstance returns the interface URL if a pad2go already serves port.
func runningInstance(port int) string {
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(url + "api/state")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var snap struct {
		Platform string `json:"platform"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&snap) != nil || snap.Platform == "" {
		return ""
	}
	return url
}
