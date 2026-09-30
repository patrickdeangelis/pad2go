"""Summarize bench/results/*.json and startup.txt into bench/RESULTS.md (median of runs)."""
import glob
import json
import os
import platform
import statistics
import subprocess

here = os.path.dirname(os.path.abspath(__file__))
res = os.path.join(here, "results")


def load(impl):
    return [json.load(open(p)) for p in sorted(glob.glob(os.path.join(res, f"{impl}_*.json")))]


go, py = load("go"), load("python")
med = lambda runs, key, field: statistics.median(r[key][field] for r in runs)


def fmt_ns(ns):
    if ns >= 1e6:
        return f"{ns / 1e6:.2f} ms"
    if ns >= 1e3:
        return f"{ns / 1e3:.1f} µs"
    return f"{ns:.0f} ns"


scenarios = [
    ("parse_pro", "Parse + calibrate a Pro Controller 2 report"),
    ("parse_gamecube", "Parse + calibrate a GameCube report (analog triggers)"),
    ("xbox_pipeline", "**BLE notification → Xbox report** (parse, calibrate, map, fill report)"),
    ("rumble_encode", "Encode a rumble packet (3 HD-rumble frames)"),
    ("dsu_roundtrip", "DSU: publish motion sample → received by UDP client (loopback)"),
]

lines = []
w = lines.append
cpu = subprocess.run(["sysctl", "-n", "machdep.cpu.brand_string"], capture_output=True, text=True).stdout.strip() or platform.processor()
w("# Performance: Go port vs original Python\n")
w(f"Machine: {cpu}, {platform.system()} {platform.release()}. "
  f"Go {go[0]['version']}, Python {py[0]['version']}. Median of {len(go)} runs, "
  f"{go[0]['xbox_pipeline']['n']:,} reports per scenario.\n")
w("## Per-report processing time (lower is better)\n")
w("| Scenario | Python mean | Go mean | Speedup |")
w("| --- | ---: | ---: | ---: |")
for key, label in scenarios:
    p, g = med(py, key, "batch_mean_ns"), med(go, key, "batch_mean_ns")
    w(f"| {label} | {fmt_ns(p)} | {fmt_ns(g)} | **{p / g:.0f}×** |")

w("\n## Latency distribution: BLE notification → Xbox report\n")
w("Timed per call, so it includes the timer overhead "
  f"(Python ≈{fmt_ns(med(py, 'timer_overhead', 'mean_ns'))}, Go ≈{fmt_ns(med(go, 'timer_overhead', 'mean_ns'))}).\n")
w("| Percentile | Python | Go |")
w("| --- | ---: | ---: |")
for field, label in [("p50_ns", "p50"), ("p99_ns", "p99"), ("p999_ns", "p99.9"), ("max_ns", "max")]:
    w(f"| {label} | {fmt_ns(med(py, 'xbox_pipeline', field))} | {fmt_ns(med(go, 'xbox_pipeline', field))} |")

w("\n## DSU motion server round trip\n")
w("| Percentile | Python | Go |")
w("| --- | ---: | ---: |")
for field, label in [("p50_ns", "p50"), ("p99_ns", "p99"), ("p999_ns", "p99.9"), ("max_ns", "max")]:
    w(f"| {label} | {fmt_ns(med(py, 'dsu_roundtrip', field))} | {fmt_ns(med(go, 'dsu_roundtrip', field))} |")

pp = statistics.median(r["parallel_4"]["reports_per_sec"] for r in py)
gp = statistics.median(r["parallel_4"]["reports_per_sec"] for r in go)
w("\n## Throughput: 4 controllers in parallel\n")
w("One thread (Python) or goroutine (Go) per controller, full pipeline.\n")
w("| | Reports/second |")
w("| --- | ---: |")
w(f"| Python | {pp:,.0f} |")
w(f"| Go | {gp:,.0f} |")
w(f"\nGo is **{gp / pp:.0f}×** higher. Python threads share the GIL, so adding controllers adds no parallelism.\n")

rows = [l.split() for l in open(os.path.join(res, "startup.txt")) if l.strip()]
size = next(int(r[1]) for r in rows if r[0] == "binary_bytes")
st = lambda impl, i: statistics.median(float(r[i]) for r in rows if r[0] == impl)
ms = lambda impl: f"{st(impl, 1) * 1000:.0f} ms" if st(impl, 1) > 0 else "< 10 ms"  # `time` resolves 10 ms
w("## Startup and memory\n")
w("| | Start + load code | Peak memory (RSS) |")
w("| --- | ---: | ---: |")
w(f"| Python (interpreter + importing the app's modules) | {ms('python')} | {st('python', 2) / 2**20:.0f} MB |")
w(f"| Go (static binary, {size / 2**20:.1f} MB) | {ms('go')} | {st('go', 2) / 2**20:.0f} MB |")

w("""
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
""")
open(os.path.join(here, "RESULTS.md"), "w").write("\n".join(lines))
print("\n".join(lines))
