#!/usr/bin/env python3
"""Native FFI + HTTP/SSE routing presets, without starting a VPN tunnel.

Run after `cd app && flutter build macos --release`. Uses temporary data and
a loopback provider. Refuses to run alongside a live shared VPN controller.
"""
import ctypes
import json
import os
from pathlib import Path
from queue import Empty, Queue
import socket
import subprocess
import sys
import tempfile
from threading import Event, Lock, Thread
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "app/build/macos/Build/Products/Release/nekkobox.app"


def main():
    # A Go c-shared runtime sees the environment inherited at process startup.
    # Re-exec before loading it, with loopback endpoints already in that env.
    if "HIKKARI_PRESET_SMOKE_PORT" not in os.environ:
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        env = dict(os.environ)
        env["HIKKARI_PRESET_SMOKE_PORT"] = str(port)
        env["HIKKARI_PRESET_CATALOG_URL"] = f"http://127.0.0.1:{port}/catalog"
        env["HIKKARI_PRESET_EVENTS_URL"] = f"http://127.0.0.1:{port}/events"
        os.execve(sys.executable, [sys.executable, str(Path(__file__).resolve())], env)
    try:
        with socket.create_connection(("127.0.0.1", 9090), timeout=0.5):
            raise SystemExit("Stop the existing VPN before running this test (port 9090 is in use).")
    except OSError:
        pass

    lock, stopped, events = Lock(), Event(), Queue()
    version = 3

    class Provider(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream" if self.path == "/events" else "application/json")
            self.end_headers()
            if self.path == "/events":
                try:
                    self.wfile.write(b": connected\n\n")
                    self.wfile.flush()
                    while not stopped.is_set():
                        try:
                            events.get(timeout=0.25)
                        except Empty:
                            continue
                        self.wfile.write(b"event: catalog\ndata: changed\n\n")
                        self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    pass
                return
            with lock:
                current = version
            self.wfile.write(json.dumps({
                "schemaVersion": 1, "version": current,
                "services": [{"id": "steam", "name": "Steam", "icon": "steam",
                              "domains": [f"service{current}.example"], "ips": [],
                              "processes": {"darwin": ["steam_osx"]}, "defaultModes": []}],
            }).encode())

        def log_message(self, *_):
            pass

    provider = ThreadingHTTPServer(("127.0.0.1", int(os.environ["HIKKARI_PRESET_SMOKE_PORT"])), Provider)
    provider.daemon_threads = True
    Thread(target=provider.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{provider.server_port}"
    os.environ["HIKKARI_PRESET_CATALOG_URL"] = base + "/catalog"
    os.environ["HIKKARI_PRESET_EVENTS_URL"] = base + "/events"

    lib = ctypes.CDLL(str(APP / "Contents/Frameworks/libnekkobox.dylib"))
    lib.NekkoboxCall.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
    lib.NekkoboxCall.restype = ctypes.c_void_p
    lib.NekkoboxFree.argtypes = [ctypes.c_void_p]
    lib.NekkoboxShutdown.argtypes = []

    def call(method, args=None):
        pointer = lib.NekkoboxCall(method.encode(), json.dumps(args or {}).encode())
        try:
            result = json.loads(ctypes.string_at(pointer))
        finally:
            lib.NekkoboxFree(pointer)
        assert result["ok"], (method, result.get("error"))
        return result.get("data")

    def wait_version(expected):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            snapshot = call("RoutingPresets")
            if snapshot["catalog"]["version"] == expected:
                return snapshot
            time.sleep(0.05)
        raise AssertionError(f"catalog {expected} did not arrive: {snapshot}")

    try:
        with tempfile.TemporaryDirectory(prefix="hikkari-presets-smoke-") as directory:
            call("Init", {"baseDir": directory})
            try:
                call("AddProfileFromURI", {"uri": "vless://11111111-2222-3333-4444-555555555555@127.0.0.1:8443?security=tls&sni=example.com#Smoke"})
                assert wait_version(3)["syncEnabled"] is True
                call("AddRule", {"mode": "bypass", "category": "domains", "value": "manual.example"})
                call("SetServicePreset", {"mode": "bypass", "id": "steam", "on": True})
                config = Path(directory) / "config.json"
                assert "service3.example" in config.read_text()
                with lock:
                    version = 4
                events.put(None)
                wait_version(4)
                assert "service4.example" in config.read_text() and "service3.example" not in config.read_text()
                assert call("Routing")["bypass"]["domains"] == ["manual.example"]
                subprocess.run([str(APP / "Contents/Resources/sing-box"), "check", "-c", str(config)], cwd=directory, check=True, capture_output=True)
                print("PASS: native FFI applies HTTP/SSE updates to valid sing-box config without changing manual rules")

                call("SetPresetSync", {"on": False})
                with lock:
                    version = 5
                events.put(None)
                call("RefreshRoutingPresets")
                assert call("RoutingPresets")["catalog"]["version"] == 4
                lib.NekkoboxShutdown()
                call("Init", {"baseDir": directory})
                snapshot = call("RoutingPresets")
                assert snapshot["syncEnabled"] is False and snapshot["catalog"]["version"] == 4
                call("SetServicePreset", {"mode": "bypass", "id": "steam", "on": False})
                assert "service4.example" not in config.read_text() and "manual.example" in config.read_text()
                print("PASS: opt-out and choices survive restart; removing a service preserves manual routing")
            finally:
                lib.NekkoboxShutdown()
    finally:
        stopped.set()
        provider.shutdown()
        provider.server_close()


if __name__ == "__main__":
    main()
