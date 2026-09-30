"""Benchmark the original Switch2Connect (Python) hot paths.

Runs the upstream code unmodified (Windows-only imports are stubbed; see
stubs.py). Prints JSON results on stdout. Usage:

    python3 bench.py /path/to/Switch2Connect/src [--dump FILE]
"""
import ctypes
import gc
import json
import os
import random
import socket
import struct
import sys
import threading
import time
import zlib

import stubs

stubs.install(sys.argv[1] if len(sys.argv) > 1 and not sys.argv[1].startswith("-") else "/tmp/s2c-orig/src")

import cemuhook_udp  # noqa: E402
import controller as C  # noqa: E402
import virtual_controller as VC  # noqa: E402
from config import CONFIG  # noqa: E402

N = int(os.environ.get("BENCH_N", "200000"))
SEED = 42


# --- shared inputs (same generator logic as the Go harness) -----------------

def pack_stick(x, y):
    v = x | (y << 12)
    return bytes([v & 0xFF, (v >> 8) & 0xFF, (v >> 16) & 0xFF])


def make_reports(n, gamecube=False):
    rnd = random.Random(SEED)
    out = []
    for i in range(n):
        b = bytearray(64)
        lx, ly = rnd.randrange(600, 3500), rnd.randrange(600, 3500)
        rx, ry = rnd.randrange(600, 3500), rnd.randrange(600, 3500)
        if gamecube:
            b[0] = i & 0xFF
            b[2], b[3], b[4] = rnd.randrange(256) & 0x7F, rnd.randrange(256) & 0x3F, rnd.randrange(256) & 0x13
            b[5:8], b[8:11] = pack_stick(lx, ly), pack_stick(rx, ry)
            b[12], b[13] = rnd.randrange(30, 245), rnd.randrange(30, 245)
            for off in range(34, 46, 2):
                struct.pack_into("<h", b, off, rnd.randrange(-4096, 4096))
        else:
            struct.pack_into("<I", b, 0, i)
            struct.pack_into("<I", b, 4, rnd.getrandbits(26))
            b[10:13], b[13:16] = pack_stick(lx, ly), pack_stick(rx, ry)
            struct.pack_into("<H", b, 31, 3900)
            for off in range(48, 60, 2):
                struct.pack_into("<h", b, off, rnd.randrange(-4096, 4096))
        out.append(bytes(b))
    return out


CAL_BYTES = pack_stick(2048, 2048) + pack_stick(1500, 1500) + pack_stick(1500, 1500)


# --- timing ------------------------------------------------------------------

def percentiles(samples):
    s = sorted(samples)
    n = len(s)
    pick = lambda q: s[min(n - 1, int(q * n))]
    return {
        "n": n, "mean_ns": sum(s) / n, "p50_ns": pick(0.50), "p99_ns": pick(0.99),
        "p999_ns": pick(0.999), "max_ns": s[-1],
    }


def measure(fn, inputs, warmup=0.1):
    pc = time.perf_counter_ns
    for x in inputs[: int(len(inputs) * warmup)]:
        fn(x)
    samples = []
    append = samples.append
    for x in inputs:
        t0 = pc()
        fn(x)
        append(pc() - t0)
    out = percentiles(samples)
    # Whole-loop timing: accurate mean without per-call timer overhead.
    t0 = pc()
    for x in inputs:
        fn(x)
    out["batch_mean_ns"] = (pc() - t0) / len(inputs)
    return out


def timer_overhead():
    pc = time.perf_counter_ns
    samples = []
    for _ in range(N):
        t0 = pc()
        samples.append(pc() - t0)
    return percentiles(samples)


# --- scenarios ---------------------------------------------------------------

def bench_parse():
    cal = C.StickCalibrationData(CAL_BYTES)
    reports = make_reports(N)
    dz = (0.03, 0.03)
    pid = C.PRO_CONTROLLER2_PID
    return measure(lambda d: C.ControllerInputData(d, cal, cal, pid, None, dz), reports)


def bench_parse_gamecube():
    cal = C.make_fixed_stick_calibration()
    reports = make_reports(N, gamecube=True)
    dz = (0.03, 0.03)
    pid = C.NSO_GAMECUBE_CONTROLLER_PID
    gcc = [36, 190, 240, 36, 190, 240]
    return measure(lambda d: C.ControllerInputData(d, cal, cal, pid, gcc, dz), reports)


class XUSB_REPORT(ctypes.Structure):
    _fields_ = [("wButtons", ctypes.c_ushort), ("bLeftTrigger", ctypes.c_ubyte),
                ("bRightTrigger", ctypes.c_ubyte), ("sThumbLX", ctypes.c_short),
                ("sThumbLY", ctypes.c_short), ("sThumbRX", ctypes.c_short),
                ("sThumbRY", ctypes.c_short)]


class FakeVX360:
    """vgamepad.VX360Gamepad's Python-side work, minus the ViGEmClient.dll call."""

    def __init__(self):
        self.report = XUSB_REPORT()

    def left_trigger(self, value):
        self.report.bLeftTrigger = value

    def right_trigger(self, value):
        self.report.bRightTrigger = value

    def left_joystick_float(self, x, y):
        self.report.sThumbLX, self.report.sThumbLY = round(x * 32767), round(y * 32767)

    def right_joystick_float(self, x, y):
        self.report.sThumbRX, self.report.sThumbRY = round(x * 32767), round(y * 32767)

    def update(self):
        pass


class FakeController:
    class _Info:
        product_id = C.PRO_CONTROLLER2_PID

    class _Dev:
        address = "AA:BB:CC:DD:EE:FF"

    controller_info = _Info()
    device = _Dev()
    gyro_mouse_enabled = False

    def is_joycon(self): return False
    def is_joycon_left(self): return False
    def is_joycon_right(self): return False
    def is_pro_controller(self): return True


def make_virtual_controller(ctrl):
    vc = object.__new__(VC.VirtualController)
    vc.state_lock = threading.RLock()
    vc.vg_controller = FakeVX360()
    vc.driver_type = "ViGEmBus"
    vc.controllers = [ctrl]
    vc.hold_mode = "Vertical"
    return vc


def bench_xbox_pipeline():
    """Raw BLE notification -> parsed/calibrated -> Xbox report (as submitted to ViGEm)."""
    cal = C.StickCalibrationData(CAL_BYTES)
    ctrl = FakeController()
    vc = make_virtual_controller(ctrl)
    reports = make_reports(N)
    dz = (0.03, 0.03)
    pid = C.PRO_CONTROLLER2_PID

    def step(d):
        inp = C.ControllerInputData(d, cal, cal, pid, None, dz)
        vc.update_as_xbox(inp, inp.buttons & 0x03FFFFFF, ctrl, None)

    return measure(step, reports)


def bench_rumble_encode():
    rnd = random.Random(SEED)
    frames = [(C.VibrationData(lf_amp=rnd.randrange(1024), hf_amp=rnd.randrange(1024)),
               C.VibrationData(lf_amp=rnd.randrange(1024), hf_amp=rnd.randrange(1024)),
               C.VibrationData(lf_amp=rnd.randrange(1024), hf_amp=rnd.randrange(1024)))
              for _ in range(N)]
    seq = [0]

    # Verbatim encoding from Controller.set_vibration (Pro Controller path).
    def step(f):
        v1, v2, v3 = f
        encode_frame = lambda v: v.get_bytes()
        motor_vibrations = (0x50 + (seq[0] & 0x0F)).to_bytes(1, 'little') + encode_frame(v1) + encode_frame(v2) + encode_frame(v3)
        payload = (b'\x00' + motor_vibrations + motor_vibrations)
        seq[0] += 1
        return payload

    return measure(step, frames)


def dsuc_packet(msg_type, body):
    payload = struct.pack("<I", msg_type) + body
    pkt = bytearray(b"DSUC" + struct.pack("<HHII", 1001, len(payload), 0, 1234) + payload)
    struct.pack_into("<I", pkt, 8, zlib.crc32(pkt) & 0xFFFFFFFF)
    return bytes(pkt)


def bench_dsu_roundtrip():
    """Publish one motion sample and receive it on a loopback UDP client."""
    CONFIG.save_config = lambda *a, **k: None  # don't write the user's config
    srv = cemuhook_udp.CemuHookUDPServer("127.0.0.1", 0)
    srv.start()
    port = srv.sock.getsockname()[1]
    cli = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    cli.settimeout(2.0)
    cli.connect(("127.0.0.1", port))
    cli.send(dsuc_packet(0x100002, bytes(8)))
    deadline = time.time() + 2
    while not srv.clients and time.time() < deadline:
        time.sleep(0.001)

    cal = C.StickCalibrationData(CAL_BYTES)
    inputs = [C.ControllerInputData(d, cal, cal, C.PRO_CONTROLLER2_PID, None, (0.03, 0.03))
              for d in make_reports(min(N, 50000))]
    mac = bytes.fromhex("AABBCCDDEEFF")

    def step(inp):
        srv.report_controller_data(2, mac, 5, inp, inp.accelerometer, inp.gyroscope)
        cli.recv(256)

    try:
        return measure(step, inputs)
    finally:
        srv.stop()


def bench_parallel(threads=4):
    """Total pipeline throughput with one thread per controller (the GIL serializes them)."""
    per = N // threads
    cal = C.StickCalibrationData(CAL_BYTES)
    reports = make_reports(per)
    pid = C.PRO_CONTROLLER2_PID

    def worker():
        ctrl = FakeController()
        vc = make_virtual_controller(ctrl)
        for d in reports:
            inp = C.ControllerInputData(d, cal, cal, pid, None, (0.03, 0.03))
            vc.update_as_xbox(inp, inp.buttons & 0x03FFFFFF, ctrl, None)

    ts = [threading.Thread(target=worker) for _ in range(threads)]
    t0 = time.perf_counter_ns()
    for t in ts:
        t.start()
    for t in ts:
        t.join()
    elapsed = time.perf_counter_ns() - t0
    return {"threads": threads, "reports": per * threads, "elapsed_ns": elapsed,
            "reports_per_sec": per * threads / (elapsed / 1e9)}


def dump(path, count=2000):
    """Write "hex wButtons LT RT LX LY RX RY" per report for `bench/go verify`."""
    cal = C.StickCalibrationData(CAL_BYTES)
    ctrl = FakeController()
    vc = make_virtual_controller(ctrl)
    with open(path, "w") as f:
        for d in make_reports(count):
            inp = C.ControllerInputData(d, cal, cal, C.PRO_CONTROLLER2_PID, None, (0.03, 0.03))
            vc.update_as_xbox(inp, inp.buttons & 0x03FFFFFF, ctrl, None)
            r = vc.vg_controller.report
            f.write(f"{d.hex()} {r.wButtons:04x} {r.bLeftTrigger} {r.bRightTrigger} "
                    f"{r.sThumbLX} {r.sThumbLY} {r.sThumbRX} {r.sThumbRY}\n")


def main():
    if "--dump" in sys.argv:
        dump(sys.argv[sys.argv.index("--dump") + 1])
        return
    gc.collect()
    results = {
        "impl": "python",
        "version": sys.version.split()[0],
        "timer_overhead": timer_overhead(),
        "parse_pro": bench_parse(),
        "parse_gamecube": bench_parse_gamecube(),
        "xbox_pipeline": bench_xbox_pipeline(),
        "rumble_encode": bench_rumble_encode(),
        "dsu_roundtrip": bench_dsu_roundtrip(),
        "parallel_4": bench_parallel(4),
    }
    json.dump(results, sys.stdout, indent=1)


if __name__ == "__main__":
    main()
