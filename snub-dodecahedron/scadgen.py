"""
Turn an unfolded net into a 3D, foldable, beveled solid (OpenSCAD + STL).

Printing / folding model
------------------------
The net is printed as a flat plate of thickness ``T`` lying in the XY plane:

    z = T  -> the OUTER surface of the finished polyhedron (stays continuous
              across every fold, so the outside looks clean)
    z = 0  -> the INNER surface (this is where the V-grooves open)

To fold two adjacent panels from flat (180 deg) to the solid's interior
dihedral angle ``theta`` we remove a V-groove on the inner side along the
shared edge.  When the groove closes, the panels sit at ``theta``.

    fold angle   = 180 - theta            (how far we rotate, total)
    each wall is cut at half-angle  gamma = (fold + slack) / 2   from vertical
    groove half-width at the base    w    = (T - web) * tan(gamma)
    a thin "web" of thickness ``web`` is left at the top as a living hinge.

``slack`` makes the groove a little wider than strictly necessary so the
panels can always reach ``theta`` (and a touch beyond); any resulting gap is
filled with glue.  This follows the "leave more room than necessary" bias.

Glue seams (edges where two far-apart parts of the net meet when folded) are
chamfered on each mating face by ``gamma`` as well, so the two faces are
coplanar and glue flush, forming the correct dihedral.

Recommended print orientation: inner (grooved) side DOWN on the bed.  Every
bevel wall is within ~gamma of vertical (<~20 deg), so the grooves are
self-supporting and need no support material.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

import numpy as np


@dataclass
class FoldParams:
    edge_mm: float = 30.0       # length of every polygon edge
    thickness_mm: float = 12.0  # plate depth (T): thick, rigid panels
    web_mm: float = 0.4         # living-hinge thickness left at the top (~2 layers)
    slack_deg: float = 6.0      # extra opening added to every fold (more room)
    over_mm: float = 0.4        # run grooves slightly past each vertex
    eps_mm: float = 0.05        # cut a hair below the bed for clean booleans
    bevel_boundary: bool = True # chamfer glue-seam edges too
    piece_gap_mm: float = 8.0   # spacing between pieces in the layout
    magnets: bool = True            # pocket a magnet hole in each exterior face
    magnet_diameter_mm: float = 25.4 / 8.0   # 1/8 inch
    magnet_depth_mm: float = 25.4 / 16.0     # 1/16 inch
    magnet_fn: int = 32             # facets for the round magnet pocket


def _unit(v):
    v = np.asarray(v, float)
    n = np.linalg.norm(v)
    return v / n if n else v


def _wedge_multmatrix(start, x_axis, z_axis):
    """4x4 (row-major lists) mapping local (x=u, y=z_world, z=length) -> world.

    local x  -> x_axis      (in-plane direction the groove widens)
    local y  -> world +Z    (plate thickness direction)
    local z  -> z_axis      (along the edge / extrusion direction)
    origin   -> start
    """
    xa = _unit(x_axis)
    za = _unit(z_axis)
    return [
        [xa[0], 0.0, za[0], start[0]],
        [xa[1], 0.0, za[1], start[1]],
        [0.0,   1.0, 0.0,   start[2]],
        [0.0,   0.0, 0.0,   1.0],
    ]


def _fmt_mat(m):
    rows = ", ".join("[" + ", ".join(f"{v:.6f}" for v in row) + "]" for row in m)
    return "[" + rows + "]"


def _gamma(dihedral, slack_deg):
    """Half wall-angle from vertical for a fold to `dihedral` (radians)."""
    fold = 180.0 - dihedral
    return math.radians((fold + slack_deg) / 2.0)


def _scad_for_piece(piece, params: FoldParams, offset, name):
    e = params.edge_mm
    T = params.thickness_mm
    web = params.web_mm
    over = params.over_mm
    eps = params.eps_mm
    ox, oy = offset

    def S(pt):  # scale a unit-net 2D point to mm + apply layout offset
        return (pt[0] * e + ox, pt[1] * e + oy)

    lines = [f"module {name}() {{", "  difference() {"]

    # --- the flat plate: 2D union of all face polygons, extruded ---
    lines.append("    linear_extrude(height = T)")
    lines.append("    union() {")
    for _, poly in piece.faces:
        pts = ", ".join(f"[{x:.4f}, {y:.4f}]" for x, y in (S(p) for p in poly))
        lines.append(f"      polygon([{pts}]);")
    lines.append("    }")

    # --- subtract every groove / chamfer ---
    apex_z = T - web
    if apex_z <= 0:
        raise ValueError("web_mm must be smaller than thickness_mm")

    # internal fold hinges: symmetric V-groove
    for h in piece.hinges:
        A = np.array([*S(h.p), 0.0])
        B = np.array([*S(h.q), 0.0])
        d = _unit(B - A)
        nperp = np.array([-d[1], d[0], 0.0])
        g = _gamma(h.dihedral, params.slack_deg)
        w = apex_z * math.tan(g)
        wb = w * (apex_z + eps) / apex_z  # widen base so walls keep angle g
        L = float(np.linalg.norm(B - A)) + 2 * over
        start = A - d * over
        m = _wedge_multmatrix(start, nperp, d)
        tri = f"[[{-wb:.4f}, {-eps:.4f}], [{wb:.4f}, {-eps:.4f}], [0, {apex_z:.4f}]]"
        lines.append(f"    multmatrix({_fmt_mat(m)})")
        lines.append(f"      linear_extrude(height = {L:.4f}) polygon({tri});")

    # glue-seam (boundary) edges: one-sided chamfer, feathered to the top
    if params.bevel_boundary:
        for b in piece.boundary:
            A = np.array([*S(b.p), 0.0])
            B = np.array([*S(b.q), 0.0])
            d = _unit(B - A)
            nperp = np.array([-d[1], d[0], 0.0])
            inward = np.array([b.inward[0], b.inward[1], 0.0])
            if nperp @ inward < 0:
                nperp = -nperp
            g = _gamma(b.dihedral, params.slack_deg)
            # chamfer up to the same apex as the hinge grooves, leaving a thin
            # land at the top: this keeps the mesh a clean 2-manifold (no
            # knife edge) while still giving a flush glue face.
            w = apex_z * math.tan(g)
            wb = w * (apex_z + eps) / apex_z
            L = float(np.linalg.norm(B - A)) + 2 * over
            start = A - d * over
            m = _wedge_multmatrix(start, nperp, d)
            tri = f"[[0, {-eps:.4f}], [{wb:.4f}, {-eps:.4f}], [0, {apex_z:.4f}]]"
            lines.append(f"    multmatrix({_fmt_mat(m)})")
            lines.append(f"      linear_extrude(height = {L:.4f}) polygon({tri});")

    # magnet pockets: one per face, centered on the exterior (top) surface
    if params.magnets and params.magnet_diameter_mm > 0 and params.magnet_depth_mm > 0:
        if params.magnet_depth_mm >= T:
            raise ValueError("magnet_depth_mm must be smaller than thickness_mm")
        d = params.magnet_diameter_mm
        depth = params.magnet_depth_mm
        z0 = T - depth
        for _, poly in piece.faces:
            cx = sum(S(p)[0] for p in poly) / len(poly)
            cy = sum(S(p)[1] for p in poly) / len(poly)
            lines.append(
                f"    translate([{cx:.4f}, {cy:.4f}, {z0:.4f}]) "
                f"cylinder(h = {depth + eps:.4f}, d = {d:.4f}, $fn = {params.magnet_fn});"
            )

    lines.append("  }")
    lines.append("}")
    return "\n".join(lines)


def generate_scad(pieces, params: FoldParams) -> str:
    # lay pieces out left-to-right
    offsets = []
    cursor = 0.0
    for p in pieces:
        x0, y0, x1, y1 = p.bbox()
        offsets.append((cursor - x0 * params.edge_mm, -y0 * params.edge_mm))
        cursor += (x1 - x0) * params.edge_mm + params.piece_gap_mm

    magnet_note = (
        f"// magnet pockets = {params.magnet_diameter_mm:.3f} mm dia x "
        f"{params.magnet_depth_mm:.3f} mm deep, exterior face centers"
        if params.magnets else "// magnet pockets: disabled"
    )
    header = [
        "// Snub dodecahedron foldable net - generated by scadgen.py",
        f"// edge = {params.edge_mm} mm, thickness(depth) = {params.thickness_mm} mm,",
        f"// hinge web = {params.web_mm} mm, fold slack = {params.slack_deg} deg",
        magnet_note,
        "// Print inner (grooved) side DOWN; no supports needed.",
        f"T = {params.thickness_mm};",
        "$fn = 8;",
        "",
    ]
    body = []
    calls = []
    for i, (p, off) in enumerate(zip(pieces, offsets)):
        name = f"piece{i}"
        body.append(_scad_for_piece(p, params, off, name))
        calls.append(f"{name}();")
    return "\n".join(header) + "\n".join(body) + "\n\n" + "\n".join(calls) + "\n"
