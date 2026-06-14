# /// script
# requires-python = ">=3.9"
# dependencies = ["numpy>=1.24", "scipy>=1.10"]
# ///
"""Render an SVG preview of the unfolded net(s) and verify no faces overlap."""

from __future__ import annotations

import numpy as np

from snubgeom import SnubDodecahedron
from unfold import Unfolder, _convex_overlap, _shrink


def layout_pieces(pieces, gap=1.5):
    """Translate each piece so they sit side by side; return offsets."""
    offsets = []
    x_cursor = 0.0
    for p in pieces:
        x0, y0, x1, y1 = p.bbox()
        offsets.append((x_cursor - x0, -y0))
        x_cursor += (x1 - x0) + gap
    return offsets


def verify_no_overlap(pieces):
    """Independent global check: no two faces in the same piece overlap."""
    bad = 0
    for p in pieces:
        polys = [poly for _, poly in p.faces]
        adj_share = {}  # face index pairs that share an edge are allowed to touch
        for i in range(len(polys)):
            for j in range(i + 1, len(polys)):
                a = _shrink(polys[i], 5e-3)
                b = _shrink(polys[j], 5e-3)
                if _convex_overlap(a, b):
                    bad += 1
    return bad


def write_svg(pieces, path, scale=40.0, gap=1.5):
    offsets = layout_pieces(pieces, gap=gap)
    # global bbox
    minx = miny = 1e9
    maxx = maxy = -1e9
    for p, (ox, oy) in zip(pieces, offsets):
        for _, poly in p.faces:
            for x, y in poly:
                minx = min(minx, x + ox)
                miny = min(miny, y + oy)
                maxx = max(maxx, x + ox)
                maxy = max(maxy, y + oy)
    pad = 1.0
    W = (maxx - minx + 2 * pad) * scale
    H = (maxy - miny + 2 * pad) * scale

    def X(x):
        return (x - minx + pad) * scale

    def Y(y):
        return H - (y - miny + pad) * scale  # flip for screen coords

    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{W:.0f}" height="{H:.0f}" '
        f'viewBox="0 0 {W:.0f} {H:.0f}">',
        f'<rect width="{W:.0f}" height="{H:.0f}" fill="white"/>',
    ]
    for p, (ox, oy) in zip(pieces, offsets):
        for fi, poly in p.faces:
            pts = " ".join(f"{X(x + ox):.2f},{Y(y + oy):.2f}" for x, y in poly)
            fill = "#ffe9b3" if len(poly) == 5 else "#cfe8ff"
            out.append(
                f'<polygon points="{pts}" fill="{fill}" stroke="#888" '
                f'stroke-width="0.6"/>'
            )
        # hinges (fold lines) in green
        for h in p.hinges:
            x1, y1 = h.p
            x2, y2 = h.q
            col = "#2a8f2a" if h.kind == "3-3" else "#c000c0"
            out.append(
                f'<line x1="{X(x1 + ox):.2f}" y1="{Y(y1 + oy):.2f}" '
                f'x2="{X(x2 + ox):.2f}" y2="{Y(y2 + oy):.2f}" '
                f'stroke="{col}" stroke-width="1.2"/>'
            )
    out.append("</svg>")
    with open(path, "w") as f:
        f.write("\n".join(out))


if __name__ == "__main__":
    s = SnubDodecahedron()
    s.verify()
    pieces = Unfolder(s).unfold()
    bad = verify_no_overlap(pieces)
    print(f"pieces={len(pieces)} faces={sum(len(p.faces) for p in pieces)} "
          f"overlapping_pairs={bad}")
    write_svg(pieces, "net_preview.svg")
    print("wrote net_preview.svg")
