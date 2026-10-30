#!/usr/bin/env python3
"""Exercise the built macOS FFI bridge against a local subscription provider.

Run after `cd app && flutter build macos --release`.
The test uses disposable profiles and never starts a tunnel or contacts a real
subscription provider. A running VPN on the shared control port aborts the test.
"""
import base64
import ctypes
import json
from pathlib import Path
import socket
import subprocess
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "app/build/macos/Build/Products/Release/nekkobox.app"


def main():
    try:
        with socket.create_connection(("127.0.0.1", 9090), timeout=0.5):
            raise SystemExit("Stop the existing VPN before running this test (port 9090 is in use).")
    except OSError:
        pass

    lib = ctypes.CDLL(str(APP / "Contents/Frameworks/libnekkobox.dylib"))
    lib.NekkoboxCall.argtypes = [ctypes.c_char_p, ctypes.c_char_p]
    lib.NekkoboxCall.restype = ctypes.c_void_p
    lib.NekkoboxFree.argtypes = [ctypes.c_void_p]
    lib.NekkoboxShutdown.argtypes = []

    def call(method, args=None):
        pointer = lib.NekkoboxCall(method.encode(), json.dumps(args or {}).encode())
        try:
            return json.loads(ctypes.string_at(pointer))
        finally:
            lib.NekkoboxFree(pointer)

    def must(method, args=None):
        result = call(method, args)
        assert result["ok"], (method, result.get("error"))
        return result.get("data")

    identities = []

    class Provider(BaseHTTPRequestHandler):
        def do_GET(self):
            identities.append(self.headers.get("X-Hwid"))
            self.send_response(200)
            if self.path == "/blocked":
                self.send_header("X-Hwid-Max-Devices-Reached", "true")
            self.end_headers()
            lines = "\n".join(
                f"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:{8443+i}?security=tls&sni=example.com#Server-{i}"
                for i in range(3)
            )
            self.wfile.write(base64.b64encode(lines.encode()))

        def log_message(self, *_):
            pass

    provider = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    Thread(target=provider.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{provider.server_port}"
    with tempfile.TemporaryDirectory(prefix="hikkari-mac-smoke-") as directory:
        state = Path(directory)
        must("Init", {"baseDir": directory})
        try:
            must("AddProfileFromURI", {"uri": f"[subscription]({url}/servers)"})
            profiles = must("Profiles")
            assert len(profiles) == 3
            print("PASS: native macOS FFI imported all 3 subscription servers")
            blocked = call("AddProfileFromURI", {"uri": url + "/blocked"})
            assert not blocked["ok"] and "лимит устройств" in blocked["error"]
            assert len(must("Profiles")) == 3
            assert len(identities) == 2 and identities[0] and identities[0] == identities[1]
            assert (state / "subscription-device-id").read_text().strip() == identities[0]
            print("PASS: persistent device identity and provider refusal without fake profiles")

            def check_config():
                check = subprocess.run(
                    [str(APP / "Contents/Resources/sing-box"), "check", "-c", str(state / "config.json")],
                    cwd=directory, capture_output=True, text=True,
                )
                assert check.returncode == 0, check.stderr

            check_config()
            must("SwitchProfile", {"name": profiles[0]["name"]})
            assert must("ActiveProfile") == profiles[0]["name"]
            must("SwitchProfile", {"name": profiles[1]["name"]})
            assert must("ActiveProfile") == profiles[1]["name"]
            exported = must("ExportProfileURI", {"name": profiles[1]["name"]})
            assert exported.startswith("vless://")
            must("SwitchProfile", {"name": profiles[0]["name"]})
            print("PASS: profile switching and portable export through native FFI")
            must("DeleteProfile", {"name": profiles[0]["name"]})
            remaining = must("Profiles")
            assert len(remaining) == 2 and remaining[0]["active"]
            check_config()
            print("PASS: active profile deletion selects a remaining server")
            must("SetAutoMode", {"on": True})
            for profile in remaining:
                must("DeleteProfile", {"name": profile["name"]})
            assert must("Profiles") == [] and must("ActiveProfile") == ""
            assert must("AutoMode") is False
            check_config()
            for method in ("StartVPN", "RestartVPN"):
                result = call(method)
                assert not result["ok"] and "добавьте сервер" in result["error"]
            print("PASS: last profile deletion, empty configuration, and refusal to start an empty VPN")

            lib.NekkoboxShutdown()
            legacy = {
                "active": "p-server",
                "profiles": [
                    {"name": name, "label": name, "kind": "outbound", "config": {
                        "type": "vless", "server": "127.0.0.1", "server_port": 443,
                        "uuid": "11111111-2222-3333-4444-555555555555",
                        "connect_timeout": "7s",
                    }} for name in ("server", "p-server")
                ],
            }
            (state / "profiles.json").write_text(json.dumps(legacy))
            (state / "service-uptime-history.json").write_text(json.dumps({
                "p-server|Test": [{"ts": 1, "ok": True, "rtt_ms": 42}],
                "p-p-server|Test": [{"ts": 2, "ok": True, "rtt_ms": 84}],
            }))
            for _ in range(2):
                must("Init", {"baseDir": directory})
                assert must("ActiveProfile") == "p-server"
                history = must("ServiceUptimeHistory")
                assert len(history) == 2
                assert history["server|Test"][0]["rtt_ms"] == 42
                assert history["p-server|Test"][0]["rtt_ms"] == 84
                stored = json.loads((state / "profiles.json").read_text())
                assert all(p["connection"]["uri"] and p["config"]["connect_timeout"] == "7s"
                           for p in stored["profiles"])
                assert (state / "subscription-device-id").read_text().strip() == identities[0]
                check_config()
                lib.NekkoboxShutdown()
            print("PASS: legacy profile options, overlapping history IDs and installation ID survive two restarts")
        finally:
            lib.NekkoboxShutdown()
            provider.shutdown()
            provider.server_close()


if __name__ == "__main__":
    main()
