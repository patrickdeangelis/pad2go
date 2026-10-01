// Command bench measures the Go port's hot paths with the same scenarios as
// bench/python/bench.py and prints JSON results.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/config"
	"github.com/patrickdeangelis/pad2go/internal/controller"
	"github.com/patrickdeangelis/pad2go/internal/controller/controllertest"
	"github.com/patrickdeangelis/pad2go/internal/dsu"
	"github.com/patrickdeangelis/pad2go/internal/mapping"
	"github.com/patrickdeangelis/pad2go/internal/protocol"
	"github.com/patrickdeangelis/pad2go/internal/virtualpad"
)

var n = 200000

type stats struct {
	N         int     `json:"n"`
	BatchMean float64 `json:"batch_mean_ns"`
	Mean      float64 `json:"mean_ns"`
	P50       int64   `json:"p50_ns"`
	P99       int64   `json:"p99_ns"`
	P999      int64   `json:"p999_ns"`
	Max       int64   `json:"max_ns"`
}

func percentiles(s []int64) stats {
	slices.Sort(s)
	var sum float64
	for _, v := range s {
		sum += float64(v)
	}
	pick := func(q float64) int64 { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return stats{len(s), 0, sum / float64(len(s)), pick(0.5), pick(0.99), pick(0.999), s[len(s)-1]}
}

func measure[T any](inputs []T, fn func(T)) stats {
	for _, x := range inputs[:len(inputs)/10] {
		fn(x)
	}
	samples := make([]int64, len(inputs))
	for i, x := range inputs {
		t0 := time.Now()
		fn(x)
		samples[i] = int64(time.Since(t0))
	}
	out := percentiles(samples)
	// Whole-loop timing: accurate mean without per-call timer overhead.
	t0 := time.Now()
	for _, x := range inputs {
		fn(x)
	}
	out.BatchMean = float64(time.Since(t0)) / float64(len(inputs))
	return out
}

func packStick(x, y int) []byte {
	v := x | y<<12
	return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
}

func makeReports(count int, gamecube bool) [][]byte {
	rnd := rand.New(rand.NewPCG(42, 42))
	r := func(lo, hi int) int { return lo + rnd.IntN(hi-lo) }
	out := make([][]byte, count)
	for i := range out {
		b := make([]byte, 64)
		lx, ly, rx, ry := r(600, 3500), r(600, 3500), r(600, 3500), r(600, 3500)
		if gamecube {
			b[0] = byte(i)
			b[2], b[3], b[4] = byte(rnd.IntN(256)&0x7F), byte(rnd.IntN(256)&0x3F), byte(rnd.IntN(256)&0x13)
			copy(b[5:], packStick(lx, ly))
			copy(b[8:], packStick(rx, ry))
			b[12], b[13] = byte(r(30, 245)), byte(r(30, 245))
			for off := 34; off < 46; off += 2 {
				binary.LittleEndian.PutUint16(b[off:], uint16(r(-4096, 4096)))
			}
		} else {
			binary.LittleEndian.PutUint32(b[0:], uint32(i))
			binary.LittleEndian.PutUint32(b[4:], rnd.Uint32()&0x03FFFFFF)
			copy(b[10:], packStick(lx, ly))
			copy(b[13:], packStick(rx, ry))
			binary.LittleEndian.PutUint16(b[31:], 3900)
			for off := 48; off < 60; off += 2 {
				binary.LittleEndian.PutUint16(b[off:], uint16(r(-4096, 4096)))
			}
		}
		out[i] = b
	}
	return out
}

var calBytes = append(append(packStick(2048, 2048), packStick(1500, 1500)...), packStick(1500, 1500)...)

// Sinks keep the compiler from eliding the measured work; they are only written.
var (
	sinkF float64 //nolint:unused
	sinkB []byte  //nolint:unused
)

func timerOverhead() stats {
	samples := make([]int64, n)
	for i := range samples {
		t0 := time.Now()
		samples[i] = int64(time.Since(t0))
	}
	return percentiles(samples)
}

func benchParse(gamecube bool) stats {
	pid := uint16(protocol.ProController2PID)
	cal := protocol.ParseStickCalibration(calBytes)
	opt := protocol.ParseOptions{ProductID: pid}
	if gamecube {
		pid = protocol.NSOGameCubeControllerPID
		cal = protocol.FixedStickCalibration()
		opt = protocol.ParseOptions{ProductID: pid, GCTriggerMode: protocol.GCTriggerBump, GCTriggerCalibration: protocol.DefaultGCTriggerCalibration}
	}
	return measure(makeReports(n, gamecube), func(d []byte) {
		r, _ := protocol.ParseReport(d, opt)
		lx, ly := cal.Apply(r.LeftStickRaw[0], r.LeftStickRaw[1], 1, 0.03)
		rx, ry := cal.Apply(r.RightStickRaw[0], r.RightStickRaw[1], 1, 0.03)
		sinkF += lx + ly + rx + ry
	})
}

// newController initializes a real controller against a simulated Pro
// Controller 2 whose stick calibration matches the Python harness.
func newController(addr string) (*controller.Controller, *controllertest.Sim) {
	sim := controllertest.New(addr, protocol.ProController2PID)
	block := append(slices.Clone(calBytes), 0, 0)
	sim.Memory[protocol.AddrCalibrationJoystick1] = block
	sim.Memory[protocol.AddrUserCalibrationJoystick2] = block
	c := controller.New(sim, slog.New(slog.DiscardHandler))
	if err := c.Initialize(context.Background(), controller.Options{AdvertisedPID: protocol.ProController2PID}); err != nil {
		panic(err)
	}
	sim.SendReport(make([]byte, 64)) // neutral frame opens the settle gate
	return c, sim
}

// storeBackend keeps the last Xbox report, like vgamepad's report struct.
type storeBackend struct{}
type storePad struct{ last mapping.XboxState }

func (storeBackend) Name() string                                         { return "store" }
func (storeBackend) Close() error                                         { return nil }
func (storeBackend) NewPad(virtualpad.RumbleFunc) (virtualpad.Pad, error) { return &storePad{}, nil }
func (p *storePad) Update(s mapping.XboxState) error                      { p.last = s; return nil }
func (p *storePad) Close() error                                          { return nil }

func newPipeline(devices int) (*app.App, []*controllertest.Sim) {
	cfg := config.Default()
	cfg.MaxControllers = max(devices, 1)
	a := app.New(cfg, storeBackend{}, nil, slog.New(slog.DiscardHandler))
	sims := make([]*controllertest.Sim, devices)
	for i := range sims {
		var c *controller.Controller
		c, sims[i] = newController("AA:BB:CC:DD:EE:0" + strconv.Itoa(i))
		if err := a.AddDevice(context.Background(), c); err != nil {
			panic(err)
		}
	}
	return a, sims
}

// benchPipeline: raw BLE notification -> parse -> settle gate -> calibrate ->
// map -> Xbox report, through the real controller and app code.
func benchPipeline() stats {
	_, sims := newPipeline(1)
	return measure(makeReports(n, false), sims[0].SendReport)
}

func benchRumble() stats {
	rnd := rand.New(rand.NewPCG(42, 42))
	frames := make([][3]protocol.Vibration, n)
	for i := range frames {
		for j := range 3 {
			frames[i][j] = protocol.Vibration{LFFreq: protocol.DefaultLFFreq, HFFreq: protocol.DefaultHFFreq,
				LFAmp: uint16(rnd.IntN(1024)), HFAmp: uint16(rnd.IntN(1024))}
		}
	}
	var seq uint8
	return measure(frames, func(f [3]protocol.Vibration) {
		sinkB = protocol.RumblePacket(seq, f, true)
		seq++
	})
}

func dsucPacket(msgType uint32, body []byte) []byte {
	payload := binary.LittleEndian.AppendUint32(nil, msgType)
	payload = append(payload, body...)
	pkt := make([]byte, 16+len(payload))
	copy(pkt, "DSUC")
	binary.LittleEndian.PutUint16(pkt[4:], 1001)
	binary.LittleEndian.PutUint16(pkt[6:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(pkt[12:], 1234)
	copy(pkt[16:], payload)
	binary.LittleEndian.PutUint32(pkt[8:], crc32.ChecksumIEEE(pkt))
	return pkt
}

func benchDSU() stats {
	srv, err := dsu.Listen("127.0.0.1:0", nil)
	if err != nil {
		panic(err)
	}
	defer srv.Close()
	cli, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		panic(err)
	}
	defer cli.Close()
	buf := make([]byte, 256)
	if _, err := cli.Write(dsucPacket(0x100002, make([]byte, 8))); err != nil {
		panic(err)
	}
	// Wait until the subscription registers.
	for {
		srv.Publish(dsu.Pad{})
		_ = cli.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		if _, err := cli.Read(buf); err == nil {
			break
		}
	}
	_ = cli.SetReadDeadline(time.Time{})

	cal := protocol.ParseStickCalibration(calBytes)
	opt := protocol.ParseOptions{ProductID: protocol.ProController2PID}
	raw := makeReports(min(n, 50000), false)
	pads := make([]dsu.Pad, len(raw))
	for i, b := range raw {
		r, _ := protocol.ParseReport(b, opt)
		lx, ly := cal.Apply(r.LeftStickRaw[0], r.LeftStickRaw[1], 1, 0.03)
		rx, ry := cal.Apply(r.RightStickRaw[0], r.RightStickRaw[1], 1, 0.03)
		// Motion conversion happens before publishing (outside the timed step).
		var a, g [3]float32
		for j := range 3 {
			a[j], g[j] = float32(r.Accel[j])/4096, float32(r.Gyro[j])*0.061
		}
		pads[i] = dsu.Pad{MAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, Model: dsu.ModelDS4, Battery: 5,
			Buttons: r.Buttons, LX: lx, LY: ly, RX: rx, RY: ry, Accel: a, Gyro: g}
	}
	return measure(pads, func(p dsu.Pad) {
		srv.Publish(p)
		_, _ = cli.Read(buf)
	})
}

type parallelResult struct {
	Threads   int     `json:"threads"`
	Reports   int     `json:"reports"`
	ElapsedNs int64   `json:"elapsed_ns"`
	PerSec    float64 `json:"reports_per_sec"`
}

func benchParallel(workers int) parallelResult {
	per := n / workers
	_, sims := newPipeline(workers)
	reports := makeReports(per, false)
	var wg sync.WaitGroup
	t0 := time.Now()
	for _, sim := range sims {
		wg.Go(func() {
			for _, b := range reports {
				sim.SendReport(b)
			}
		})
	}
	wg.Wait()
	el := time.Since(t0)
	return parallelResult{workers, per * workers, int64(el), float64(per*workers) / el.Seconds()}
}

// verify replays "hex wButtons LT RT LX LY RX RY" lines produced by the
// original Python pipeline and reports any report where the port differs.
func verify(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	pad := &storePad{}
	a := app.New(config.Default(), padBackend{pad}, nil, slog.New(slog.DiscardHandler))
	c, sim := newController("AA:BB:CC:DD:EE:FF")
	if err := a.AddDevice(context.Background(), c); err != nil {
		panic(err)
	}
	total, diffs := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		raw, _ := hex.DecodeString(f[0])
		sim.SendReport(raw)
		want := make([]int64, 7)
		w, _ := strconv.ParseUint(f[1], 16, 16)
		want[0] = int64(w)
		for i := 2; i < 8; i++ {
			want[i-1], _ = strconv.ParseInt(f[i], 10, 32)
		}
		s := pad.last
		got := []int64{int64(s.Buttons), int64(s.LeftTrigger), int64(s.RightTrigger), int64(s.LX), int64(s.LY), int64(s.RX), int64(s.RY)}
		total++
		if !slices.Equal(got, want) {
			if diffs < 5 {
				fmt.Fprintf(os.Stderr, "diff %s\n  python %v\n  go     %v\n", f[0][:40], want, got)
			}
			diffs++
		}
	}
	fmt.Printf("%d reports compared, %d differ\n", total, diffs)
}

type padBackend struct{ p *storePad }

func (b padBackend) Name() string                                         { return "store" }
func (b padBackend) Close() error                                         { return nil }
func (b padBackend) NewPad(virtualpad.RumbleFunc) (virtualpad.Pad, error) { return b.p, nil }

func main() {
	if len(os.Args) == 3 && os.Args[1] == "verify" {
		verify(os.Args[2])
		return
	}
	if v, err := strconv.Atoi(os.Getenv("BENCH_N")); err == nil {
		n = v
	}
	runtime.GC()
	res := map[string]any{
		"impl":           "go",
		"version":        runtime.Version(),
		"timer_overhead": timerOverhead(),
		"parse_pro":      benchParse(false),
		"parse_gamecube": benchParse(true),
		"xbox_pipeline":  benchPipeline(),
		"rumble_encode":  benchRumble(),
		"dsu_roundtrip":  benchDSU(),
		"parallel_4":     benchParallel(4),
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	res["gc"] = map[string]any{"num_gc": ms.NumGC, "pause_total_ns": ms.PauseTotalNs}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(res); err != nil {
		panic(err)
	}
}
