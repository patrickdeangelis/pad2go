"""Stub Windows-only modules so the original Switch2Connect sources import on any OS.

Only the pure-Python hot paths (report parsing, calibration, rumble encoding,
CemuHook packets) are exercised by the benchmarks; stubs are never called there.
"""
import importlib.abc
import importlib.machinery
import os
import sys
import types
from unittest import mock

STUBBED = {
    "bleak", "bleak.backends", "bleak.backends.device", "bleak.backends.characteristic",
    "bleak.backends.scanner", "bleak.exc", "bluetooth", "win32api", "win32con", "win32gui",
    "win32process", "win32event", "winerror", "pywintypes", "winreg", "imufusion", "comtypes",
    "vgamepad", "hid", "usb", "usb.core", "usb.util", "libusb_package", "pystray", "webview",
    "PIL", "PIL.Image", "serial", "serial.tools", "serial.tools.list_ports", "winrt",
}


class _Finder(importlib.abc.MetaPathFinder, importlib.abc.Loader):
    def find_spec(self, name, path, target=None):
        root = name.split(".")[0]
        if name in STUBBED or root in STUBBED or root.startswith("winrt") or root.startswith("win32"):
            return importlib.machinery.ModuleSpec(name, self, is_package=True)
        return None

    def create_module(self, spec):
        m = mock.MagicMock(name=spec.name)
        m.__path__ = []
        m.__spec__ = spec
        return m

    def exec_module(self, module):
        pass


def install(src_dir):
    sys.meta_path.insert(0, _Finder())
    # ctypes.windll / WinDLL don't exist off Windows.
    import ctypes
    if not hasattr(ctypes, "windll"):
        ctypes.windll = mock.MagicMock()
        ctypes.WinDLL = mock.MagicMock()
        ctypes.WINFUNCTYPE = ctypes.CFUNCTYPE
        ctypes.wintypes = mock.MagicMock()
        sys.modules.setdefault("ctypes.wintypes", ctypes.wintypes)
    os.environ.setdefault("APPDATA", "/tmp")
    sys.path.insert(0, src_dir)
