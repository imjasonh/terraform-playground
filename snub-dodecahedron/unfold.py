# /// script
# requires-python = ">=3.9"
# dependencies = ["numpy>=1.24", "scipy>=1.10"]
# ///
"""
Unfold the snub dodecahedron into one or more flat, non-overlapping nets.

We grow each net greedily over the face-adjacency graph. A face is attached
to a growing net by rotating it (in 3D) about the shared edge until it is
coplanar with the rest of the net, then projecting to 2D. If attaching a
face would make it overlap a face already in the net, we leave it for a
later net. The result is a "spanning forest": a small number of connected
flat pieces that together tile the whole surface and can be glued up.

Each piece records:
  - faces:    [(face_index, [(x, y), ...]), ...]      2D polygons
  - hinges:   [Hinge(p, q, dihedral, kind), ...]      fold lines (grooved)
  - boundary: [Boundary(p, q, dihedral, inward, kind)] cut/glue edges (chamfered)
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

import numpy as np

from snubgeom import SnubDodecahedron


@dataclass
class Hinge:
    p: tuple  # 2D endpoint
    q: tuple
    dihedral: float
    kind: str  # "3-3" or "3-5"


@dataclass
class Boundary:
    p: tuple
    q: tuple
    dihedral: float
    inward: tuple  # unit 2D vector pointing into the panel (where to chamfer)
    kind: str


@dataclass
class Piece:
    faces: list = field(default_factory=list)
    hinges: list = field(default_factory=list)
    boundary: list = field(default_factory=list)

    def bbox(self):
        xs, ys = [], []
        for _, poly in self.faces:
            for x, y in poly:
                xs.append(x)
                ys.append(y)
        return min(xs), min(ys), max(xs), max(ys)


# ---------------------------------------------------------------------------
# small linear-algebra helpers (homogeneous 4x4)
# ---------------------------------------------------------------------------
def _skew(v):
    x, y, z = v
    return np.array([[0, -z, y], [z, 0, -x], [-y, x, 0]], dtype=float)


def _rot_align(a, b):
    """3x3 rotation taking unit vector a onto unit vector b."""
    a = a / np.linalg.norm(a)
    b = b / np.linalg.norm(b)
    v = np.cross(a, b)
    c = float(a @ b)
    s = np.linalg.norm(v)
    if s < 1e-12:
        if c > 0:
            return np.eye(3)
        # antiparallel: 180 deg about any axis perpendicular to a
        perp = np.array([1.0, 0.0, 0.0])
        if abs(a[0]) > 0.9:
            perp = np.array([0.0, 1.0, 0.0])
        axis = np.cross(a, perp)
        axis = axis / np.linalg.norm(axis)
        K = _skew(axis)
        return np.eye(3) + 2 * (K @ K)
    K = _skew(v)
    return np.eye(3) + K + K @ K * ((1 - c) / (s * s))


def _mat4(R3=None, t=None):
    M = np.eye(4)
    if R3 is not None:
        M[:3, :3] = R3
    if t is not None:
        M[:3, 3] = t
    return M


def _rot_about_line(P, d, angle):
    """4x4: rotate by `angle` about the line through point P with direction d."""
    d = d / np.linalg.norm(d)
    K = _skew(d)
    R3 = np.eye(3) + math.sin(angle) * K + (1 - math.cos(angle)) * (K @ K)
    T1 = _mat4(t=-np.asarray(P, float))
    T2 = _mat4(t=np.asarray(P, float))
    return T2 @ _mat4(R3=R3) @ T1


def _apply(M, v):
    h = np.array([v[0], v[1], v[2], 1.0])
    return (M @ h)[:3]


# ---------------------------------------------------------------------------
# 2D geometry helpers
# ---------------------------------------------------------------------------
def _side(a, b, p):
    """Signed area sign of point p relative to directed line a->b."""
    return (b[0] - a[0]) * (p[1] - a[1]) - (b[1] - a[1]) * (p[0] - a[0])


def _poly_centroid(poly):
    arr = np.array(poly)
    return arr.mean(axis=0)


def _shrink(poly, factor):
    c = _poly_centroid(poly)
    return [tuple(c + (np.array(p) - c) * (1 - factor)) for p in poly]


def _convex_overlap(poly_a, poly_b):
    """SAT overlap test for two convex polygons (True if they overlap)."""
    for poly in (poly_a, poly_b):
        n = len(poly)
        for i in range(n):
            x1, y1 = poly[i]
            x2, y2 = poly[(i + 1) % n]
            ax, ay = -(y2 - y1), (x2 - x1)  # axis = edge normal
            amin = min((ax * px + ay * py) for px, py in poly_a)
            amax = max((ax * px + ay * py) for px, py in poly_a)
            bmin = min((ax * px + ay * py) for px, py in poly_b)
            bmax = max((ax * px + ay * py) for px, py in poly_b)
            if amax <= bmin or bmax <= amin:
                return False  # separating axis found
    return True


# ---------------------------------------------------------------------------
# unfolding
# ---------------------------------------------------------------------------
class Unfolder:
    def __init__(self, solid: SnubDodecahedron, overlap_margin=2e-3):
        self.s = solid
        self.margin = overlap_margin
        # face adjacency: fi -> list of (neighbor_fi, (va, vb))
        self.adj = {fi: [] for fi in range(len(solid.faces))}
        for e, fs in solid.edge_faces.items():
            a, b = tuple(e)
            fa, fb = fs
            self.adj[fa].append((fb, (a, b)))
            self.adj[fb].append((fa, (a, b)))

    def _flatten_root(self, fi):
        """4x4 mapping world -> plane(z=0) for the root face fi."""
        n = self.s.face_normal(fi)
        R = _rot_align(n, np.array([0.0, 0.0, 1.0]))
        M = _mat4(R3=R)
        # translate so face sits on z=0
        v0 = _apply(M, self.s.vertices[self.s.faces[fi][0]])
        return _mat4(t=np.array([0.0, 0.0, -v0[2]])) @ M

    def _poly2d(self, M, fi):
        return [tuple(_apply(M, self.s.vertices[v])[:2]) for v in self.s.faces[fi]]

    def _child_matrix(self, parent_M, fa, fb, edge):
        """Matrix that places face fb coplanar with the net built around fa."""
        va, vb = edge
        P = self.s.vertices[va]
        Q = self.s.vertices[vb]
        dih = self.s.dihedral_angle(fa, fb)
        fold = math.radians(180.0 - dih)

        edge2 = (
            tuple(_apply(parent_M, P)[:2]),
            tuple(_apply(parent_M, Q)[:2]),
        )
        parent_c = _poly_centroid(self._poly2d(parent_M, fa))
        ps = _side(edge2[0], edge2[1], parent_c)

        best = None
        for ang in (fold, -fold):
            U = _rot_about_line(P, Q - P, ang)
            M = parent_M @ U
            poly = self._poly2d(M, fb)
            cc = _poly_centroid(poly)
            cs = _side(edge2[0], edge2[1], cc)
            # planarity check: all z near 0
            zmax = max(abs(_apply(M, self.s.vertices[v])[2]) for v in self.s.faces[fb])
            if zmax < 1e-6 and ps * cs < 0:
                best = M
                break
        if best is None:
            # fall back to the better of the two even if marginal
            best = M
        return best, edge2, dih

    def unfold(self, root_face=None, max_faces_per_piece=None):
        n_faces = len(self.s.faces)
        cap = max_faces_per_piece or n_faces
        # prefer to start pieces on pentagons (nice clusters of 5 triangles)
        order = sorted(range(n_faces), key=lambda f: (len(self.s.faces[f]) != 5, f))
        if root_face is not None:
            order = [root_face] + [f for f in order if f != root_face]

        unplaced = set(range(n_faces))
        placed_M = {}  # fi -> matrix (global, but each piece has own frame)
        placed_poly = {}  # fi -> 2D polygon
        piece_of = {}  # fi -> piece index
        hinge_records = []  # (piece, edge2, dihedral)
        pieces = []

        for seed in order:
            if seed not in unplaced:
                continue
            pidx = len(pieces)
            piece_faces = {}
            M = self._flatten_root(seed)
            placed_M[seed] = M
            poly = self._poly2d(M, seed)
            placed_poly[seed] = poly
            piece_faces[seed] = poly
            piece_of[seed] = pidx
            unplaced.discard(seed)

            # grow piece maximally with repeated passes
            changed = True
            while changed and len(piece_faces) < cap:
                changed = False
                for f in list(piece_faces.keys()):
                    if len(piece_faces) >= cap:
                        break
                    for nb, edge in self.adj[f]:
                        if nb not in unplaced:
                            continue
                        M_child, edge2, dih = self._child_matrix(
                            placed_M[f], f, nb, edge
                        )
                        cand = self._poly2d(M_child, nb)
                        cand_s = _shrink(cand, self.margin)
                        overlap = False
                        for of, opoly in piece_faces.items():
                            if of == f:
                                continue
                            if _convex_overlap(cand_s, _shrink(opoly, self.margin)):
                                overlap = True
                                break
                        if overlap:
                            continue
                        placed_M[nb] = M_child
                        placed_poly[nb] = cand
                        piece_faces[nb] = cand
                        piece_of[nb] = pidx
                        unplaced.discard(nb)
                        hinge_records.append((pidx, f, nb, edge2, dih))
                        changed = True

            pieces.append(piece_faces)

        # Assemble Piece objects with hinges + boundary edges.
        result = []
        hinge_by_piece = {}
        hinge_edge_set = {}  # piece -> set of frozenset(face pair) that are hinges
        for pidx, fa, fb, edge2, dih in hinge_records:
            hinge_by_piece.setdefault(pidx, []).append((edge2, dih, fa, fb))
            hinge_edge_set.setdefault(pidx, set()).add(frozenset((fa, fb)))

        for pidx, piece_faces in enumerate(pieces):
            piece = Piece()
            for fi, poly in piece_faces.items():
                piece.faces.append((fi, poly))

            for edge2, dih, fa, fb in hinge_by_piece.get(pidx, []):
                kind = "3-3" if (len(self.s.faces[fa]) == 3 and len(self.s.faces[fb]) == 3) else "3-5"
                piece.hinges.append(Hinge(edge2[0], edge2[1], dih, kind))

            # boundary edges: every face edge whose partner is NOT a hinge in this piece
            hset = hinge_edge_set.get(pidx, set())
            seen_boundary = set()
            for fi, poly in piece_faces.items():
                face = self.s.faces[fi]
                n = len(face)
                for k in range(n):
                    va, vb = face[k], face[(k + 1) % n]
                    nb = None
                    for cand_nb, e in self.adj[fi]:
                        if frozenset(e) == frozenset((va, vb)):
                            nb = cand_nb
                            break
                    if nb is not None and frozenset((fi, nb)) in hset:
                        continue  # this edge is an internal hinge
                    key = (fi, frozenset((va, vb)))
                    if key in seen_boundary:
                        continue
                    seen_boundary.add(key)
                    p2 = poly[k]
                    q2 = poly[(k + 1) % n]
                    dih = self.s.dihedral_angle(fi, nb) if nb is not None else 180.0
                    c = _poly_centroid(poly)
                    mid = ((p2[0] + q2[0]) / 2, (p2[1] + q2[1]) / 2)
                    inward = np.array([c[0] - mid[0], c[1] - mid[1]])
                    inward = inward / (np.linalg.norm(inward) + 1e-12)
                    kind = "3-3" if (nb is not None and len(self.s.faces[fi]) == 3 and len(self.s.faces[nb]) == 3) else "3-5"
                    piece.boundary.append(
                        Boundary(p2, q2, dih, tuple(inward), kind)
                    )
            result.append(piece)

        return result


if __name__ == "__main__":
    s = SnubDodecahedron()
    s.verify()
    pieces = Unfolder(s).unfold()
    total_faces = sum(len(p.faces) for p in pieces)
    print(f"pieces: {len(pieces)}, total faces: {total_faces}")
    for i, p in enumerate(pieces):
        x0, y0, x1, y1 = p.bbox()
        print(
            f"  piece {i}: {len(p.faces):2d} faces, "
            f"{len(p.hinges):2d} hinges, {len(p.boundary):2d} boundary, "
            f"size {x1 - x0:.2f} x {y1 - y0:.2f}"
        )
