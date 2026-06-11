# Real-world comparison: pymage vs. uv Dockerfiles

This is a hands-on study of migrating real, uv-based Python projects that ship a
`Dockerfile` to `pymage`. The goals were to (a) see how the resulting images
differ and (b) surface gaps/bugs real projects hit. It directly drove several
fixes — runtime-only dependency resolution, sdist→wheel building, `--extra` /
`--package` selection, and environment-marker evaluation — plus a ranked list of
the remaining migration blockers.

## Method & caveats

The study ran in a sandbox **without a Docker daemon**, and `cgr.dev`
(pymage's default base) was unreachable, so there is **no live `docker build` /
`docker run` head-to-head**. Instead, for each project pymage built the image
and pushed it to an in-process registry (`crane registry serve`); sizes and
layer counts were read from the resulting manifest. For an apples-to-apples
comparison every build used Docker Hub `python:3.x-slim` (matching the project's
`requires-python`) as the base and `--platform linux/amd64`; wheels were fetched
from PyPI.

Reported sizes are **compressed** layer sizes — the bytes a registry stores and
a client pulls. "deps" = the dependency (wheel) layers only; "total" includes
the shared base layers.

## Projects

| Project | Stack | Lock pkgs | Notes |
| --- | --- | --- | --- |
| [`astral-sh/uv-docker-example`](https://github.com/astral-sh/uv-docker-example) | FastAPI (`fastapi[standard]`) | 42 | Astral's canonical uv+Docker example; `src/` layout; `[project.scripts]` |
| [`fastapi/full-stack-fastapi-template`](https://github.com/fastapi/full-stack-fastapi-template) (backend) | FastAPI + SQLModel + psycopg + alembic | 88 | uv **workspace**; lock at repo root, `uv sync --package app` |
| [`hauxir/imgpush`](https://github.com/hauxir/imgpush) | Image service (opencv, nudenet, Wand, boto3) | 81 | Needs **system libs** (ImageMagick, nginx); `--extra rembg`; an sdist-only dep |

## Headline finding & fix: runtime-only dependency closure

pymage originally installed **every** package in `uv.lock`, including
dev-dependency groups and the whole resolution universe. For these projects that
meant shipping linters and type-checkers in the runtime image:

| Project | deps before | deps after | removed |
| --- | --- | --- | --- |
| uv-docker-example | 31.1 MB (41 wheels) | **15.4 MB (40 wheels)** | `ruff` (15.7 MB — **50%** of deps) |
| full-stack backend | 94.5 MB (87 wheels) | **34.7 MB (73 wheels)** | `ruff` (16), `mypy` (15), `ty` (12), `zizmor` (10), `prek` (5), `pytest`, `coverage`, `smokeshow` — **63%** of deps |

The fix makes `lock.ParseUVLockFile` install only the **runtime dependency
closure**: starting from the local/project (and workspace-member) packages'
`dependencies`, transitively following each package's `dependencies` and
expanding any requested extras via `optional-dependencies`, while **never**
following `dev-dependencies` groups. This matches `uv sync --no-dev` and is what
the example Dockerfile does with `UV_NO_DEV=1`. (A lock with no project package
— e.g. a bare requirements lock — still installs everything, unchanged.)

Final pymage images (after the fix, base `python:3.12-slim`/`python:3.10-slim`,
`linux/amd64`):

- **uv-docker-example:** 58.6 MB total, 45 layers (40 one-wheel layers + base + source).
- **full-stack backend:** 79.6 MB total, 78 layers.

## Where pymage already wins (pure-wheel apps)

For projects whose dependencies are all wheels (the FastAPI ones), pymage
produces images that are **leaner and more reproducible** than the typical uv
Dockerfile:

- **No build tooling in the image.** The uv Dockerfiles copy the `uv` binary in
  (or use a `uv`-preinstalled base). pymage's image is just `base + wheels +
  app source` — no `uv`, no pip, no apt layers.
- **No dev dependencies** (after the fix) — the canonical example's own
  Dockerfile ships `ruff` unless you remember `UV_NO_DEV`; pymage excludes it by
  default.
- **No `__pycache__`/`.pyc`.** pymage doesn't byte-compile (the Dockerfiles set
  `UV_COMPILE_BYTECODE=1`). Trade-off: smaller, deterministic layers vs. a
  slightly slower first import at runtime.
- **Reproducible & content-addressed**: same lock+source+base ⇒ same digest;
  one-wheel-per-layer means a single dependency bump re-pushes one small layer,
  and each layer is annotated (`dev.pymage.wheels`) with exactly what it
  contains.
- **Fast, docker-less builds** from any OS.

## Incremental rebuilds: where layering pays off

The study above compares *first* builds. The bigger day-to-day win is what
happens on the **second** build, after a small change. Because pymage puts each
wheel (or a stable hash-bucket of wheels) in its own content-addressed layer,
only the layers whose bytes actually changed need to be pushed; every other
layer is deduplicated by the registry (a `HEAD` that returns "already present").

Measured against `uv-docker-example` (44 layers: base + 40 wheels + app), pushing
both the original and modified image to the same registry and diffing the
resulting manifests:

| Change | Layers changed | Bytes re-uploaded |
| --- | --- | --- |
| Edit one app source file | **1 / 44** (the app layer) | **10.2 KB** |
| Bump one dependency (`six` 1.16.0 → 1.15.0) | **1 / N** (just that wheel's layer) | **11.3 KB** |

Contrast with the typical uv Dockerfile, which installs the whole environment in
one step:

```dockerfile
RUN --mount=... uv sync --frozen --no-install-project   # one big "deps" layer
COPY . /app                                             # app layer
```

- **Editing app code** invalidates the `COPY` layer in *both* approaches, so
  here they're comparable (both re-push only the small app layer).
- **Bumping a single dependency** re-runs `uv sync`, which rewrites the entire
  virtualenv — a *single* image layer containing **all** dependencies. The whole
  deps layer is a new blob and must be re-pushed and re-pulled: tens to hundreds
  of MB (≈15 MB for `uv-docker-example`, ≈35 MB for the FastAPI backend, ≈190 MB
  for `imgpush`). pymage re-uploads only the one changed wheel (≈11 KB here).

So the same one-line dependency bump costs the Dockerfile its full dependency
layer, while pymage uploads kilobytes. This mirrors the unit/e2e guarantees
(`TestAutoAddingDepChangesOneLayer`, `TestAutoVersionBumpChangesOneLayer`, and
the e2e no-bytes rebuild test): adding or bumping one dependency changes at most
one layer, and an unchanged input re-pushes nothing.

(The `auto` layer budget bin-packs wheels into ≤127 layers when there are more
wheels than the budget; a changed wheel then re-pushes only its bucket-mates, so
the worst case is one bucket rather than the whole environment.)

## Gaps & migration blockers (ranked)

1. **(FIXED) Installed the whole `uv.lock`, including dev groups.** Caused
   50–63% dependency bloat above. Now resolves the runtime closure.
2. **(FIXED) sdist-only dependencies.** `imgpush` previously failed on
   `timeout-decorator==0.5.0`, which publishes no wheel. pymage now builds a
   wheel from the sdist (`pip wheel --no-deps`) and feeds it into the existing
   layer path; the build host needs `python`/`pip`. Pure-python sdists build to
   `py3-none-any` (any target); compiled sdists build for the host platform only
   (cross-arch builds of those still error clearly). `imgpush` now builds with 52
   runtime wheels including the sdist-built `timeout-decorator`.
3. **No system/OS packages (by design — documented).** pymage installs Python
   wheels, not apt/apk packages. `imgpush` needs `libmagickwand` (for `Wand`) and
   `nginx`; those must come from the **base image**. Projects with system-library
   dependencies must pick/compose a base that already includes them — pymage
   can't `apt-get install`. See the README "Base image requirements" section.
4. **(FIXED) Extras selection.** `--extra <group>` enables a project's own
   `[project.optional-dependencies]` group (e.g. `imgpush`'s `rembg`), feeding the
   closure's roots. Extras requested by dependencies (`fastapi[standard]`) were
   already followed.
5. **Defaulting to the base's platforms explodes on Docker Hub `python`.** That
   image advertises ~8 platforms (incl. `linux/arm/v7`, `linux/386`, `s390x`…),
   and pymage tried to build all of them, failing on
   `httptools` (no `cp312` `armv7l` wheel). Chainguard's base (amd64+arm64 only)
   is fine. *Possible fix: default the auto platform set to a curated common
   subset (amd64/arm64) or the host arch, and require explicit `--platform`
   otherwise.* (Marker evaluation, item 7, mitigates the *marker-gated* slice of
   this; wheel availability for the arch is still required.)
6. **(FIXED) Workspace / `--package` model.** `full-stack-fastapi-template` is a
   uv workspace; the Dockerfile builds one member (`uv sync --package app`).
   `--package <name>` now roots the closure at a single workspace member;
   omitting it unions all members (the prior behavior).
7. **(FIXED) Environment markers are now evaluated** per target. Dependency
   markers (`sys_platform`, `platform_machine`, `python_version`, `os_name`, …,
   with `and`/`or`/`in`/comparisons) are evaluated against the platform and
   interpreter being built, so marker-gated deps are included only when they
   apply (and per-arch for multi-arch builds). Unparseable markers conservatively
   include the dependency rather than drop it.

## Verdict

After the runtime-closure fix and this round of work, **pymage is a smaller,
faster, reproducible alternative to a uv Dockerfile** for the projects studied:
all three now build (including `imgpush`, via sdist building), with no daemon and
no build tooling in the image, and incremental rebuilds re-upload only changed
layers. The main remaining caveat is **runtime system libraries**, which must be
provided by the base image (documented), plus the multi-platform default for
bases that advertise many architectures (item 5).
