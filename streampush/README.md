# streampush

Push a container image whose single layer is **generated as it uploads** and
**never buffered**. The goal: stream a 200GB layer to a registry using a small,
constant amount of memory, with the layer digest finalized only once the upload
completes.

```
go run . --size 200GB --data random --compression none --registry stream
```

## How it works

The whole pipeline is single-pass and back-pressured by the network:

1. **Generator** (`gen.go`) — a `genReader` produces exactly `--size` bytes on
   demand into each `Read` buffer (zeros or a fast allocation-free xorshift
   PRNG). It never allocates per-read and never holds more than one buffer.
2. **`stream.Layer`** (go-containerregistry's `pkg/v1/stream`) — wraps the
   generator, gzips it on the fly through an `io.Pipe`, and computes the
   compressed digest, the uncompressed `diffID`, and the size **during the
   single streaming pass**. Until the stream is fully consumed, `Digest()`,
   `DiffID()` and `Size()` return `stream.ErrNotComputed`.
3. **`remote.Write`** — the pusher sees `stream.ErrNotComputed`, so it uploads
   the layer first via one chunked `PATCH` (no `Content-Length`, no retry
   buffer), then derives the config + manifest from the now-known digests and
   `PUT`s them. The layer body is sent straight from the gzip pipe to the socket.

So memory stays flat regardless of layer size. A 4 GiB and a 32 GiB push use the
same peak heap:

```
=== 4GiB ===   elapsed: 5.3s    peak Go heap during push: 50.53 MiB
=== 32GiB ===  elapsed: 42.7s   peak Go heap during push: 57.51 MiB
```

(The peak heap includes the in-process registry, since `--registry stream` runs
in the same process.)

## About `pkg/registry`

The prompt asked to use go-containerregistry's `pkg/registry` as a Docker Hub
stand-in *if it supports incremental uploads*. It speaks the incremental upload
**protocol** (`POST` → `PATCH` → `PUT`), and you can select it with
`--registry pkg` for small images. But it is **not** a non-buffering target:

- Its `PATCH` handler does `io.Copy(&bytes.Buffer{}, req.Body)`, reading the
  entire chunk into memory (`pkg/registry/blobs.go`).
- Its default blob handler stores each blob in a `map[string][]byte`.

A 200GB layer would therefore require ~200GB of RAM on the server. `streampush`
prints a warning and will likely OOM if you point `--registry pkg` at a
multi-gigabyte `--size`.

To actually receive a 200GB layer, this repo includes **`sinkRegistry`**
(`registry.go`): a minimal OCI distribution registry that streams the
`PATCH`/`PUT` body through a `sha256` hasher with a fixed staging buffer,
retaining only a bounded prefix (`--retain-below`, default 32 MiB) so the config
and manifest stay pullable while the giant layer is digested-and-dropped. This
is the default (`--registry stream`).

## Flags

| flag | default | meaning |
| --- | --- | --- |
| `--size` | `200GB` | uncompressed layer size (`200GB`, `512MiB`, raw bytes, ...) |
| `--registry` | `stream` | `stream` (non-buffering sink), `pkg` (buffers!), or a `host[:port]` to push to a real registry |
| `--data` | `zero` | `zero` (fast/compressible) or `random` (incompressible, CPU heavy) |
| `--compression` | `none` | `none`, `speed`, `default`, `best`, or `0`–`9` |
| `--retain-below` | `32MiB` | sink retains blob content below this size |
| `--repo` / `--tag` | `streampush/big` / `latest` | image coordinates |
| `--insecure` | `false` | use http for an explicit `--registry` host |
| `--verbose` | `false` | log registry + push activity |

### Wire size vs. generated size

`stream.Layer` always gzips. To put real bytes on the wire:

- `--data zero --compression none` — fast; ~200GB of (gzip-wrapped, undeflated)
  zeros actually traverse the socket. Good for plumbing/throughput tests.
- `--data random --compression none` — incompressible bytes, on-wire size tracks
  generated size, low CPU.
- `--data random --compression best` — realistic CPU cost; the digest is of the
  compressed stream.

### Pushing to a real registry

```
# e.g. a local registry:2 container, or Docker Hub (with auth configured)
go run . --size 1GiB --data random --registry localhost:5000 --insecure
go run . --size 1GiB --data random --registry index.docker.io --repo youruser/streamtest
```

`remote.Write` uses the default keychain, so `docker login` credentials apply.
Note that real registries enforce blob/layer size limits; Docker Hub will not
accept a 200GB layer.
