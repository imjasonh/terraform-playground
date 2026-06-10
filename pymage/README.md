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
| `layer-strategy` | `--layer-strategy` | `per-wheel` |
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
| `--layer-strategy` | `per-wheel` (default) or `single-deps-layer`. |
| `--platform` | Target platform(s); selects compatible wheels and base. Repeatable / comma-separated (e.g. `linux/amd64,linux/arm64`) builds a multi-arch image index. Defaults to the platforms the base image supports. |
| `--python` | Interpreter version, e.g. `python3.12`. Optional — **auto-detected from the base** when omitted; if set, must match the base. Drives wheel selection and the site-packages layout. |
| `--cache-dir` | Content-addressed layer cache; reuses compressed layers and downloaded wheels across rebuilds. |
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
