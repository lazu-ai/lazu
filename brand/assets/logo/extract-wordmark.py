#!/usr/bin/env python3
"""Shape the Lazu wordmark once and freeze the outlines.

Reads Instrument Serif Regular (OFL, brand/assets/fonts/), draws "Lazu" glyph by
glyph and writes wordmark-outlines.json for generate-assets.mjs, so exported
SVG / PNG never depend on a host font. Run again only if the typeface or the
manual kerning below changes.

    python3 brand/assets/logo/extract-wordmark.py
"""
import json
from pathlib import Path

from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.ttLib import TTFont

HERE = Path(__file__).resolve().parent
FONT = HERE.parent / "fonts" / "InstrumentSerif-Regular.ttf"
OUT = HERE / "wordmark-outlines.json"
TEXT = "Lazu"
# Instrument Serif ships no pair kerning for these letters; the L's foot pushes
# the a away, so the lockup pulls that one pair in by 30/1000 em (W1 decision).
MANUAL_KERN = {("L", "a"): -30}

font = TTFont(FONT)
upm = font["head"].unitsPerEm
glyph_set = font.getGlyphSet()
cmap = font.getBestCmap()

x = 0
glyphs = []
bounds = [None, None, None, None]
for index, char in enumerate(TEXT):
    name = cmap[ord(char)]
    pen = SVGPathPen(glyph_set)
    glyph_set[name].draw(pen)
    box = BoundsPen(glyph_set)
    glyph_set[name].draw(box)
    x_min, y_min, x_max, y_max = box.bounds
    bounds = [
        x + x_min if bounds[0] is None else min(bounds[0], x + x_min),
        y_min if bounds[1] is None else min(bounds[1], y_min),
        x + x_max if bounds[2] is None else max(bounds[2], x + x_max),
        y_max if bounds[3] is None else max(bounds[3], y_max),
    ]
    glyphs.append({"char": char, "x": x, "advance": glyph_set[name].width, "d": pen.getCommands()})
    x += glyph_set[name].width
    if index + 1 < len(TEXT):
        x += MANUAL_KERN.get((char, TEXT[index + 1]), 0)

OUT.write_text(
    json.dumps(
        {
            "font": "Instrument Serif Regular (OFL 1.1)",
            "text": TEXT,
            "unitsPerEm": upm,
            "capHeight": font["OS/2"].sCapHeight,
            "xHeight": font["OS/2"].sxHeight,
            "advance": x,
            "bounds": {"minX": bounds[0], "minY": bounds[1], "maxX": bounds[2], "maxY": bounds[3]},
            "glyphs": glyphs,
        },
        indent=1,
    )
    + "\n"
)
print(f"wrote {OUT.relative_to(HERE.parent.parent.parent)} · advance {x}/{upm}")
