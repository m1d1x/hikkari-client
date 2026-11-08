#!/usr/bin/env python3
"""Move IBM Plex Mono's line box from the capitals' middle toward lowercase.

Why: the whole UI is lowercase. Plex ships ascent 1025 / descent 275 (per
1000 em), so the middle of a text line sits 0.375 em above the baseline while
the middle of a lowercase letter sits at x-height / 2 = 0.258 em. Next to an
icon (centred on the line box) the text looked ~0.117 em too low.

Centring exactly on the x-height overshoots: Cyrillic labels have descenders
(д, р, у, щ) and some carry digits or capitals, so the eye puts the middle of
a word lower than the middle of its x-height band. The target is halfway
between the x-height middle and the cap-height middle: (xHeight + capHeight)
/ 4 above the baseline.

What: keep the line height (ascent + descent = 1300) and move the split so
that (ascent - descent) / 2 equals that target. hhea, OS/2 typo and OS/2 win
metrics are set to the same values: macOS reads hhea, Windows reads win or
typo depending on the fsSelection bit, and they must not disagree.

Run after replacing or updating the font files, from `app/`:

    python3 -m venv /tmp/ftenv && /tmp/ftenv/bin/pip install fonttools
    /tmp/ftenv/bin/python tool/center_font_metrics.py

The script is idempotent: it computes absolute values from the font's own
x-height, cap height and line height, so a second run changes nothing.
"""

import glob
import os
import sys

from fontTools.ttLib import TTFont

FONTS = os.path.join(os.path.dirname(__file__), "..", "assets", "fonts", "IBMPlexMono-*.ttf")


def centred_metrics(
    x_height: int, cap_height: int, line_height: int
) -> tuple[int, int]:
    """Ascent and descent with the same sum whose midpoint sits halfway
    between the x-height middle and the cap-height middle."""
    middle = (x_height + cap_height) / 4
    ascent = round(line_height / 2 + middle)
    return ascent, line_height - ascent


def main() -> int:
    paths = sorted(glob.glob(FONTS))
    if not paths:
        print("no fonts found", file=sys.stderr)
        return 1
    for path in paths:
        font = TTFont(path)
        os2 = font["OS/2"]
        hhea = font["hhea"]
        line_height = hhea.ascent - hhea.descent + hhea.lineGap
        ascent, descent = centred_metrics(os2.sxHeight, os2.sCapHeight, line_height)
        hhea.ascent, hhea.descent, hhea.lineGap = ascent, -descent, 0
        os2.sTypoAscender, os2.sTypoDescender, os2.sTypoLineGap = ascent, -descent, 0
        os2.usWinAscent, os2.usWinDescent = ascent, descent
        font.save(path)
        print(f"{os.path.basename(path)}: ascent {ascent}, descent {descent}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
