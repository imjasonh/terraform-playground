"""
Exact geometry of the snub dodecahedron (an Archimedean solid).

The snub dodecahedron has:
  - 60 vertices
  - 150 edges
  - 92 faces: 12 regular pentagons + 80 equilateral triangles
  - every vertex has configuration 3.3.3.3.5

We build it from the exact Cartesian coordinates (Wikipedia / Weisstein),
take the convex hull, merge coplanar hull triangles back into the real
pentagon/triangle faces, and expose:

  - vertices            (60, 3)            float array, normalised to edge length 1
  - faces               list[list[int]]    CCW (outward) vertex-index loops
  - edges               dict[(i,j)] -> (faceA, faceB)
  - dihedral_angle(...) interior dihedral angle of an edge (degrees)

Everything is verified at import time (see `build_snub_dodecahedron`):
all 150 edges are equal length, there are exactly 12 pentagons and 80
triangles, and the dihedral angles match the known values
(3-3: 164.175 deg, 3-5: 152.930 deg).
"""

from __future__ import annotations

import itertools
import math

import numpy as np
from scipy.spatial import ConvexHull

PHI = (1.0 + math.sqrt(5.0)) / 2.0


def _xi() -> float:
    """Real root of xi^3 - 2*xi = phi."""
    # Solve xi^3 - 2 xi - phi = 0 for the single real positive root.
    coeffs = [1.0, 0.0, -2.0, -PHI]
    roots = np.roots(coeffs)
    real = [r.real for r in roots if abs(r.imag) < 1e-9]
    # The relevant root is the largest real one (~1.7155615).
    return max(real)


def _base_points() -> np.ndarray:
    """The five generating points before permutation / sign expansion."""
    xi = _xi()
    a = xi - 1.0 / xi
    b = xi * PHI + PHI * PHI + PHI / xi
    p = PHI
    pts = [
        (2 * a, 2.0, 2 * b),
        (a + b / p + p, -a * p + b + 1.0 / p, a / p + b * p - 1.0),
        (a + b / p - p, a * p - b + 1.0 / p, a / p + b * p + 1.0),
        (-a / p + b * p + 1.0, -a + b / p - p, a * p + b - 1.0 / p),
        (-a / p + b * p - 1.0, a - b / p - p, a * p + b + 1.0 / p),
    ]
    return np.array(pts, dtype=float)


def _even_permutations(v):
    """The 3 even (cyclic) permutations of a 3-vector."""
    x, y, z = v
    return [(x, y, z), (y, z, x), (z, x, y)]


def _sign_variants(v):
    """Sign assignments with an even number of minus signs (0 or 2)."""
    x, y, z = v
    out = []
    for sx, sy, sz in itertools.product((1, -1), repeat=3):
        if (sx * sy * sz) == 1:  # product +1  <=> even number of minus signs
            out.append((sx * x, sy * y, sz * z))
    return out


def _all_vertices() -> np.ndarray:
    seen = []
    for base in _base_points():
        for perm in _even_permutations(base):
            for sv in _sign_variants(perm):
                seen.append(sv)
    pts = np.array(seen, dtype=float)
    # De-duplicate (there should be exactly 60 distinct points).
    uniq = []
    for p in pts:
        if not any(np.allclose(p, q, atol=1e-6) for q in uniq):
            uniq.append(p)
    return np.array(uniq, dtype=float)


def _merge_coplanar_faces(points: np.ndarray, hull: ConvexHull):
    """Group hull simplices (triangles) into the real polygonal faces.

    Two adjacent simplices belong to the same face iff they are coplanar
    (same outward normal). Returns a list of faces, each an ordered
    (CCW seen from outside) list of vertex indices.
    """
    n_simp = len(hull.simplices)

    # Outward unit normal for each simplex.
    normals = hull.equations[:, :3]
    offsets = hull.equations[:, 3]

    # Union-find over simplices that share an edge and are coplanar.
    parent = list(range(n_simp))

    def find(i):
        while parent[i] != i:
            parent[i] = parent[parent[i]]
            i = parent[i]
        return i

    def union(i, j):
        parent[find(i)] = find(j)

    # Map each undirected triangle edge -> simplices that contain it.
    from collections import defaultdict

    edge_to_simps = defaultdict(list)
    for si, tri in enumerate(hull.simplices):
        for a, b in ((tri[0], tri[1]), (tri[1], tri[2]), (tri[2], tri[0])):
            edge_to_simps[frozenset((int(a), int(b)))].append(si)

    for simps in edge_to_simps.values():
        if len(simps) == 2:
            s0, s1 = simps
            if (
                np.allclose(normals[s0], normals[s1], atol=1e-6)
                and abs(offsets[s0] - offsets[s1]) < 1e-6
            ):
                union(s0, s1)

    groups = defaultdict(list)
    for si in range(n_simp):
        groups[find(si)].append(si)

    faces = []
    for simps in groups.values():
        vids = set()
        for si in simps:
            vids.update(int(v) for v in hull.simplices[si])
        vids = list(vids)
        normal = normals[simps[0]]
        center = points[vids].mean(axis=0)
        # Build an in-plane basis to order vertices by angle.
        ref = points[vids[0]] - center
        ref = ref / np.linalg.norm(ref)
        binormal = np.cross(normal, ref)

        def angle(vi):
            d = points[vi] - center
            return math.atan2(float(d @ binormal), float(d @ ref))

        vids.sort(key=angle)
        # Ensure CCW with respect to the OUTWARD normal.
        ordered = np.array([points[v] for v in vids])
        area_vec = np.zeros(3)
        for i in range(len(ordered)):
            area_vec += np.cross(ordered[i], ordered[(i + 1) % len(ordered)])
        if area_vec @ normal < 0:
            vids.reverse()
        faces.append(vids)
    return faces


class SnubDodecahedron:
    def __init__(self):
        pts = _all_vertices()
        assert len(pts) == 60, f"expected 60 vertices, got {len(pts)}"

        hull = ConvexHull(pts)
        # Normalise so that edge length == 1.
        faces = _merge_coplanar_faces(pts, hull)

        # Measure a representative edge length and rescale.
        i, j = faces[0][0], faces[0][1]
        edge_len = np.linalg.norm(pts[i] - pts[j])
        pts = pts / edge_len

        self.vertices = pts
        self.faces = faces

        # Build edge -> faces map.
        from collections import defaultdict

        edge_faces = defaultdict(list)
        for fi, face in enumerate(faces):
            n = len(face)
            for k in range(n):
                a, b = face[k], face[(k + 1) % n]
                edge_faces[frozenset((a, b))].append(fi)
        self.edge_faces = edge_faces

    def face_normal(self, fi):
        face = self.faces[fi]
        v = self.vertices[face]
        c = v.mean(axis=0)
        n = np.cross(v[1] - v[0], v[2] - v[0])
        n = n / np.linalg.norm(n)
        # Make it point outward (away from origin, which is the centroid).
        if n @ c < 0:
            n = -n
        return n

    def dihedral_angle(self, fa, fb):
        """Interior dihedral angle between two faces (degrees)."""
        na, nb = self.face_normal(fa), self.face_normal(fb)
        ang_between_normals = math.degrees(
            math.acos(max(-1.0, min(1.0, float(na @ nb))))
        )
        return 180.0 - ang_between_normals

    # ---- verification -------------------------------------------------
    def verify(self):
        nv = len(self.vertices)
        nf = len(self.faces)
        ne = len(self.edge_faces)
        tris = sum(1 for f in self.faces if len(f) == 3)
        pents = sum(1 for f in self.faces if len(f) == 5)
        assert nv == 60, nv
        assert nf == 92, nf
        assert ne == 150, ne
        assert tris == 80, tris
        assert pents == 12, pents

        # All edges equal length.
        lengths = []
        for e in self.edge_faces:
            a, b = tuple(e)
            lengths.append(np.linalg.norm(self.vertices[a] - self.vertices[b]))
        lengths = np.array(lengths)
        assert lengths.std() < 1e-6, lengths.std()
        assert abs(lengths.mean() - 1.0) < 1e-6, lengths.mean()

        # Every edge shared by exactly 2 faces.
        assert all(len(v) == 2 for v in self.edge_faces.values())

        # Dihedral angles.
        d33 = []
        d35 = []
        for e, (fa, fb) in self.edge_faces.items():
            la, lb = len(self.faces[fa]), len(self.faces[fb])
            d = self.dihedral_angle(fa, fb)
            if la == 3 and lb == 3:
                d33.append(d)
            else:
                d35.append(d)
        # There are no pentagon-pentagon edges.
        assert len(d35) == 60, len(d35)  # 12 pentagons * 5 edges
        assert len(d33) == 90, len(d33)  # remaining
        m33, m35 = float(np.mean(d33)), float(np.mean(d35))
        assert abs(m33 - 164.1750) < 0.01, m33
        assert abs(m35 - 152.9299) < 0.01, m35
        return {
            "vertices": nv,
            "faces": nf,
            "edges": ne,
            "triangles": tris,
            "pentagons": pents,
            "edge_len": float(lengths.mean()),
            "dihedral_3_3": m33,
            "dihedral_3_5": m35,
        }


def build_snub_dodecahedron() -> SnubDodecahedron:
    s = SnubDodecahedron()
    s.verify()
    return s


if __name__ == "__main__":
    s = build_snub_dodecahedron()
    import json

    print(json.dumps(s.verify(), indent=2))
