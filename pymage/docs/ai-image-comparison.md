# Large AI images: where the size lives, and what pymage saves

A common worry with GPU/AI Python images is that they're huge and slow to
rebuild and ship. This study answers two questions with real numbers:

1. In a CUDA/PyTorch image, is the size in the **base image** or in the **Python
   wheels**?
2. How do **incremental rebuilds** (bump one dependency, or edit app code)
   compare between a normal `uv`/`pip` Dockerfile and pymage?

**TL;DR:** for modern PyTorch installs the CUDA runtime ships *inside the
wheels*, so the dependencies — not the base — are almost the entire image
(2945 MB of 2988 MB here, **98.5%**). That plays directly to pymage's strength:
each wheel is its own content-addressed layer, so bumping one dependency
re-uploads **one layer (~11 MB)** while the ~2.9 GB of `torch`/CUDA layers are
deduplicated by the registry. A `uv sync` Dockerfile puts the whole environment
in a single layer, so the same one-line bump re-pushes the **entire ~2.9 GB**.

## Where does the size come from?

Modern `torch` wheels on PyPI are the CUDA build: `torch` itself plus a stack of
`nvidia-*` CUDA-runtime wheels (cuDNN, cuBLAS, cuFFT, NCCL, …) and `triton`.
These are pulled as ordinary wheel dependencies — the CUDA libraries are *in the
wheels*, not in the base image.

A representative `uv` project (`torch`, `transformers`, `numpy`, `pillow`,
`fastapi`; 62 packages resolved) built on a `python:3.12-slim` base, pushed to a
local registry (compressed/registry sizes):

| Bucket | Compressed size | Share |
| --- | --- | --- |
| **Dependency (wheel) layers** | **2945 MB** | **98.5%** |
| Base image (`python:3.12-slim`, 5 layers) | 43 MB | 1.5% |
| **Image total** | **2988 MB** | 100% |

Largest single layers (one wheel each):

| Wheel | Layer (compressed) |
| --- | --- |
| `torch==2.12.0` | 564 MB |
| `nvidia-cublas` | 434 MB |
| `nvidia-cudnn-cu13` | 374 MB |
| `triton` | 234 MB |
| `nvidia-cufft` | 229 MB |
| `nvidia-nccl-cu13` | 208 MB |
| `nvidia-cusolver` | 204 MB |
| `nvidia-cusparselt-cu13` | 174 MB |

So the base is a rounding error; the wheels (dominated by `torch` + the CUDA
stack, ~2.7 GB) are the image. (Even if you *do* use a multi-GB
`nvidia/cuda:*-runtime` base, that base is stable and shared/cached — the churn
that costs you on every rebuild is the dependency layer.)

## Incremental rebuilds (the real win)

Built `v1`, then made two one-line changes and rebuilt, pushing to the same
registry and diffing the manifests. "Bytes re-uploaded" = the total size of
layers whose digest changed (every other layer is already present and is
skipped via a registry `HEAD`).

| Change | Layers changed | Bytes re-uploaded | Rebuild wall time |
| --- | --- | --- | --- |
| Bump one dep (`transformers` 5.11.0 → 5.10.2) | **1 / 65** | **11.2 MB** | ~6 s |
| Edit one app source file | **1 / 65** | **0.3 KB** | ~6 s |

On the dependency bump, the push moved exactly two blobs: the new
`transformers` layer (11 MB) and the image config; the other 63 layers —
including all ~2.9 GB of `torch`/CUDA — were already present and skipped.
**99.6% of the image (2977 MB) was deduplicated.**

### Contrast with a `uv`/`pip` Dockerfile

A typical CUDA Dockerfile installs the environment in a single step:

```dockerfile
RUN --mount=... uv sync --frozen --no-install-project   # ONE layer: ~2.9 GB
COPY . /app
```

- **Editing app code** invalidates the `COPY` layer only, so it's cheap in both
  approaches (assuming the multi-stage / deps-first ordering above). With a naive
  `RUN pip install … && COPY .` in one layer, even an app edit re-pushes
  everything.
- **Bumping one dependency** re-runs `uv sync`, which rewrites the whole
  virtualenv into a **single new image layer**. That entire layer — all ~2.9 GB,
  including `torch` and every CUDA wheel that *didn't* change — becomes a new blob
  that must be re-pushed and later re-pulled.

|  | pymage | `uv sync` Dockerfile |
| --- | --- | --- |
| Bytes re-pushed on a one-dep bump | **~11 MB** (the changed wheel) | **~2.9 GB** (whole venv layer) |
| Bytes a consumer re-pulls for the update | ~11 MB | ~2.9 GB |
| Layer granularity | one layer per wheel (auto-bucketed to ≤127) | one layer per `RUN` |

That's roughly a **260× reduction** in data moved for a routine dependency bump,
and the savings repeat on every `docker pull` of the new revision across every
node/replica. Build time drops too: pymage reuses the cached `torch`/CUDA layers
and only fetches + layers the changed wheel (~6 s here), whereas re-running
`uv sync` re-materializes and re-exports the multi-GB environment.

You can't easily get pymage's granularity from a Dockerfile: splitting `torch`
into its own layer means fragile, hand-maintained multi-`RUN` install ordering,
and even then a patch to one CUDA wheel re-pushes its whole `RUN` group. pymage
does per-wheel layering automatically and deterministically.

## Method & caveats

- Real `uv` project locked with `uv lock` (universal lock); pymage resolved the
  linux/amd64 runtime closure and pulled wheels from the lock URLs. Sizes are
  compressed (registry) bytes from the pushed manifests.
- Built on `python:3.12-slim` to isolate the dependency story. In production
  you'd typically run GPU images on a CUDA base (or rely on the host NVIDIA
  driver + container toolkit, since the `torch`/`nvidia-*` wheels already bundle
  the CUDA *runtime* libraries). The base choice doesn't change the
  layer-reuse result — the dependency layers are what churn.
- No GPU was needed: this measures build/layout/push behavior, not training.
- The first (cold) build downloads the full ~2.8 GB of wheels once (~90 s here);
  subsequent builds reuse the wheel and layer caches.
