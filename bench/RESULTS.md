# Performance: Go port vs original Python

Machine: Apple M1 Pro, Darwin 25.4.0. Go go1.26.1, Python 3.12.0. Median of 3 runs, 200,000 reports per scenario.

## Per-report processing time (lower is better)

| Scenario | Python mean | Go mean | Speedup |
| --- | ---: | ---: | ---: |
| Parse + calibrate a Pro Controller 2 report | 6.6 µs | 138 ns | **48×** |
| Parse + calibrate a GameCube report (analog triggers) | 7.4 µs | 211 ns | **35×** |
| **BLE notification → Xbox report** (parse, calibrate, map, fill report) | 12.3 µs | 260 ns | **47×** |
| Encode a rumble packet (3 HD-rumble frames) | 2.0 µs | 63 ns | **32×** |
| DSU: publish motion sample → received by UDP client (loopback) | 20.0 µs | 9.9 µs | **2×** |

## Latency distribution: BLE notification → Xbox report

Timed per call, so it includes the timer overhead (Python ≈38 ns, Go ≈18 ns).

| Percentile | Python | Go |
| --- | ---: | ---: |
| p50 | 12.1 µs | 291 ns |
| p99 | 17.6 µs | 334 ns |
| p99.9 | 60.3 µs | 584 ns |
| max | 220.5 µs | 43.3 µs |

## DSU motion server round trip

| Percentile | Python | Go |
| --- | ---: | ---: |
| p50 | 17.7 µs | 8.5 µs |
| p99 | 49.2 µs | 35.1 µs |
| p99.9 | 96.1 µs | 56.8 µs |
| max | 1.35 ms | 157.8 µs |

## Throughput: 4 controllers in parallel

One thread (Python) or goroutine (Go) per controller, full pipeline.

| | Reports/second |
| --- | ---: |
| Python | 79,210 |
| Go | 13,660,386 |

Go is **172×** higher. Python threads share the GIL, so adding controllers adds no parallelism.

## Startup and memory

| | Start + load code | Peak memory (RSS) |
| --- | ---: | ---: |
| Python (interpreter + importing the app's modules) | 470 ms | 70 MB |
| Go (static binary, 3.5 MB) | < 10 ms | 11 MB |

## How to read this

- **What the numbers mean for a player:** a Switch 2 controller sends a report
  every few milliseconds over BLE, and the radio link adds about 7.5–15 ms
  (connection interval) before either program sees it. The Python pipeline's
  ~12 µs is about 0.1% of that. Both programs add latency far below anything a
  player can feel. The Go port's advantages are CPU headroom, steadier tail
  latency and parallelism, not a visibly lower input delay.
- **The Python figures are a lower bound.** The original's real input callback
  also runs gyro fusion (numpy/imufusion), mouse emulation, profile logic and
  more per report. Only the steps both versions share are timed here, so the
  real gap per report is larger.
- **Same work, same answers:** both pipelines were given 2,000 identical random
  reports, and the Xbox reports they produced matched exactly (see Reproduce).
- **Not measured:** the ViGEmBus driver call (Windows-only, the same driver for
  both), BLE stack time (the same OS stack for both), and rumble end to end.
- The original runs on Windows. Here it runs on macOS with its Windows-only
  imports stubbed out; only pure-Python code is timed.

## Reproduce

```bash
git clone --depth 1 https://github.com/TommyWabg/Switch2Connect /tmp/s2c-orig
```

```bash
bench/run.sh /tmp/s2c-orig/src
```

Equivalence check (Python writes its outputs, Go replays the same bytes):

```bash
(cd bench/python && python3 bench.py /tmp/s2c-orig/src --dump /tmp/py_xbox.txt)
```

```bash
go run ./bench/go verify /tmp/py_xbox.txt
```
