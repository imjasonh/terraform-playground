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

## Usage

```
# --lock is a pinned + hashed requirements file
#   (pip-compile / uv pip compile --generate-hashes)
# --find-links is a directory of the resolved .whl files
# --source is the application source (optional)
pymage build \
  --base cgr.dev/chainguard/python:latest \
  --lock requirements.txt \
  --find-links ./wheelhouse \
  --source ./ \
  --entrypoint python --entrypoint -m --entrypoint myapp \
  -t registry.example.com/me/myapp:latest
```

The wheels referenced by the lock must be present in a `--find-links` directory
(e.g. produced by `pip download -r requirements.txt -d ./wheelhouse`). Resolution
is intentionally local so builds are hermetic and reproducible.

### Useful flags

| Flag | Description |
| --- | --- |
| `--push=false` | Build without pushing (combine with `--oci-layout`). |
| `--oci-layout DIR` | Also write the image to an OCI layout directory. |
| `--print-digest` | Print only the resulting image digest (no push). |
| `--sbom PATH` | Write a CycloneDX SBOM of the resolved wheels. |
| `--layer-strategy` | `per-wheel` (default) or `single-deps-layer`. |
| `--platform` | Target platform; selects compatible wheels and the base, e.g. `linux/amd64`. |
| `--python` | Interpreter version / site-packages dir (default `python3.12`); also selects compatible wheels. |
| `--cache-dir` | Content-addressed layer cache; reuses compressed layers across rebuilds. |
| `--prefix` | install prefix / venv root (default `/app/.venv`). |
| `--workdir` | image working dir and source destination (default `/app`). |
| `--user` | image user, e.g. `65532`. |
| `--insecure` | use plain HTTP for the registry. |
| `--require-hashes` | require `--hash` on every requirement (default true). |

## Layout

| Package | Responsibility |
| --- | --- |
| `internal/ptar` | Deterministic tar + OCI layer construction. |
| `internal/wheel` | Parse a wheel and lay it out into installed files. |
| `internal/lock` | Parse hashed `requirements.txt`. |
| `internal/wheelhouse` | Resolve requirements to local wheel files (hash-checked). |
| `internal/build` | Assemble base + per-wheel layers + app layer; rewrite config. |
| `internal/sbom` | Emit a deterministic CycloneDX SBOM. |
| `internal/cli` | The `build` command. |
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
