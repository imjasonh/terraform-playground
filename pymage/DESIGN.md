# `pymage` — a docker-less, layer-aware Python image builder

> Status: **Implemented.** This document captures the architecture and rationale;
> the builder, tests, and CI now live alongside it in this directory. Some items
> `uv.lock` ingestion and network wheel fetching are implemented; some other
> items noted as future work remain unimplemented.

## 1. Goal

A single **CLI tool** (in the spirit of [`ko`](https://github.com/ko-build/ko))
that builds and pushes OCI container images for Python applications **without a
Docker daemon** — no hosted service, no infrastructure. It is fast and cheap
because it exploits content-addressed layering:

- **Wheel dependencies are split into reusable layers.** Once a layer for a given
  wheel exists, builds reuse it **without downloading the wheel or uploading the
  layer bytes** again.
- **Only genuinely new dependencies cause layer changes.** Bumping app code, or
  rebuilding an unchanged dependency set, changes *no* dependency layers.
- **Application code is a thin layer on top** of the dependency layers, so the
  common edit-rebuild loop touches only one small layer.
- Builds are **reproducible**: same lock + same source + same base ⇒ same image
  digest, which lets us skip work entirely when nothing changed.

### Non-goals (initially)

- Compiling C extensions / building wheels from sdists. We consume **pre-built
  wheels** only. (sdist→wheel is a future extension; it would run a build, then
  the resulting wheel re-enters the normal layer path.)
- Replacing dependency *resolution*. We delegate resolution to existing,
  correct tools (`uv` / `pip`) and consume their lockfile output.
- A general-purpose Dockerfile interpreter. This is a focused Python app builder
  (think `ko`, but for Python wheels).

## 2. Why this works (the core idea)

Three well-understood registry/OCI facts combine to make "no-bytes" rebuilds possible:

1. **Layers are content-addressed.** A layer blob is referenced by the digest of
   its (gzip-compressed) tar. Two builds that produce byte-identical layers
   produce the same digest, and a registry stores it once.
2. **Registries support blob existence checks and cross-repo mounts.** Before
   uploading, we `HEAD /v2/<repo>/blobs/<digest>`. If the blob already exists in
   the target repo, we upload nothing. If it exists elsewhere in the same
   registry, we `POST .../blobs/uploads/?mount=<digest>&from=<srcRepo>` to mount
   it — **zero bytes transferred**.
3. **A manifest is just JSON referencing blob digests.** If every layer + config
   blob already exists, a "build" is reduced to `PUT manifest` (a few KB).

So if we make each wheel produce a **deterministic** layer and we **cache the
mapping `wheel-sha256 → {diffID, blob digest, size}`**, then for a dependency we
have seen before we never touch the wheel bytes: we already know its layer digest,
we confirm the blob is in the registry, and we reference it. Adding a brand-new
dependency is the only thing that forces a download + layer build + one upload.

This is the same family of tricks behind `crane`/`go-containerregistry` blob
mounting, `nixery`'s per-package layering, and Bazel `rules_oci` `py_image`'s
deps-vs-app split — specialized here for the Python wheel ecosystem.

## 3. Layering strategy

### 3.1 One deterministic layer per wheel (default)

Each resolved distribution (`name==version`, for a specific
platform/abi/python tag) becomes **its own layer** whose tar contains exactly the
files that installing that wheel writes into the environment's `site-packages`
(plus its `*.dist-info` and any `bin/` console scripts).

Properties this gives us, matching the requirements:

- Adding a dependency ⇒ exactly one new layer; all existing layers keep the same
  digest ⇒ already present in registry ⇒ mounted/skipped.
- Removing a dependency ⇒ that layer is dropped from the manifest; no other layer
  changes.
- Bumping a dependency version ⇒ old layer dropped, one new layer added.

Layer **order** in the manifest is sorted deterministically (by distribution name,
then version) so the assembled image/config is itself reproducible and cacheable.
The app code layer is always last (top).

### 3.2 Determinism requirements (critical)

A wheel must always produce a byte-identical layer, or the cache and reuse break.
"Installing" a wheel is essentially: unzip it, lay files under a fixed
`site-packages` prefix, generate `RECORD`, and synthesize `console_scripts`. We do
this ourselves (in Go) to control every byte:

- **tar normalization:** fixed `mtime` (epoch 0 or a fixed `SOURCE_DATE_EPOCH`),
  `uid=gid=0`, fixed mode bits (`0644` files / `0755` dirs / preserve exec bit on
  scripts), no device/xattr/PAX-extra records, **entries emitted in sorted order**,
  consistent name prefixes, no `__pycache__`/`.pyc` (compiled at runtime or in a
  separate optional step).
- **deterministic gzip:** pin compression level and strip the gzip header mtime.
  Better still, **cache the compressed blob** keyed by content so we never
  recompress (avoids depending on the compressor being bit-stable across versions).
- **fixed install layout:** a single prefix shared by all wheels, e.g.
  `/app/.venv/lib/python<X.Y>/site-packages` with scripts in `/app/.venv/bin`. The
  base image sets `PATH`/`VIRTUAL_ENV` (or we drop a `.pth`) so the interpreter
  finds them.
- **disjoint file sets:** distributions own mostly-disjoint files; tar handles
  repeated *directory* headers fine. Namespace packages (pkgutil/PEP-420) share a
  dir but distinct files. We generate each package's `RECORD` deterministically and
  set `INSTALLER` to a constant. Edge cases (data files, `*.pth` from a package)
  are captured inside that package's own layer.

The cache key for a layer is:
`(wheel-sha256, target-os/arch, python-tag/abi-tag, install-layout-version)`.
Pure-python wheels (`py3-none-any`) are platform-independent ⇒ one layer shared
across all arches.

### 3.3 Layer-count trade-off (configurable)

Per-wheel layering maximizes reuse but a large dependency tree can exceed practical
limits (registries/runtimes tolerate many layers, but ~100+ tiny blobs slow pulls
and some tooling caps near 127). Strategies, selectable via flag/config:

- `auto` (**default, implemented**): one layer per wheel while the total image
  layer count fits a budget — `max-layers` (default **127**, counting base +
  deps + app), or `max-wheel-layers` to cap dependency layers directly. When the
  wheel count exceeds the budget, wheels are **bin-packed by hashing each
  distribution's normalized name into K = budget buckets**. Because a wheel's
  bucket depends only on its name, adding/removing/version-bumping one dependency
  changes only that one bucket's layer; every other layer keeps its digest. This
  is reuse-optimal for a fixed budget (size-balanced packing was rejected because
  it reshuffles on insert).
- `per-wheel`: best reuse, one layer per dist, no cap.
- `single-deps-layer`: all deps in one layer + app layer. Simplest, weakest reuse
  (any dep change rebuilds the whole deps layer). Useful for tiny apps.

## 4. The "no-bytes" build flow

```
lockfile + source + base-ref
        │
        ▼
1. Ingest lock  ─►  [{name, version, wheel-url, sha256, tags}, ...]
        │
        ▼
2. For each dep:
     key = sha256 (+ platform/py tags)
     ├─ layer meta in cache?         ── no ─► download wheel ─► build layer
     │        │ yes                                 │            (deterministic)
     │        ▼                                      ▼
     │   know {blob digest, diffID}            cache meta + blob
     ▼
3. For each layer blob:
     HEAD /v2/<repo>/blobs/<digest>
     ├─ exists in target repo      ─► reference only (0 bytes)
     ├─ exists elsewhere in reg.   ─► cross-repo mount (0 bytes)
     └─ missing                    ─► upload once
        │
        ▼
4. App layer: deterministic tar of source (respect ignore file, drop __pycache__),
   content-addressed → same HEAD/mount/upload logic.
        │
        ▼
5. Assemble: base (by digest) + sorted dep layers + app layer.
   Build config (entrypoint, env: PATH/VIRTUAL_ENV/PYTHONPATH, workdir, user,
   labels), compute manifest digest.
        │
        ▼
6. If multi-arch: repeat per arch, assemble an image index.
        │
        ▼
7. If manifest digest already tagged ─► done (no upload at all).
   Else PUT config (if missing) + PUT manifest/index.   ← only small JSON moves
```

For an unchanged dependency set, steps 2–3 transfer **zero** dependency bytes; only
the app layer (if source changed) and the manifest move.

### 4.1 Caches

- **Local cache** (always): content-addressed dirs for wheels and built layer
  blobs + a small metadata DB (`wheel-sha256 → layer meta`). Lets repeated local
  builds skip downloads and recompression.
- **Remote shared cache** (optional): the **target registry itself** is the cache
  for blobs — `HEAD` is the lookup. Optionally a side bucket (GCS/S3) for layer
  metadata so CI runners share the `sha256 → digest` map without each re-deriving
  it. This is what enables a cold runner to build with no wheel downloads.

## 5. Dependency resolution / lock ingestion

We do **not** resolve; we ingest a fully-pinned, hashed lock so every wheel is
identified by URL + sha256. Supported inputs (pluggable parsers):

- `requirements.txt` with `--hash=sha256:...` (from `pip-compile`/`uv pip compile`).
- `uv.lock`.
- `poetry.lock` / PDM lock.
- `pip install --report <json>` output (rich, includes resolved wheel URLs).

If only loose requirements are provided, we can **shell out** to `uv pip compile`
to produce a hashed lock first (clearly an online step, separate from the
no-bytes build path). The lock is the contract that makes builds reproducible.

## 6. Base image handling

- Reference a base by **digest** (e.g. a distroless or Chainguard `python` image).
  We fetch only its *manifest + config* (small) — base layer blobs are referenced
  by digest and **never pulled**.
- We append our layers on top and rewrite the config: set
  `Env` (`PATH`, `VIRTUAL_ENV`, optional `PYTHONPATH`), `WorkingDir`, `User`
  (non-root by default), `Entrypoint`/`Cmd`, and `Labels`/annotations.
- The base must provide a compatible CPython (matching the wheels' python/abi
  tags). We validate the base's interpreter version against the lock's tags and
  fail fast on mismatch.

## 7. Reproducibility, SBOM, provenance

- Deterministic everything ⇒ identical inputs yield an identical image digest.
  We expose `--dry-run`/`--print-digest` to compute the would-be digest offline.
- **SBOM**: we already hold every wheel's name/version/sha256, so we emit an
  SBOM (SPDX/CycloneDX) and attach it. (Natural fit for this repo's Chainguard
  leanings.)
- Optional signing/attestation (`cosign`-style) and SLSA provenance as a later
  add-on.

## 8. Multi-arch

Wheels are tagged by platform/abi (manylinux, macОS, etc.). For multi-arch images
we resolve a wheel set **per target platform**, build per-arch images (sharing all
pure-python layers), and publish an **image index** referencing each arch manifest.

## 9. Surface area & tech choices

- **Language: Go**, consistent with the rest of this monorepo, using
  [`go-containerregistry`](https://github.com/google/go-containerregistry) (ggcr)
  for all registry I/O, layer/image types, blob mounting, and auth (keychain). No
  Docker daemon, no shelling out to `crane`.
- Wheel "install" implemented in Go: parse the wheel zip (it is a zip with
  `*.dist-info/{RECORD,WHEEL,METADATA,entry_points.txt}`), lay files into the
  staging prefix, synthesize console scripts, write deterministic tar.
- **A single CLI**, like `ko` — no daemon, no server, no infra. Build-affecting
  inputs live in a `[tool.pymage]` table in `pyproject.toml` (the standard PyPA
  namespace for tool config), so the common invocation is just:
  ```
  # [tool.pymage] in pyproject.toml:
  #   repo = "registry.example.com/me/myapp"
  #   base = "cgr.dev/chainguard/python@sha256:..."
  #   platforms = ["linux/amd64", "linux/arm64"]
  pymage build              # -> registry.example.com/me/myapp:latest (by digest)
  pymage build -t v1.2.3    # tag component only; repo comes from config
  ```
  Like `ko`, the destination **repo** is configuration, not a positional/flag on
  every run, and images are always published **by digest**; `-t/--tag` sets only
  the tag pointer(s). Every config key has a matching flag (`--repo`, `--base`,
  `--platform`, …) and an explicit flag overrides config, which overrides the
  built-in default. It prints the resulting image digest on success. Useful
  variants: `--push=false` (build to local OCI layout), `--print-digest`
  (compute the would-be digest offline, no push), and standard ggcr keychain
  auth (Docker config / cloud helpers) so it works in CI without extra setup.
- New self-contained module dir `pymage/` with its own `go.mod` and
  `README.md`, mirroring repo conventions. **No `main.tf` / Cloud Run** — this is
  a standalone CLI, not a hosted service.

## 10. Incremental implementation plan

Each phase is independently useful and reviewable.

1. **Skeleton + deterministic tar.** New `pymage/` Go module; a function
   that writes a normalized, reproducible tar and a round-trip determinism test
   (same input ⇒ same bytes ⇒ same digest).
2. **Wheel → layer.** Parse a wheel zip, lay out files into the fixed prefix,
   generate `RECORD`/console scripts, produce a ggcr `v1.Layer`. Golden-file tests
   asserting stable diffIDs for fixture wheels (one pure-python, one manylinux).
3. **Lock ingestion.** Parser(s) for hashed `requirements.txt` and `uv.lock`,
   normalized to `{name, version, url, sha256, tags}`. Wheels download from
   lock URLs when not present in `--find-links`.
4. **Local layer cache.** Content-addressed wheel + blob store and the
   `sha256 → layer meta` DB. Prove second build does zero downloads/recompression.
5. **Image assembly + push (ggcr).** Base-by-digest, sorted dep layers, app layer,
   config rewrite, manifest. Wire `HEAD` existence checks + cross-repo blob mount.
   Prove an unchanged-deps rebuild pushes only the manifest (and app layer if
   source changed).
6. **App layer + ignore file.** Deterministic source tar honoring an ignore file,
   excluding `__pycache__`/VCS dirs.
7. **Multi-arch + image index.**
8. **SBOM emission**, then optional signing/provenance.

## 11. Risks & open questions

- **Determinism is load-bearing.** Any non-reproducible byte (mtime, gzip header,
  entry ordering, locale-dependent sorting) silently breaks reuse. Mitigation:
  cache the *compressed blob* by content, plus determinism tests in CI.
- **Layer-count vs. reuse.** Per-wheel is ideal for reuse but can produce many
  layers; the `hybrid` strategy bounds it at some reuse cost. Pick defaults and
  document the trade-off (§3.3).
- **File collisions** between distributions (shared namespace dirs, data files,
  duplicate scripts). Need a conflict policy (last-writer / fail / dedicated
  layer). Mostly rare in practice; must be handled deterministically.
- **`__pycache__`/`.pyc`.** Excluded for determinism; optionally pre-compile in a
  separate, clearly-marked optional layer (bytecode is hash-stable given fixed
  inputs + `PYTHONHASHSEED`).
- **Cross-repo mount auth.** The push credential must also have *pull* scope on the
  source repo for `mount=...&from=...` to succeed; otherwise we fall back to
  upload. Handle the 202-without-mount case gracefully.
- **Base/interpreter mismatch.** Wheel tags must match the base CPython; validate
  and fail fast.
- **sdist-only dependencies.** Out of scope initially; later, build the wheel once
  then feed it into the normal layer path.

## 12. Prior art (for reference)

`ko`, `apko`/`melange`, `crane`/`go-containerregistry` (blob mounts), Bazel
`rules_oci` / `rules_docker` `py3_image` (deps-vs-app split), `nixery` (per-package
layers), Cloud Native Buildpacks (layer reuse + rebase), `distroless`.
