#!/usr/bin/env python3
"""Fetch pinned official Xray macOS slices and build the bundled universal binary."""
import hashlib
import io
from pathlib import Path
import subprocess
import urllib.request
import zipfile

VERSION = "26.10.21"
ROOT = Path(__file__).resolve().parents[1]
ASSETS = {
    "arm64": ("Xray-macos-arm64-v8a.zip", "926cc55fda2a8757dabe4f22dc98772f218ace025f101bd0b68aee232e59d5dc"),
    "amd64": ("Xray-macos-64.zip", "8e5169b429e76ce5f550c8294db465bc259f30c158ba900aa40802b76ad2f66e"),
}


def main():
    archive = ROOT / "archive"
    archive.mkdir(exist_ok=True)
    paths = []
    for arch, (name, digest) in ASSETS.items():
        url = f"https://github.com/XTLS/Xray-core/releases/download/v{VERSION}/{name}"
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read()
        if hashlib.sha256(data).hexdigest() != digest:
            raise SystemExit(f"SHA256 mismatch: {name}")
        with zipfile.ZipFile(io.BytesIO(data)) as bundle:
            path = archive / f"xray-{arch}"
            path.write_bytes(bundle.read("xray"))
            path.chmod(0o755)
            license_name = next((n for n in bundle.namelist() if n.lower() == "license"), None)
            if license_name:
                (archive / "xray.LICENSE").write_bytes(bundle.read(license_name))
        paths.append(str(path))
        print(f"Verified {name}", flush=True)
    subprocess.run(["lipo", "-create", *paths, "-output", str(archive / "xray")], check=True)
    (archive / "xray").chmod(0o755)
    subprocess.run([str(archive / "xray"), "version"], check=True)


if __name__ == "__main__":
    main()
