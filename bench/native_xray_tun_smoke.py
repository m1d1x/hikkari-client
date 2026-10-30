#!/usr/bin/env python3
"""macOS native Xray TUN/process proof, root required, no sing-box involved.

Uses only the benchmark subnet 198.18.0.0/15. Never changes DNS or the default
route. Fixtures and the real generated client config must be supplied explicitly.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import platform
import re
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request


def route():
    result = subprocess.run(["/sbin/route", "-n", "get", "198.18.0.2"], text=True, capture_output=True, check=True)
    fields = dict(re.findall(r"^\s*([a-z]+):\s*(.+)$", result.stdout, re.M))
    return fields


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def http_fixture(marker):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(marker.encode())
        def log_message(self, *_):
            pass
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def stop(process):
    if process and process.poll() is None:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)
            raise RuntimeError("Xray did not shut down gracefully")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--client-config", type=Path, required=True)
    parser.add_argument("--result", type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != "Darwin" or os.geteuid() != 0:
        raise SystemExit("This isolated native TUN proof requires macOS administrator privileges.")
    version = subprocess.check_output([str(args.binary), "version"], text=True)
    assert version.startswith("Xray 26.10.21 "), version
    before = route()
    assert before.get("destination") == "default", f"Benchmark subnet already routed explicitly: {before}"
    assert not before.get("interface", "").startswith("utun"), "An existing VPN route is active; leaving it untouched."
    dns_before = subprocess.check_output(["/usr/sbin/scutil", "--dns"])
    direct, transported = http_fixture("process-bypass"), http_fixture("xray-transport")
    server = client = None
    observed_interface = None
    try:
        with tempfile.TemporaryDirectory(prefix="hikkari-native-tun-") as directory:
            state = Path(directory)
            port = free_port()
            server_config = {
                "log": {"loglevel": "warning"},
                "inbounds": [{"listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": {
                    "clients": [{"id": "11111111-2222-3333-4444-555555555555"}], "decryption": "none"}}],
                "outbounds": [{"protocol": "freedom", "settings": {"redirect": f"127.0.0.1:{transported.server_port}",
                    "finalRules": [{"action": "allow", "ip": ["127.0.0.1/32"]}]}}],
            }
            native = json.loads(args.client_config.read_text())
            assert native["inbounds"][-1]["protocol"] == "tun"
            for inbound in native["inbounds"]:
                if inbound["protocol"] == "tun":
                    inbound["settings"]["autoSystemRoutingTable"] = ["198.18.0.0/15"]
                    inbound["settings"]["autoSystemDnsToGateway"] = False
                elif inbound["protocol"] == "socks":
                    inbound["port"] = free_port()
            for outbound in native["outbounds"]:
                if outbound["tag"].startswith("transport-"):
                    outbound["settings"]["vnext"][0]["port"] = port
                    outbound["streamSettings"]["sockopt"] = {"interface": "lo0"}
                elif outbound["tag"] == "direct":
                    outbound["settings"]["redirect"] = f"127.0.0.1:{direct.server_port}"
                    outbound["streamSettings"] = {"sockopt": {"interface": "lo0"}}
            assert any("curl" in r.get("process", []) for r in native["routing"]["rules"])
            (state / "server.json").write_text(json.dumps(server_config))
            (state / "client.json").write_text(json.dumps(native))
            with (state / "native.log").open("w+") as log:
                server = subprocess.Popen([str(args.binary), "run", "-c", str(state / "server.json")], stdout=log, stderr=log)
                client = subprocess.Popen([str(args.binary), "run", "-c", str(state / "client.json")], stdout=log, stderr=log)
                try:
                    for _ in range(60):
                        if client.poll() is not None:
                            raise RuntimeError("native Xray exited before TUN readiness")
                        current = route()
                        if current.get("interface", "").startswith("utun"):
                            observed_interface = current["interface"]
                            break
                        time.sleep(0.1)
                    assert observed_interface, "native Xray did not create the benchmark route"
                    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                    control = opener.open("http://198.18.0.2:18080/control", timeout=6).read().decode()
                    assert control == "xray-transport", control
                    bypass = subprocess.check_output(["/usr/bin/curl", "--silent", "--show-error", "--noproxy", "*",
                        "--connect-timeout", "3", "--max-time", "6", "http://198.18.0.2:18080/bypass"], text=True)
                    assert bypass == "process-bypass", bypass
                    print("PASS: real native TUN uses Xray transport; curl process exclusion uses direct outbound", flush=True)
                except Exception:
                    log.flush()
                    log.seek(0)
                    print(log.read(), flush=True)
                    raise
                finally:
                    stop(client)
                    client = None
                    stop(server)
                    server = None
    finally:
        stop(client)
        stop(server)
        direct.shutdown()
        transported.shutdown()
        after = route()
        # A stale route can only be ours if it still names the exact interface
        # observed during this test. Never remove a different process's route.
        if observed_interface and after.get("interface") == observed_interface:
            subprocess.run(["/sbin/route", "-n", "delete", "-net", "198.18.0.0/15"], check=True, capture_output=True)
            after = route()
        assert after.get("interface") == before.get("interface"), (before, after)
        assert subprocess.check_output(["/usr/sbin/scutil", "--dns"]) == dns_before, "DNS state changed during native-only test"
    args.result.write_text(json.dumps({"version": "26.10.21", "os": "darwin", "native_tun": True, "process_routing": True,
        "default_route_preserved": True, "dns_preserved": True}))
    print("PASS: benchmark route removed; default route and DNS preserved", flush=True)


if __name__ == "__main__":
    main()
