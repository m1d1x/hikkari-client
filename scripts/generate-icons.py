#!/usr/bin/env python3
"""generate-icons.py — every platform icon from the one vector logo.

    python3 scripts/generate-icons.py [--landing ../hikkari-landing]

brand/logo.svg is the single source: the hikkari mark as one filled path in
the brand pink, viewBox tight around it. Everything below is derived from it
and committed, so builds need none of the tools this script uses
(rsvg-convert, from Homebrew: `brew install librsvg`):

    app/assets/logo.svg                        the mark inside the app (flutter_svg)
    app/macos/.../AppIcon.appiconset/*.png     macOS: squircle on Apple's 1024 grid
    app/windows/runner/resources/app_icon.ico  Windows: the GUI, the launcher and
                                               the installer (rsrc in CI) share it
    app/android/app/src/main/res/...           Android: adaptive vector icon
                                               (API 26+) and legacy PNGs
    <landing>/static/favicon.{svg,ico},        the site, when --landing is given
    <landing>/static/apple-touch-icon.png

Change the logo or the tile here and rerun; never edit the outputs by hand.
"""

import argparse
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LOGO = ROOT / "brand" / "logo.svg"

# The tile behind the mark: the app's own background (tokens.dart `bg`) with
# a slight lift at the top so a dark tile still reads on a dark Dock/taskbar.
TILE_TOP = "#1E1E23"
TILE_BOTTOM = "#0E0E10"


def load_mark():
    svg = LOGO.read_text()
    vb = re.search(r'viewBox="0 0 ([\d.]+) ([\d.]+)"', svg)
    d = re.search(r'<path[^>]* d="([^"]+)"', svg)
    fill = re.search(r'fill="(#[0-9A-Fa-f]{6})"', svg)
    if not (vb and d and fill):
        sys.exit(f"{LOGO}: expected one filled <path> in a 0 0 W H viewBox")
    return float(vb.group(1)), float(vb.group(2)), d.group(1), fill.group(1)


W, H, PATH, PINK = load_mark()


def mark(cx, cy, width, px=None):
    """The mark centred on (cx, cy), `width` wide. When the icon is rendered
    at `px` pixels per unit below 1 (16-32 px icons), the needles and the
    eye's outline get thinner than a pixel and dissolve; an outline in the
    mark's own colour keeps them about a pixel wide."""
    s = width / W
    stroke = ""
    if px is not None and width * px < 48:
        stroke = (f' stroke="{PINK}" stroke-linejoin="round" '
                  f'stroke-width="{0.7 / (s * px):.2f}"')
    return (f'<path fill="{PINK}"{stroke} transform="translate({cx - W * s / 2:.2f} '
            f'{cy - H * s / 2:.2f}) scale({s:.5f})" d="{PATH}"/>')


def tile_svg(size, mark_ratio, radius_ratio):
    """Full-bleed tile: Windows, Android legacy, favicons, apple-touch."""
    r = size * radius_ratio
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}">
<defs><linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="{TILE_TOP}"/><stop offset="1" stop-color="{TILE_BOTTOM}"/>
</linearGradient></defs>
<rect width="{size}" height="{size}" rx="{r:.2f}" fill="url(#bg)"/>
{mark(size / 2, size / 2, size * mark_ratio, 1)}
</svg>
'''


def macos_svg(mark_ratio, size):
    """Apple's macOS 11+ grid: 1024 canvas, 824 squircle at 100, drop shadow."""
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">
<defs>
<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="{TILE_TOP}"/><stop offset="1" stop-color="{TILE_BOTTOM}"/>
</linearGradient>
<filter id="shadow" x="-10%" y="-10%" width="120%" height="125%">
<feGaussianBlur stdDeviation="10"/>
</filter>
</defs>
<rect x="100" y="110" width="824" height="824" rx="185.4" fill="#000000" fill-opacity="0.3" filter="url(#shadow)"/>
<rect x="100" y="100" width="824" height="824" rx="185.4" fill="url(#bg)"/>
<rect x="100.5" y="100.5" width="823" height="823" rx="185" fill="none" stroke="#FFFFFF" stroke-opacity="0.08"/>
{mark(512, 512, 824 * mark_ratio, size / 1024)}
</svg>
'''


def render(svg, size, out, tmp):
    src = tmp / f"{out.stem}-{size}.svg"
    src.write_text(svg)
    out.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["rsvg-convert", "-w", str(size), "-h", str(size),
                    str(src), "-o", str(out)], check=True)


def small(size):
    """Below 48 px the mark's needles vanish unless it fills more of the tile."""
    return 0.86 if size <= 32 else 0.74


def ico(sizes, out, tmp, radius_ratio):
    """An .ico whose every entry is a PNG (Windows Vista+ and every browser
    read those): a tenth of the size of BMP entries, and lossless."""
    pngs = []
    for size in sizes:
        png = tmp / f"ico-{out.stem}-{size}.png"
        render(tile_svg(size, small(size), radius_ratio), size, png, tmp)
        pngs.append((size, png.read_bytes()))
    header = struct.pack("<HHH", 0, 1, len(pngs))
    offset = len(header) + 16 * len(pngs)
    entries, bodies = b"", b""
    for size, body in pngs:
        # Width and height 0 mean 256 in an ICONDIRENTRY.
        entries += struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32,
                               len(body), offset + len(bodies))
        bodies += body
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(header + entries + bodies)


def flutter():
    shutil.copyfile(LOGO, ROOT / "app" / "assets" / "logo.svg")


def macos(tmp):
    d = ROOT / "app/macos/Runner/Assets.xcassets/AppIcon.appiconset"
    for size in (16, 32, 64, 128, 256, 512, 1024):
        render(macos_svg(0.80 if size <= 32 else 0.66, size), size,
               d / f"app_icon_{size}.png", tmp)


def windows(tmp):
    ico((16, 20, 24, 32, 40, 48, 64, 256),
        ROOT / "app/windows/runner/resources/app_icon.ico", tmp, 0.18)


ANDROID_LEGACY = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}


def android(tmp):
    res = ROOT / "app/android/app/src/main/res"
    for density, size in ANDROID_LEGACY.items():
        render(tile_svg(size, 0.74, 0.18), size,
               res / f"mipmap-{density}" / "ic_launcher.png", tmp)

    # Adaptive icon: 108 dp layers, the launcher masks to any shape inside the
    # 66 dp safe circle. The needles reach the mark's bounding-box corners, so
    # its diagonal — not its width — has to fit that circle.
    diag = (W * W + H * H) ** 0.5
    s = 64 / diag
    tx, ty = 54 - W * s / 2, 54 - H * s / 2
    fg = f'''<?xml version="1.0" encoding="utf-8"?>
<!-- Generated by scripts/generate-icons.py from brand/logo.svg. -->
<vector xmlns:android="http://schemas.android.com/apk/res/android"
    android:width="108dp"
    android:height="108dp"
    android:viewportWidth="108"
    android:viewportHeight="108">
    <group
        android:translateX="{tx:.3f}"
        android:translateY="{ty:.3f}"
        android:scaleX="{s:.5f}"
        android:scaleY="{s:.5f}">
        <path
            android:fillColor="{PINK}"
            android:pathData="{PATH}" />
    </group>
</vector>
'''
    (res / "drawable").mkdir(parents=True, exist_ok=True)
    (res / "drawable" / "ic_launcher_foreground.xml").write_text(fg)
    (res / "values" / "ic_launcher_background.xml").write_text(
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<!-- Generated by scripts/generate-icons.py. -->\n'
        f'<resources>\n    <color name="ic_launcher_background">{TILE_BOTTOM}</color>\n</resources>\n')
    (res / "mipmap-anydpi-v26").mkdir(parents=True, exist_ok=True)
    (res / "mipmap-anydpi-v26" / "ic_launcher.xml").write_text(
        '''<?xml version="1.0" encoding="utf-8"?>
<!-- Generated by scripts/generate-icons.py. -->
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background" />
    <foreground android:drawable="@drawable/ic_launcher_foreground" />
    <monochrome android:drawable="@drawable/ic_launcher_foreground" />
</adaptive-icon>
''')


def web(landing, tmp):
    static = landing / "static"
    if not static.is_dir():
        sys.exit(f"{static}: not a directory")
    (static / "favicon.svg").write_text(tile_svg(1024, small(1024), 0.22))
    ico((16, 32, 48), static / "favicon.ico", tmp, 0.22)
    # iOS masks the touch icon itself: a square, no rounding of our own.
    render(tile_svg(180, 0.70, 0), 180, static / "apple-touch-icon.png", tmp)


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--landing", type=Path, help="hikkari-landing checkout: also write the site's favicons")
    args = p.parse_args()
    if not shutil.which("rsvg-convert"):
        sys.exit("rsvg-convert not found: brew install librsvg")
    with tempfile.TemporaryDirectory() as t:
        tmp = Path(t)
        flutter()
        macos(tmp)
        windows(tmp)
        android(tmp)
        if args.landing:
            web(args.landing.resolve(), tmp)


if __name__ == "__main__":
    main()
