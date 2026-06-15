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
from scadgen import FoldParams, generate_scad, generate_scad_single
from snubgeom import SnubDodecahedron
from unfold import Unfolder, _fits_bed


def parse_bed(spec):
    """Parse a bed spec like '7x7in', '7in', '180x180', '180' -> (w_mm, h_mm).

    Values are millimetres unless the spec contains 'in' or '\"' (inches),
    in which case the unit applies to the whole spec.
    """
    s = spec.strip().lower().replace('"', "in")
    inch = "in" in s
    s = s.replace("in", "")
    vals = [float(p) for p in s.split("x")]
    if len(vals) == 1:
        vals = [vals[0], vals[0]]
    f = 25.4 if inch else 1.0
    return vals[0] * f, vals[1] * f


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
    ap.add_argument("--magnet-diameter", type=float, default=25.4 / 8.0,
                    help="magnet pocket diameter, mm (default 3.175 = 1/8 in)")
    ap.add_argument("--magnet-depth", type=float, default=25.4 / 16.0,
                    help="magnet pocket depth, mm (default 1.5875 = 1/16 in)")
    ap.add_argument("--no-magnets", action="store_true",
                    help="do not pocket magnet holes in the faces")
    ap.add_argument("--bed", type=str, default=None,
                    help="print-bed size, e.g. '7x7in' or '180x180' (mm). "
                         "Splits into the fewest balanced pieces that fit.")
    ap.add_argument("--bed-margin", type=float, default=5.0,
                    help="keep pieces this far inside each bed edge, mm (default 5)")
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

    bed_units = None
    if args.bed and args.max_faces_per_piece:
        print("error: use either --bed or --max-faces-per-piece, not both",
              file=sys.stderr)
        return 2

    unfolder = Unfolder(solid)
    if args.bed:
        bed_w, bed_h = parse_bed(args.bed)
        usable_w = bed_w - 2 * args.bed_margin
        usable_h = bed_h - 2 * args.bed_margin
        bed_units = (usable_w / args.edge, usable_h / args.edge)
        print(f"unfolding to fit a {bed_w:.1f} x {bed_h:.1f} mm bed "
              f"({args.bed_margin:.1f} mm margin -> usable "
              f"{usable_w:.1f} x {usable_h:.1f} mm) ...")
        pieces = unfolder.unfold_for_bed(bed_units)
    else:
        print("unfolding into a flat net ...")
        pieces = unfolder.unfold(max_faces_per_piece=args.max_faces_per_piece)
    bad = verify_no_overlap(pieces)
    n_hinge = sum(len(p.hinges) for p in pieces)
    n_seam = sum(len(p.boundary) for p in pieces) // 2
    print(f"  {len(pieces)} piece(s), {sum(len(p.faces) for p in pieces)} faces, "
          f"{n_hinge} fold hinges, {n_seam} glue seams, "
          f"{bad} overlapping face pairs")
    if bad:
        print("  WARNING: overlaps detected; try --bed or --max-faces-per-piece",
              file=sys.stderr)

    # per-piece sizes (and bed-fit confirmation)
    for i, p in enumerate(pieces):
        x0, y0, x1, y1 = p.bbox()
        w, h = (x1 - x0) * args.edge, (y1 - y0) * args.edge
        note = ""
        if bed_units is not None:
            ok = w <= bed_units[0] * args.edge + 1e-6 and h <= bed_units[1] * args.edge + 1e-6
            note = "  FITS BED" if ok else "  !! TOO BIG"
        print(f"    piece {i}: {len(p.faces):2d} faces, {w:6.1f} x {h:6.1f} mm{note}")

    if not args.no_magnets:
        print(f"  magnet pockets: {args.magnet_diameter:.3f} mm dia x "
              f"{args.magnet_depth:.3f} mm deep, one per face (exterior)")

    svg_path = args.out + "_preview.svg"
    magnet_r = (args.magnet_diameter / 2.0 / args.edge) if not args.no_magnets else 0.0
    write_svg(pieces, svg_path, magnet_radius=magnet_r, bed_units=bed_units)
    print(f"  wrote {svg_path}")

    params = FoldParams(
        edge_mm=args.edge,
        thickness_mm=args.depth,
        web_mm=args.web,
        slack_deg=args.slack,
        over_mm=args.over,
        bevel_boundary=not args.no_boundary_bevel,
        magnets=not args.no_magnets,
        magnet_diameter_mm=args.magnet_diameter,
        magnet_depth_mm=args.magnet_depth,
    )

    # combined .scad (overview / single-bed layout)
    scad_path = args.out + ".scad"
    with open(scad_path, "w") as f:
        f.write(generate_scad(pieces, params))
    print(f"  wrote {scad_path}")

    # one standalone .scad per piece (each centered, ready to print on the bed)
    piece_scads = []
    if len(pieces) > 1:
        for i, p in enumerate(pieces):
            ppath = f"{args.out}_piece{i:02d}.scad"
            with open(ppath, "w") as f:
                f.write(generate_scad_single(p, params, name=f"piece{i:02d}"))
            piece_scads.append(ppath)
        print(f"  wrote {len(piece_scads)} per-piece .scad files "
              f"({args.out}_piece00.scad ...)")

    def render(scad_file):
        openscad = shutil.which("openscad")
        if not openscad:
            print("  openscad not found; skipping STL", file=sys.stderr)
            return None
        stl_file = scad_file[:-5] + ".stl"
        print(f"  rendering {stl_file} ...")
        r = subprocess.run([openscad, "-o", stl_file, scad_file],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(r.stderr[-2000:], file=sys.stderr)
            print("  STL render failed", file=sys.stderr)
            return None
        return stl_file

    if args.stl:
        targets = piece_scads if len(pieces) > 1 else [scad_path]
        for t in targets:
            render(t)

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
