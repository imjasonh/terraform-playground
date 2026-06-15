#!/usr/bin/env python3
# /// script
# requires-python = ">=3.9"
# dependencies = ["numpy>=1.24", "scipy>=1.10"]
# ///
"""
Generate a foldable, beveled snub-dodecahedron net as OpenSCAD (+ optional STL).

Example:
    uv run generate.py --edge 30 --depth 3 --web 0.6 --slack 6 --stl

The output is a flat plate you print, then fold along the grooved hinges and
glue the seams to obtain a snub dodecahedron.  See README.md for details.
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys

from preview import verify_no_overlap, write_svg
from scadgen import FoldParams, generate_scad
from snubgeom import SnubDodecahedron
from unfold import Unfolder


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--edge", type=float, default=30.0,
                    help="edge length of every polygon, mm (default 30)")
    ap.add_argument("--depth", "--thickness", dest="depth", type=float,
                    default=12.0, help="plate depth / thickness, mm (default 12)")
    ap.add_argument("--web", type=float, default=0.4,
                    help="living-hinge web thickness at the top, mm (default 0.4)")
    ap.add_argument("--slack", type=float, default=6.0,
                    help="extra fold opening added to every groove, deg (default 6)")
    ap.add_argument("--over", type=float, default=0.4,
                    help="run grooves this far past each vertex, mm (default 0.4)")
    ap.add_argument("--max-faces-per-piece", type=int, default=None,
                    help="split the net into smaller pieces of at most N faces")
    ap.add_argument("--no-boundary-bevel", action="store_true",
                    help="leave glue-seam edges square instead of chamfered")
    ap.add_argument("--out", default="out/snub_net",
                    help="output path prefix (default out/snub_net)")
    ap.add_argument("--stl", action="store_true",
                    help="also render an STL with the openscad CLI")
    ap.add_argument("--solid", action="store_true",
                    help="also export the target snub dodecahedron solid (reference)")
    args = ap.parse_args(argv)

    out_dir = os.path.dirname(args.out) or "."
    os.makedirs(out_dir, exist_ok=True)

    print("building snub dodecahedron geometry ...")
    solid = SnubDodecahedron()
    info = solid.verify()
    print(f"  verified: {info['faces']} faces "
          f"({info['pentagons']} pentagons + {info['triangles']} triangles), "
          f"dihedrals 3-3={info['dihedral_3_3']:.3f} 3-5={info['dihedral_3_5']:.3f}")

    print("unfolding into a flat net ...")
    pieces = Unfolder(solid).unfold(max_faces_per_piece=args.max_faces_per_piece)
    bad = verify_no_overlap(pieces)
    n_hinge = sum(len(p.hinges) for p in pieces)
    n_seam = sum(len(p.boundary) for p in pieces) // 2
    print(f"  {len(pieces)} piece(s), {sum(len(p.faces) for p in pieces)} faces, "
          f"{n_hinge} fold hinges, {n_seam} glue seams, "
          f"{bad} overlapping face pairs")
    if bad:
        print("  WARNING: overlaps detected; try --max-faces-per-piece to split",
              file=sys.stderr)

    svg_path = args.out + "_preview.svg"
    write_svg(pieces, svg_path)
    print(f"  wrote {svg_path}")

    params = FoldParams(
        edge_mm=args.edge,
        thickness_mm=args.depth,
        web_mm=args.web,
        slack_deg=args.slack,
        over_mm=args.over,
        bevel_boundary=not args.no_boundary_bevel,
    )
    scad = generate_scad(pieces, params)
    scad_path = args.out + ".scad"
    with open(scad_path, "w") as f:
        f.write(scad)
    print(f"  wrote {scad_path}")

    if args.stl:
        openscad = shutil.which("openscad")
        if not openscad:
            print("  openscad not found; skipping STL", file=sys.stderr)
        else:
            stl_path = args.out + ".stl"
            print(f"  rendering {stl_path} (this can take a minute) ...")
            r = subprocess.run([openscad, "-o", stl_path, scad_path],
                               capture_output=True, text=True)
            if r.returncode != 0:
                print(r.stderr[-2000:], file=sys.stderr)
                print("  STL render failed", file=sys.stderr)
                return 1
            print(f"  wrote {stl_path}")

    if args.solid:
        V = solid.vertices * args.edge
        pts = ", ".join(f"[{v[0]:.4f},{v[1]:.4f},{v[2]:.4f}]" for v in V)
        faces = ", ".join("[" + ",".join(str(i) for i in f) + "]"
                          for f in solid.faces)
        solid_scad = args.out + "_solid.scad"
        with open(solid_scad, "w") as f:
            f.write(f"// target snub dodecahedron (edge = {args.edge} mm)\n"
                    f"polyhedron(points=[{pts}], faces=[{faces}], convexity=10);\n")
        print(f"  wrote {solid_scad}")

    print("done.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
