# Real-world comparison: pymage vs. uv Dockerfiles

This is a hands-on study of migrating real, uv-based Python projects that ship a
`Dockerfile` to `pymage`. The goals were to (a) see how the resulting images
differ and (b) surface gaps/bugs real projects hit. It directly drove a
significant fix (runtime-only dependency resolution) and a ranked list of
migration blockers.

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

## Gaps & migration blockers (ranked)

1. **(FIXED) Installed the whole `uv.lock`, including dev groups.** Caused
   50–63% dependency bloat above. Now resolves the runtime closure.
2. **sdist-only dependencies are unsupported.** `imgpush` fails on
   `timeout-decorator==0.5.0`, which publishes no wheel:
   `uv.lock: timeout-decorator==0.5.0 has no wheels (sdist-only deps are not
   supported)`. uv/pip build the sdist into a wheel transparently; pymage
   consumes pre-built wheels only. This is a real blocker for projects with any
   wheelless dependency. *Possible fix: build sdists to wheels once (shell out to
   `uv`/`pip wheel`) and feed them into the existing layer path.*
3. **No system/OS packages.** pymage installs Python wheels, not apt/apk
   packages. `imgpush` needs `libmagickwand` (for `Wand`) and `nginx`; those must
   come from the base image. Projects with system-library dependencies must pick
   a base that already includes them — pymage can't `apt-get install`.
4. **No extras selection.** `imgpush`'s Dockerfile uses `uv sync --extra rembg`;
   pymage installs only the default runtime closure (no opt-in extras), so it
   can't reproduce that dependency set. *Possible fix: a `--extra` flag feeding
   the closure's root extras.*
5. **Defaulting to the base's platforms explodes on Docker Hub `python`.** That
   image advertises ~8 platforms (incl. `linux/arm/v7`, `linux/386`, `s390x`…),
   and pymage tried to build all of them, failing on
   `httptools` (no `cp312` `armv7l` wheel). Chainguard's base (amd64+arm64 only)
   is fine. *Possible fix: default the auto platform set to a curated common
   subset (amd64/arm64) or the host arch, and require explicit `--platform`
   otherwise.*
6. **No workspace / `--package` model.** `full-stack-fastapi-template` is a uv
   workspace; the Dockerfile builds one member (`--package app`). pymage treats
   all local packages as roots and unions their runtime closures — which happens
   to be correct here, but there's no way to target a single member or pass the
   lock at a different path cleanly.
7. **Environment markers aren't evaluated** in the closure, so a few
   marker-gated deps may be over-included. Bounded (wheel platform-filtering
   catches incompatibilities), but a runtime dep gated to a non-target platform
   with no compatible wheel would error.

## Verdict

After the runtime-closure fix, **pymage is a smaller, faster, reproducible
alternative to a uv Dockerfile for pure-wheel applications** (the FastAPI cases),
with no daemon and no build tooling in the image. It is **not yet a drop-in
replacement** for projects that need system libraries, depend on sdist-only
packages, or rely on optional extras — those are the highest-value items to
close next (sdist→wheel building and `--extra` support chief among them).
