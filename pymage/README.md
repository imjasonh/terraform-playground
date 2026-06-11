# `pymage`

A **docker-less**, layer-aware container image builder for Python applications,
in the spirit of [`ko`](https://github.com/ko-build/ko). It builds and pushes
OCI images without a Docker daemon by composing content-addressed layers with
[`go-containerregistry`](https://github.com/google/go-containerregistry).

See [`DESIGN.md`](./DESIGN.md) for the full rationale.

## What makes it efficient

- **One deterministic layer per wheel.** Installing a wheel produces a
  byte-identical layer every time, so its digest is stable.
- **No-bytes rebuilds.** Because layers are content-addressed and the builder is
  reproducible, re-pushing an unchanged image transfers **zero** dependency
  bytes — the registry already has every blob (verified by a `HEAD` check).
- **Only new dependencies upload.** Adding a dependency creates exactly one new
  layer; every existing dependency layer keeps its digest and is skipped.
- **App code is a thin top layer**, so the common edit-rebuild loop only moves a
  small layer (and the manifest).
- **Reproducible**: same lock + same source + same base ⇒ same image digest.

This pays off most on **large AI/GPU images**, where modern `torch` wheels carry
the CUDA runtime (`nvidia-*` wheels) and the dependencies are ~98% of the image.
Bumping one dependency re-uploads a single ~11 MB layer instead of re-pushing the
whole ~2.9 GB `uv sync` venv layer — see
[`docs/ai-image-comparison.md`](./docs/ai-image-comparison.md). For pure-wheel
web apps see [`docs/real-world-comparison.md`](./docs/real-world-comparison.md).

## Usage (uv projects)

pymage is designed for [uv](https://docs.astral.sh/uv/) projects. Configure it
once in `pyproject.toml` (in the spirit of [`ko`](https://github.com/ko-build/ko),
the destination repo lives in config, not on the command line):

```toml
[tool.pymage]
repo = "registry.example.com/me/myapp"   # pushed by digest
base = "cgr.dev/chainguard/python:latest"
platforms = ["linux/amd64", "linux/arm64"]
```

Then, from the project root:

```
pymage build              # builds + pushes registry.example.com/me/myapp:latest
pymage build -t v1.2.3    # ...:v1.2.3 (and -t is repeatable for multiple tags)
pymage build ./example    # build a different project directory (positional arg)
```

The project directory is the first positional argument (default: the current
directory).

`pymage` always publishes **by digest** and prints the resulting `repo@sha256:…`
as the **only** thing on stdout — progress (per-blob `pushed`/`existing`/`mounted`
logs, per-tag pointers, diagnostics) goes to stderr — so you can run the image
directly:

```
docker run "$(pymage build)"
```

`-t/--tag` is the **tag component only**, never a full reference — the repo comes
from `[tool.pymage] repo` (or `--repo`). If no `-t` is given it uses
`[tool.pymage] tags`, defaulting to `latest`.

### Configuration

`[tool.pymage]` keys mirror the build flags; an explicit flag always overrides
the config value, which overrides the built-in default.

| Key | Flag | Default |
| --- | --- | --- |
| `repo` | `--repo` | *(required to push)* |
| `tags` | `-t`/`--tag` (repeatable) | `["latest"]` |
| `base` | `--base` | `cgr.dev/chainguard/python:latest` |
| `platforms` | `--platform` | the platforms the **base image** supports |
| `layer-strategy` | `--layer-strategy` | `auto` |
| `max-layers` | `--max-layers` | `127` |
| `max-wheel-layers` | `--max-wheel-layers` | *(derived from `max-layers`)* |
| `push-concurrency` | `--push-concurrency` | auto (≥ 4, scales with CPUs) |
| `no-cache` | `--no-cache` | `false` (caching is on by default) |
| `extras` | `--extra` (repeatable) | — (enables uv project optional-dependency groups) |
| `package` | `--package` | — (build a single uv workspace member) |
| `python` | `--python` | auto-detected from the base |
| `prefix` | `--prefix` | `/app/.venv` |
| `workdir` | `--workdir` | `/app` |
| `user` | `--user` | *(base default)* |
| `entrypoint` | `--entrypoint` | `[project.scripts]` console script |
| `cmd` | `--cmd` | — |
| `env` | `--env` | `PYTHONPATH=/app/src` when `src/` exists |
| `labels` | `--label` | — |
| `find-links` | `--find-links` | download wheels from the lock |

Other defaults: the source directory is the first positional argument (default
`.`); the lock is `uv.lock` in that directory (falling back to
`requirements.txt`). Wheels are fetched over the network from the lock URLs on
first use and cached by SHA-256 in `~/.cache/pymage/wheels`; set `find-links` to
a local wheel directory for offline / air-gapped builds.

See [`example/`](./example/) for a FastAPI app with a `[tool.pymage]` table you
can build as-is:

```
go run . build ./example --repo localhost:5000/example -t latest --insecure
```

### Multi-arch

When `--platform` is omitted, pymage builds for **exactly the platforms the base
image supports** — so a multi-arch base (e.g. `cgr.dev/chainguard/python`, which
ships `linux/amd64` + `linux/arm64`) produces a multi-arch image **index** with
no extra flags, and a single-arch base produces a single image. You can override
this by listing platforms explicitly (in config or via `--platform`):

```
pymage build                                   # match the base's platforms
pymage build --platform linux/amd64,linux/arm64 -t latest
```

Building more than one platform assembles one image per platform into an OCI
index. Because no Docker daemon is involved, this works from any host OS — Linux,
macOS, or Windows. Each platform selects its own compatible wheels from `uv.lock`
(pure-python wheels are shared across arches).

### Layer budget (`auto`)

By default (`layer-strategy = "auto"`) pymage keeps **one layer per wheel** for
maximum reuse, as long as the total image stays within a layer budget — `127`
layers by default (`max-layers`, counting the base image's layers, the
dependency layers, and the app source layer). Set `max-wheel-layers` to cap the
dependency layers directly.

Each dependency layer records the wheels it installs both in the image's config
history and as an OCI layer-descriptor annotation (`dev.pymage.wheels`,
comma-separated `name==version`), so `crane manifest` / `docker inspect` show
exactly which wheels each layer contains — including packed multi-wheel layers.

When there are more wheels than the budget allows, pymage **bin-packs** them by
hashing each distribution's (normalized) name to a stable bucket. Because a
wheel's bucket depends only on its name, adding, removing, or version-bumping a
single dependency only changes **that one bucket's layer** — every other layer
keeps its digest and is reused. (`per-wheel` forces one layer per wheel with no
cap; `single-deps-layer` puts everything in one layer.)

### Hashed requirements.txt (pip-compile / uv pip compile)

The original lock format is still supported (flags work in place of, or on top
of, a `[tool.pymage]` table):

```
pymage build \
  --lock requirements.txt \
  --find-links ./wheelhouse \
  --entrypoint python --entrypoint -m --entrypoint myapp \
  --repo registry.example.com/me/myapp -t latest
```

### Optional dependencies, workspaces, and markers (uv.lock)

pymage installs the project's **runtime closure** from `uv.lock` (the deps you'd
get from `uv sync --no-dev`), not every package in the lock. When `uv` is
installed and a `pyproject.toml` is present, resolution is delegated to
`uv export --frozen --no-dev` — uv is the source of truth for the closure,
extras, groups, and workspace selection, so pymage doesn't reimplement its
resolver. pymage then evaluates the environment markers uv emits for the target
and attaches wheel URLs from the lock. **`uv` is therefore required to build
from a `uv.lock`** — if it isn't installed the build fails (rather than falling
back to a second, potentially divergent resolver); as an alternative, export a
hashed `requirements.txt` and build with `--lock requirements.txt --find-links`.
The selectors below map onto `uv export` flags:

- `--extra <group>` enables one of the project's own
  `[project.optional-dependencies]` groups (repeatable). Extras requested *by*
  your dependencies (e.g. `fastapi[standard]`) are always followed.
- `--package <name>` roots the closure at a single uv **workspace member**
  instead of the union of all members — useful for monorepos that build several
  images from one lock.
- **Environment markers are evaluated for the target.** A dependency gated on
  `sys_platform == 'win32'` or `python_version < '3.11'` is included only when it
  applies to the platform/interpreter being built, so Linux images don't carry
  Windows-only or stale-Python-only packages. Markers are evaluated per platform,
  so each arch of a multi-arch build gets the correct set.

### Source distributions (sdists)

pymage installs **pre-built wheels only — it does not build sdists.** This is a
deliberate choice: building a source distribution runs the dependency's own build
code (`setup.py` / build backend) on the build host, which would break pymage's
core guarantees — it's not hermetic or byte-reproducible, it's a remote-code-
execution surface with no container to sandbox it, it needs a build toolchain,
and compiled packages can only be built for the host architecture (no multi-arch).

In practice this is rarely an issue: the modern ecosystem is wheel-first, so
mainstream dependencies on common targets all publish wheels. You only hit an
sdist for (a) older, low-maintenance pure-python packages that never uploaded a
wheel, (b) compiled packages on a brand-new Python or uncommon platform before
wheels are published, or (c) the occasional source-only package.

If the lock pins a package with no compatible wheel, the build fails fast and
tells you exactly how to proceed: **pre-build the wheel out-of-band with your own
(trusted, ideally isolated) tooling and point `--find-links` at it.**

```
# Build wheels once, with isolation/tooling you control:
uv pip wheel -r requirements.txt -w ./wheelhouse   # or: pip wheel ...

# Then build the image from the local wheelhouse:
pymage build --find-links ./wheelhouse -t latest
```

This keeps the escape hatch for the rare cases while keeping pymage's builds
hermetic, reproducible, multi-arch, and free of arbitrary build-time code. In
practice the wheels-only constraint costs very little: a survey of real projects
found **~99.7% of the dependencies apps actually use** are installable, and most
projects build unchanged (see [`docs/wheel-coverage-survey.md`](./docs/wheel-coverage-survey.md)).

### Base image requirements (OS / system libraries)

pymage installs Python wheels on top of the base image; it does **not** install
OS packages. The base image must already provide everything your dependencies
need at runtime beyond the interpreter and pure-Python code, including:

- the Python interpreter and standard library (matching the lock's `cp` tags);
- shared system libraries that compiled wheels link against (e.g. `libffi`,
  `libssl`, `libstdc++`, `libgomp` for some ML wheels); and
- non-Python runtime tools your app shells out to (e.g. ImageMagick for Wand,
  `ffmpeg`, `git`).

Choose (or build) a base that bundles these. For Debian-style bases that means a
variant with the libraries preinstalled; for Chainguard/Wolfi, compose a base
with the needed `apk` packages. If a dependency needs a system library the base
lacks, the image builds fine but fails at runtime — pymage can't add `apt`/`apk`
packages for you.

### Choosing a base image

The base is an input to the build, so it affects reproducibility just like the
lock and source do. **Pin it by digest** (e.g.
`cgr.dev/chainguard/python@sha256:…`) for stable, no-bytes rebuilds.

A floating tag such as `cgr.dev/chainguard/python:latest` works, but be aware:

- it makes the base an *uncontrolled input*, so rebuilds aren't reproducible and
  may push fresh base layers whenever the tag moves; and
- `:latest` slides forward across Python **minor versions**. Pure-python wheels
  keep working (they're matched by `py3` and found via `PYTHONPATH`), but
  version-specific compiled wheels (`cp312`…) break when the interpreter moves.

pymage detects the base's Python version and uses it automatically, so
`--python` is usually unnecessary. Detection looks at the `PYTHON_VERSION` env
var (official python images) and, when that's absent, the `python-X.Y` package
in `/etc/apko.json` from the top layer (Chainguard/Wolfi images). If you do pass
`--python`, it **must match** the detected base version or the build fails fast
(catching a floating tag that slid to a different Python). Bases that expose
neither signal can't be auto-detected — pass `--python` explicitly, or pin a
base that advertises its version.

### Useful flags

| Flag | Description |
| --- | --- |
| `--push=false` | Build without pushing (combine with `--oci-layout`). |
| `--oci-layout DIR` | Also write the image to an OCI layout directory. |
| `--print-digest` | Print only the resulting image digest (no push). |
| `--sbom PATH` | Write a CycloneDX SBOM of the resolved wheels. |
| `--layer-strategy` | `auto` (default), `per-wheel`, or `single-deps-layer`. |
| `--max-layers` | Cap on total image layers (base + deps + app) for `auto` (default 127). |
| `--max-wheel-layers` | Cap the dependency layer count directly (overrides `--max-layers`). |
| `--push-concurrency` | Max concurrent layer uploads when pushing (0 = auto). |
| `--platform` | Target platform(s); selects compatible wheels and base. Repeatable / comma-separated (e.g. `linux/amd64,linux/arm64`) builds a multi-arch image index. Defaults to the platforms the base image supports. |
| `--python` | Interpreter version, e.g. `python3.12`. Optional — **auto-detected from the base** when omitted; if set, must match the base. Drives wheel selection and the site-packages layout. |
| `--extra` | Enable a uv project optional-dependency group (repeatable). |
| `--package` | Build a single uv workspace member by name (default: union of all members). |
| `--cache-dir` | Cache root (default: `$PYMAGE_CACHE_DIR` or the per-user cache dir). Caches compressed layers, downloaded wheels, and base interpreter detection. |
| `--no-cache` | Disable all caching (layers, downloaded wheels, interpreter detection). |
| `--prefix` | install prefix / venv root (default `/app/.venv`). |
| `--workdir` | image working dir and source destination (default `/app`). |
| `--user` | image user, e.g. `65532`. |
| `--insecure` | use plain HTTP for the registry. |
| `--require-hashes` | require `--hash` on every requirement in `requirements.txt` (default true; `uv.lock` carries its own hashes). |

## Layout

| Package | Responsibility |
| --- | --- |
| `internal/ptar` | Deterministic tar + OCI layer construction. |
| `internal/wheel` | Parse a wheel and lay it out into installed files. |
| `internal/lock` | Parse `uv.lock` and hashed `requirements.txt`. |
| `internal/wheelhouse` | Resolve wheels locally or download from lock URLs. |
| `internal/project` | Discover lock, entrypoint, env, and `[tool.pymage]` config from a uv project. |
| `internal/build` | Assemble base + per-wheel layers + app layer; rewrite config. |
| `internal/sbom` | Emit a deterministic CycloneDX SBOM. |
| `internal/cli` | The `build` command. |
| `example/` | Sample FastAPI uv project for CI and manual testing. |
| `e2e` | End-to-end tests against a local registry. |

## Testing

```
go test ./...
```

Tests are hermetic (no network, no Docker): wheels are synthesized in-process
and pushes/pulls go to go-containerregistry's in-process registry served over
HTTP. The `e2e` package demonstrates:

- **reproducibility** — two independent builds yield the same image digest;
- **no-bytes rebuild** — re-pushing an unchanged image uploads zero blobs, and
  adding one dependency uploads exactly one new dependency layer;
- **correctness** — the installed packages import under a real `python3`.
