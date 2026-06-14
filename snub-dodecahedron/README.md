# Foldable snub dodecahedron net

Generate a **3D-printable flat net** of a [snub
dodecahedron](https://en.wikipedia.org/wiki/Snub_dodecahedron) that you print
in one (or a few) flat pieces, then **fold along beveled hinges and glue** into
the finished Archimedean solid.

This was inspired by the Wikipedia net
[`Snub_dodecahedron_flat.svg`](https://commons.wikimedia.org/wiki/File:Snub_dodecahedron_flat.svg).
Rather than tracing that bitmap-ish SVG, the geometry here is generated from the
**exact** coordinates of the snub dodecahedron, so every face is a perfect
regular polygon and every fold angle is computed from the solid's true dihedral
angles.

| The flat net (one piece) | A single fold groove | Folds into this |
|---|---|---|
| ![net](images/net.png) | ![groove](images/groove_profile.png) | ![solid](images/solid.png) |

The 92 faces (12 pentagons + 80 triangles) unfold into a **single
non-overlapping net** with 91 fold hinges and 59 glue seams. Pentagons are
shown orange, triangles blue; green fold lines are triangle–triangle edges and
magenta fold lines are triangle–pentagon edges.

The actual generated 3D plate (grooves cut on the inner side):

![3D plate](images/plate_3d.png)

## How the folding works

The net prints as a flat plate of thickness `T`:

```
 z = T   outer surface of the finished solid  (stays continuous over folds)
 z = 0   inner surface                        (the V-grooves open here)
```

To fold two adjacent panels from flat (180°) to the solid's interior dihedral
angle `θ`, a V-groove is removed from the **inner** side along the shared edge.
When the groove closes, the panels sit at `θ`. A thin **web** of material is
left at the top as a living hinge.

```
fold angle  = 180° − θ
each groove wall is cut at   γ = (fold + slack) / 2   from vertical
groove half-width at base    w = (T − web) · tan(γ)
```

The snub dodecahedron has exactly two distinct dihedral angles, so two groove
sizes are produced automatically:

| Edge type | Dihedral `θ` | Fold (180−θ) |
|---|---|---|
| triangle–triangle | **164.175°** | 15.825° |
| triangle–pentagon | **152.930°** | 27.070° |

`slack` (default **6°**) makes every groove slightly wider than strictly
necessary. The panels can then always reach `θ` — in fact they can over-close
by `slack` degrees — so any small gap at the target angle is simply filled with
glue. This follows the "leave more room than necessary" bias: it is far easier
to fill a gap with glue than to fight a groove that closes too early.

Glue seams (edges where two far-apart parts of the net meet only after folding)
are chamfered on each mating face by the same `γ`, so the two faces end up
coplanar and glue flush at the correct angle.

## Requirements

This project is run with [uv](https://docs.astral.sh/uv/). The Python scripts
carry [inline dependency metadata (PEP 723)](https://docs.astral.sh/uv/guides/scripts/),
so `uv run` automatically creates an isolated environment with `numpy`/`scipy`
on first use — there is nothing to install manually.

```bash
# install uv (see https://docs.astral.sh/uv/getting-started/installation/)
curl -LsSf https://astral.sh/uv/install.sh | sh    # or: pip install uv

# OpenSCAD is only needed to render STLs (the .scad is written regardless)
sudo apt-get install openscad                      # or https://openscad.org
```

## Usage

```bash
# default: 30 mm edges, 3 mm thick, single net, writes out/snub_net.scad + preview
uv run generate.py

# pick your own size/depth and also render an STL
uv run generate.py --edge 35 --depth 4 --web 0.6 --slack 6 --stl

# split into smaller pieces (≤ 20 faces each) for easier printing/handling
uv run generate.py --max-faces-per-piece 20 --stl

# also export the target solid as a reference model
uv run generate.py --solid
```

Open the generated `.scad` in OpenSCAD (or use `--stl`) to get a mesh, then
slice and print.

### Parameters

| Flag | Default | Meaning |
|---|---|---|
| `--edge` | `30` | edge length of every polygon (mm) — sets overall size |
| `--depth` / `--thickness` | `3` | plate depth / material thickness `T` (mm) |
| `--web` | `0.6` | living-hinge web left at the top of each groove (mm) |
| `--slack` | `6` | extra fold opening added to every groove (degrees) |
| `--over` | `0.4` | run grooves this far past each vertex for a clean fold (mm) |
| `--max-faces-per-piece` | — | split the net into pieces of at most N faces |
| `--no-boundary-bevel` | off | leave glue-seam edges square instead of chamfered |
| `--solid` | off | also export the reference snub dodecahedron solid |
| `--stl` | off | render an STL via the `openscad` CLI |
| `--out` | `out/snub_net` | output path prefix |

## Printing & assembly

1. **Orientation:** print the plate **inner (grooved) side down** on the bed.
   Every bevel wall is within ~`γ` of vertical (< ~20°), so the grooves are
   self-supporting — **no support material needed**.
2. **Material:** PLA/PETG work. The `web` is a living hinge: with a single fold
   it survives easily. For repeated folding or brittle filament, increase
   `--web`, or reduce it toward ~0.4 mm for an easier fold.
3. **Fold** each hinge until the groove walls meet (or until the faces look
   right). The grooves are valley folds toward the inner side.
4. **Glue** the 59 seams (and the hinge grooves, if you want a rigid result).
   The chamfered seam faces meet flush; fill any slack gap with glue.

Tip for choosing a size: the finished solid's circumradius is about
`2.16 × edge`, so its overall diameter is about `4.31 × edge`. So 30 mm edges
give a ball roughly 130 mm across.

## Files

| File | Purpose |
|---|---|
| `snubgeom.py` | exact snub dodecahedron geometry; self-verifies counts, edge lengths and dihedral angles |
| `unfold.py` | unfolds the solid into non-overlapping flat net piece(s) |
| `scadgen.py` | turns a net into beveled, grooved OpenSCAD |
| `preview.py` | SVG preview of the net + independent overlap check |
| `generate.py` | command-line driver tying it all together |

## Verification

Running any of the scripts re-verifies the geometry: 60 vertices, 150 edges,
92 faces (12 pentagons + 80 triangles), all edges equal length, and dihedral
angles matching the known 164.175° / 152.930°. The unfolder additionally runs
an independent SAT overlap check to confirm no two faces overlap in the layout.

```bash
uv run snubgeom.py     # prints the verified geometry stats
uv run preview.py      # writes net_preview.svg, reports overlapping pairs (0)
```
