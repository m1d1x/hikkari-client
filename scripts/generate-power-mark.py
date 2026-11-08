#!/usr/bin/env python3
"""generate-power-mark.py — the power button's eye mark from the one vector logo.

    python3 scripts/generate-power-mark.py

brand/logo.svg is one filled path of five closed outlines:

    0  the main blade: lower-left ray, upper lid and upper-right ray in one stroke
    1  the upper-left ray
    2  the lower lid
    3  the pupil
    4  the lower-right ray

The power button (app/lib/widgets/power_ring.dart) morphs the power symbol
into this mark, so each elongated outline is cut at its two tips into two
sides, and each side is resampled to the same number of points from one tip
to the other. A stroke of the power symbol, kept the same way as two sides,
then turns into a blade point by point. The pupil is kept as a circle.

Coordinates are written around the pupil centre and scaled so the power arc
of radius 1 and the eye are about the same size. The output,
app/lib/widgets/power_mark_geometry.dart, is committed; change the logo and
rerun, never edit it by hand.
"""

import math
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LOGO = ROOT / "brand" / "logo.svg"
OUT = ROOT / "app" / "lib" / "widgets" / "power_mark_geometry.dart"

SAMPLES = 240  # points per side
DENSE = 700  # points per side for pairing them up
SCALE = 1.15 / 300  # logo units → mark units: the eye a little wider than the arc


def outlines(d):
    """Closed polygons, one per subpath, from an absolute M/C/Z path."""
    polys = []
    for sub in re.split(r"(?=M)", d):
        if not sub.strip():
            continue
        tokens = re.findall(r"[MCZ]|-?\d+\.?\d*", sub)
        pts, i, cur = [], 0, None
        while i < len(tokens):
            t = tokens[i]
            if t == "M":
                cur = (float(tokens[i + 1]), float(tokens[i + 2]))
                pts.append(cur)
                i += 3
            elif t == "C":
                i += 1
                while i < len(tokens) and tokens[i] not in "MCZ":
                    c1 = (float(tokens[i]), float(tokens[i + 1]))
                    c2 = (float(tokens[i + 2]), float(tokens[i + 3]))
                    p = (float(tokens[i + 4]), float(tokens[i + 5]))
                    for k in range(1, 9):
                        s = k / 8
                        u = 1 - s
                        pts.append(
                            tuple(
                                u**3 * a + 3 * u * u * s * b + 3 * u * s * s * c + s**3 * e
                                for a, b, c, e in zip(cur, c1, c2, p)
                            )
                        )
                    cur = p
                    i += 6
            else:  # Z
                i += 1
        if len(pts) > 1 and pts[0] == pts[-1]:
            pts.pop()
        polys.append(pts)
    return polys


def dist(a, b):
    return math.hypot(a[0] - b[0], a[1] - b[1])


def tips(poly):
    """The two outline points farthest apart: the ends of an elongated shape."""
    best = (0, 0, -1.0)
    for i, a in enumerate(poly):
        for j in range(i + 1, len(poly)):
            dd = dist(a, poly[j])
            if dd > best[2]:
                best = (i, j, dd)
    return best[0], best[1]


def resample(chain, n):
    lengths = [0.0]
    for a, b in zip(chain, chain[1:]):
        lengths.append(lengths[-1] + dist(a, b))
    total = lengths[-1]
    out, j = [], 1
    for k in range(n):
        want = total * k / (n - 1)
        while j < len(chain) - 1 and lengths[j] < want:
            j += 1
        span = lengths[j] - lengths[j - 1]
        f = 0 if span == 0 else (want - lengths[j - 1]) / span
        a, b = chain[j - 1], chain[j]
        out.append((a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f))
    return out


def pair(a, b):
    """Facing pairs along two sides of a shape, [SAMPLES] of them.

    Both sides are aligned end to end by dynamic time warping: every point of
    each side is paired with some point of the other, in order, at the least
    total distance. So no stretch of either side is skipped (the cut-out at a
    ray's base stays in), while points still face across the shape. The
    alignment is then resampled evenly by the length travelled on both sides.
    """
    a, b = resample(a, DENSE), resample(b, DENSE)
    n = len(a)
    inf = float("inf")
    cost = [[inf] * (n + 1) for _ in range(n + 1)]
    cost[0][0] = 0.0
    for i in range(1, n + 1):
        ai = a[i - 1]
        row, up = cost[i], cost[i - 1]
        for j in range(1, n + 1):
            bj = b[j - 1]
            d = math.hypot(ai[0] - bj[0], ai[1] - bj[1])
            row[j] = d + min(up[j], row[j - 1], up[j - 1])
    path, i, j = [], n, n
    while i > 0 and j > 0:
        path.append((i - 1, j - 1))
        steps = [(cost[i - 1][j - 1], i - 1, j - 1), (cost[i - 1][j], i - 1, j), (cost[i][j - 1], i, j - 1)]
        _, i, j = min(steps)
    path.reverse()
    travelled = [0.0]
    for (i0, j0), (i1, j1) in zip(path, path[1:]):
        travelled.append(travelled[-1] + dist(a[i0], a[i1]) + dist(b[j0], b[j1]))
    total = travelled[-1]
    left, right, k = [], [], 1
    for s in range(SAMPLES):
        want = total * s / (SAMPLES - 1)
        while k < len(path) - 1 and travelled[k] < want:
            k += 1
        span = travelled[k] - travelled[k - 1]
        f = 0 if span == 0 else (want - travelled[k - 1]) / span
        (i0, j0), (i1, j1) = path[k - 1], path[k]
        left.append((a[i0][0] + (a[i1][0] - a[i0][0]) * f, a[i0][1] + (a[i1][1] - a[i0][1]) * f))
        right.append((b[j0][0] + (b[j1][0] - b[j0][0]) * f, b[j0][1] + (b[j1][1] - b[j0][1]) * f))
    return left, right


def sides(poly, start_near):
    """Two sides from the tip nearer [start_near] to the other tip, as
    (left, right) where left lies along +normal of the travel direction
    (normal = (-dy, dx), the same rule the painter uses)."""
    i, j = tips(poly)
    if dist(poly[j], start_near) < dist(poly[i], start_near):
        i, j = j, i
    n = len(poly)
    a = [poly[(i + k) % n] for k in range(((j - i) % n) + 1)]
    b = [poly[(i - k) % n] for k in range(((i - j) % n) + 1)]
    a, b = pair(a, b)
    m = SAMPLES // 2
    centre = ((a[m][0] + b[m][0]) / 2, (a[m][1] + b[m][1]) / 2)
    ahead = ((a[m + 1][0] + b[m + 1][0]) / 2, (a[m + 1][1] + b[m + 1][1]) / 2)
    normal = (-(ahead[1] - centre[1]), ahead[0] - centre[0])
    if (a[m][0] - centre[0]) * normal[0] + (a[m][1] - centre[1]) * normal[1] < 0:
        a, b = b, a
    return a, b


def main():
    d = re.search(r' d="([^"]+)"', LOGO.read_text()).group(1)
    polys = outlines(d)
    assert len(polys) == 5, f"expected 5 outlines in {LOGO}, got {len(polys)}"
    blade, ray_ul, lower_lid, pupil, ray_lr = polys

    xs = [p[0] for p in pupil]
    ys = [p[1] for p in pupil]
    centre = ((min(xs) + max(xs)) / 2, (min(ys) + max(ys)) / 2)
    radius = ((max(xs) - min(xs)) + (max(ys) - min(ys))) / 4

    def mark(p):
        return ((p[0] - centre[0]) * SCALE, (p[1] - centre[1]) * SCALE)

    # Start tips: the blade runs from its upper-right end down to its
    # lower-left end, as the power arc runs from the right of its gap round to
    # the left of it. Rays run from the eye outwards. The lower lid runs from
    # its lower-left end.
    shapes = {
        "blade": sides(blade, (1101, 0)),
        "lowerLid": sides(lower_lid, (0, 948)),
        "rayUpperLeft": sides(ray_ul, centre),
        "rayLowerRight": sides(ray_lr, centre),
    }

    # Where the upper lid ends along the blade, as a share of its length from
    # the upper-right tip: the blade shown before the rays grow is the lid
    # alone. The rays join the lid in wide notched stretches; walking out
    # from the junctions towards the middle, the lid starts where the blade
    # is back to the lid's own width, so no piece of a junction stays on.
    blade_left, blade_right = shapes["blade"]
    width = [dist(a, b) for a, b in zip(blade_left, blade_right)]
    mid = [((a[0] + b[0]) / 2, (a[1] + b[1]) / 2) for a, b in zip(blade_left, blade_right)]
    lid_width = sorted(width[int(SAMPLES * 0.35) : int(SAMPLES * 0.6)])
    lid_width = lid_width[len(lid_width) // 2]
    near_tr = min(range(SAMPLES), key=lambda k: dist(mid[k], (800, 245)))
    near_bl = min(range(SAMPLES), key=lambda k: dist(mid[k], (275, 640)))
    k_from = next(k for k in range(near_tr, SAMPLES) if width[k] <= lid_width * 1.15)
    k_to = next(k for k in range(near_bl, 0, -1) if width[k] <= lid_width * 1.15)
    margin = SAMPLES // 60  # a little further in, clear of the junction's edge
    lid_from = (k_from + margin) / (SAMPLES - 1)
    lid_to = (k_to - margin) / (SAMPLES - 1)

    def points(chain):
        flat = ", ".join(f"{x:.4f}, {y:.4f}" for x, y in map(mark, chain))
        return f"[{flat}]"

    lines = [
        "// GENERATED by scripts/generate-power-mark.py from brand/logo.svg.",
        "// Do not edit by hand: change the logo and rerun the script.",
        "",
        "/// The Hikkari eye as the power button draws it: each elongated outline",
        "/// of the logo cut at its tips into two sides of [kMarkSamples] points,",
        "/// from one tip to the other, as flat x, y pairs. Units: the power arc has",
        "/// radius 1, the pupil sits at the origin.",
        "library;",
        "",
        f"const int kMarkSamples = {SAMPLES};",
        f"const double kMarkPupilRadius = {radius * SCALE:.4f};",
        "",
        "/// Share of the blade's length, from its upper-right tip, where the upper",
        "/// lid starts and ends; outside it the blade is the two rays.",
        f"const double kMarkLidFrom = {lid_from:.4f};",
        f"const double kMarkLidTo = {lid_to:.4f};",
        "",
    ]
    for name, (left, right) in shapes.items():
        cap = name[0].upper() + name[1:]
        lines.append(f"const List<double> kMark{cap}Left = {points(left)};")
        lines.append(f"const List<double> kMark{cap}Right = {points(right)};")
        lines.append("")
    OUT.write_text("\n".join(lines))
    # Committed in the formatter's shape, so a rerun leaves no diff.
    subprocess.run(["dart", "format", str(OUT)], check=True, capture_output=True)
    print(f"wrote {OUT.relative_to(ROOT)}: pupil r={radius:.1f}, lid {lid_from:.3f}..{lid_to:.3f}")


if __name__ == "__main__":
    main()
