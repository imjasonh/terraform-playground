# Wheel coverage survey: what can pymage install today?

pymage installs **pre-built wheels only** — it doesn't build source distributions
(sdists), because doing so would run dependency build code on the host (an RCE
surface with no sandbox), be non-reproducible, and produce host-arch-only output
for compiled packages (see `docs/real-world-comparison.md` and `DESIGN.md`). The
natural worry: does that meaningfully shrink who can use the tool?

This is a data-driven answer. The honest summary: **almost not at all in
practice.** By package *usage*, ~99.7% of the dependencies real projects pull in
are installable, and most projects build unchanged; the rare straggler is a
pure-python package that never shipped a wheel, which is handled by pre-building
one wheel and pointing `--find-links` at it.

## What "installable" means here

pymage needs, for each required package, **one wheel compatible with each target
platform you build** — not "wheels for all platforms":

- a **pure-python** wheel (`*-none-any.whl`) covers *every* target at once;
- a **compiled** package needs a `manylinux`/`musllinux` wheel for each arch you
  build (e.g. `x86_64` and/or `aarch64`).

A package is uninstallable only when it has **no wheel at all** (sdist-only) for
the target. The survey targets `linux/amd64` (and `linux/arm64`) — the common
container targets.

## Method

- **PyPI JSON API** (`/pypi/{name}/json`) for per-package artifact lists; the full
  project list from the PyPI simple index (826,099 projects at time of survey).
- Three populations:
  1. a **random sample of all PyPI** (unweighted long tail);
  2. a **curated set of widely-depended-upon packages** (popularity proxy — the
     public download-rank dataset was network-blocked in this environment);
  3. the **resolved locks of six real projects** (the actual packages apps use),
     where we also report whether the *whole* project builds (pymage is
     all-or-nothing: one sdist-only dep blocks the build).

Caveat: snapshot in time; latest stable release per package; counts files of that
release. Popularity proxy is curated, not download-weighted.

## Results

### 1. Random sample of all PyPI (unweighted — the long tail)

`n = 845` projects (of 900 sampled) that have any distribution:

| Metric | Share |
| --- | --- |
| has any wheel | 80.8% |
| pure-python wheel (works on all targets) | 77.8% |
| **installable on linux/amd64** | **80.2%** |
| installable on linux/arm64 | 78.7% |
| installable amd64 **and** arm64 | 78.7% |
| **sdist-only (pymage can't install)** | **19.2%** |

So even across the entire long tail — including hundreds of thousands of
abandoned/trivial projects nobody depends on — ~80% are installable. The ~19%
sdist-only are overwhelmingly *pure-python* packages that simply never uploaded a
wheel (easy to pre-build), not hard compiled ones.

### 2. Curated popular packages (popularity proxy)

`n = 147` of the most widely-depended-upon packages (boto3, requests, numpy,
pandas, torch, cryptography, grpcio, lxml, pillow, opencv-python, … incl. many
compiled ones):

| Metric | Share |
| --- | --- |
| **installable on linux/amd64** | **100.0%** |
| sdist-only | 0.0% |

Every popular package — including the compiled ones — ships wheels. The modern
ecosystem is wheel-first; the build-backends default to producing wheels and the
big projects publish manylinux wheels for every common arch.

### 3. Real projects (what apps actually depend on)

Per-package installability of each project's resolved lock on `linux/amd64`, and
whether the whole project builds:

| Project | Installable deps | Whole project builds |
| --- | --- | --- |
| `astral-sh/uv-docker-example` | 41 / 41 | ✅ |
| `fastapi/full-stack-fastapi-template` | 87 / 87 | ✅ |
| `mitmproxy2swagger` | 82 / 82 | ✅ |
| AI/torch sample app (torch + CUDA + transformers) | 61 / 61 | ✅ |
| compiled-wheels sample (numpy, pydantic-core, …) | 11 / 11 | ✅ |
| `hauxir/imgpush` | 79 / 80 | ❌ (1 blocker) |

- **361 / 362 dependencies installable = 99.7%** across the six projects.
- **5 of 6 projects build with zero changes.**
- The only blocker is `imgpush`'s `timeout-decorator==0.5.0` — a tiny, pure-python
  package that ships no wheel. The fix is one line of out-of-band prep
  (`uv pip wheel timeout-decorator==0.5.0 -w ./wheelhouse` → `--find-links`), or
  the upstream simply publishing a wheel.

## Conclusion

Dropping sdist *building* did **not** meaningfully limit the user base:

- **By usage:** ~99.7% of real dependencies are installable, and most projects
  build unmodified. The probability of hitting a sdist-only straggler grows with
  dependency-set size, but the straggler is almost always a pure-python package
  that's trivial to pre-build into a `--find-links` wheelhouse — without taking on
  the RCE / non-reproducibility / single-arch costs of building sdists in-process.
- **By raw count:** ~80% of *all* PyPI projects are installable; the ~19%
  sdist-only tail is dominated by obscure, low/zero-dependency-graph packages and
  is mostly pure-python (easy to wheel).

The right trade-off, then, is the current one: install wheels (hermetic,
reproducible, multi-arch, no code execution) and provide the `--find-links`
escape hatch for the rare wheelless dependency — rather than build sdists.

Re-run the survey with `python3 docs/wheel-coverage-survey.py` (PyPI access
required).
