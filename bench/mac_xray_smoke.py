#!/usr/bin/env python3
"""Verify selectable Xray through the built macOS FFI; never start TUN."""
import ctypes
import json
from pathlib import Path
import socket
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / "app/build/macos/Build/Products/Release/nekkobox.app"


def main():
    try:
        with socket.create_connection(("127.0.0.1", 9090), timeout=0.5):
            raise SystemExit("VPN control port is in use; leave the existing VPN untouched.")
    except OSError:
        pass
    resources = APP / "Contents/Resources"
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

    with tempfile.TemporaryDirectory(prefix="hikkari-xray-ffi-") as directory:
        state = Path(directory)
        # Real runtime binaries are also discovered from an installation's data
        # folder. Symlinks avoid copying or modifying any user files.
        for name in ("xray",):
            (state / name).symlink_to(resources / name)
        try:
            must("Init", {"baseDir": directory, "engineId": "xray"})
            info = must("EngineInfo")
            assert info["id"] == "xray"
            assert info["capabilities"]["tun"]
            assert "sing-box" not in info["name"]
            process_rule = call("AddRule", {"mode": "bypass", "category": "processes", "value": "Browser"})
            if info["capabilities"]["processRouting"]:
                assert process_rule["ok"], process_rule
            else:
                assert not process_rule["ok"], "unverified process routing was silently accepted"
            must("AddRule", {"mode": "bypass", "category": "domains", "value": "example.test"})
            for name in ("server", "p-server"):
                must("AddProfileFromURI", {"uri": f"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443?security=tls&sni=example.test#{name}"})
            profiles = must("Profiles")
            assert len(profiles) == 2
            assert all("connection" in p and "config" not in p for p in json.loads((state / "profiles.json").read_text())["profiles"])
            identity = (state / "subscription-device-id").read_text()
            assert not (state / "config.json").exists(), "Xray created another engine's config"
            assert not (state / "sing-box").exists()
            config = json.loads((state / "xray.json").read_text())
            rules = config["routing"]["rules"]
            assert any("domain:example.test" in r.get("domain", []) for r in rules)
            if info["capabilities"]["processRouting"]:
                assert any("Browser" in r.get("process", []) for r in rules)
            assert any(i["protocol"] == "tun" for i in config["inbounds"])
            assert sum(o["protocol"] == "vless" for o in config["outbounds"]) == 2
            result = subprocess.run([str(resources / "xray"), "run", "-test", "-c", str(state / "xray.json")], cwd=directory, capture_output=True, text=True)
            assert result.returncode == 0, result.stderr + result.stdout
            print("PASS: standalone Xray FFI, native TUN/routing config; no sing-box binary/config/API")
            must("SwitchProfile", {"name": "p-server"})
            assert must("ActiveProfile") == "p-server"
            assert must("ExportProfileURI", {"name": "p-server"}).startswith("vless://")
            before = (state / "profiles.json").read_text()
            rejected = call("AddProfileFromURI", {"uri": "vless://id@127.0.0.1:443?type=unsupported"})
            assert not rejected["ok"] and (state / "profiles.json").read_text() == before
            changed = call("Init", {"baseDir": directory, "engineId": "sing-box"})
            assert not changed["ok"], "re-init silently ignored an engine change"
            lib.NekkoboxShutdown()
            must("Init", {"baseDir": directory, "engineId": "sing-box"})
            assert must("EngineInfo")["id"] == "sing-box" and len(must("Profiles")) == 2
            lib.NekkoboxShutdown()
            must("Init", {"baseDir": directory, "engineId": "xray"})
            assert must("ActiveProfile") == "p-server"
            assert "example.test" in must("Routing")["bypass"]["domains"]
            assert (state / "subscription-device-id").read_text() == identity
            must("DeleteProfile", {"name": "p-server"})
            assert must("ActiveProfile") == "server"
            must("DeleteProfile", {"name": "server"})
            assert must("Profiles") == [] and not must("IsRunning")
            for method in ("StartVPN", "RestartVPN"):
                assert not call(method)["ok"]
            print("PASS: common profiles/routing/HWID survive independent engine selection; active/last deletion and unsupported-transport refusal pass")
        finally:
            lib.NekkoboxShutdown()


if __name__ == "__main__":
    main()
