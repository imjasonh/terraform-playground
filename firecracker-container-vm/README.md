# firecracker-container-vm

Example **Firecracker VM runner** that uses a container image reference as the guest root filesystem, loading files **on demand** at runtime over HTTP range requests into gzip-compressed OCI layers.

This implements the lazy-pull approach described in [dagdotdev registry explorer](https://github.com/jonjohnsonjr/dagdotdev/blob/main/pkg/explore/README.md):

1. Build a **gzip zran-style index** (`indexed_deflate`) over each `tar+gzip` layer.
2. Record a **tar table of contents** mapping paths to uncompressed byte offsets.
3. On file read, fetch only the **compressed byte range** needed from the registry, seek in the gzip stream, and return file bytes.
4. Serve the merged overlay via **virtio-fs** using [`vhost-user-backend`](https://crates.io/crates/vhost-user-backend) + [`fuse-backend-rs`](https://crates.io/crates/fuse-backend-rs).

## Architecture

```
┌─────────────────────┐      vhost-user UDS      ┌──────────────────────┐
│ Firecracker (guest) │ ◄──────────────────────► │ fc-vhostfsd          │
│  virtio-fs driver   │                        │  vhost-user-backend  │
└─────────────────────┘                        │  fuse-backend-rs     │
                                               │  OverlayFs (lazy)    │
                                               └──────────┬───────────┘
                                                          │ HTTP Range
                                                          ▼
                                               ┌──────────────────────┐
                                               │ OCI registry blobs   │
                                               │ (tar+gzip layers)    │
                                               └──────────────────────┘
```

### Crates

| Crate | Role |
|-------|------|
| `fc-oci-fs` | Registry client, gzip index, tar TOC, overlay `FileSystem` |
| `fc-vhostfsd` | vhost-user virtio-fs daemon |
| `fc-runner` | Spawns `fc-vhostfsd` and configures Firecracker |

## Prerequisites

- Rust 1.96+ (see `/usr/local/cargo/env` in the devcontainer)
- For full VM boot: Firecracker with **generic vhost-user** support ([PR #5773](https://github.com/firecracker-microvm/firecracker/pull/5773)), a Linux guest kernel with `virtio_fs`, and `/dev/kvm`

## Build

```bash
source /usr/local/cargo/env
cd firecracker-container-vm
cargo build --release
```

## Run the vhost-fs daemon

```bash
./target/release/fc-vhostfsd \
  --image docker.io/library/alpine:3.20 \
  --socket /tmp/fc-vhostfs.sock \
  --cache-dir /tmp/fc-oci-cache \
  --tag rootfs
```

On first access to a layer, the daemon downloads the blob (if not cached), builds a gzip index + tar TOC, then serves files via range reads.

## Run the Firecracker example

```bash
./target/release/fc-runner \
  --image docker.io/library/alpine:3.20 \
  --kernel /path/to/vmlinux \
  --firecracker /path/to/firecracker \
  --dry-run   # prints API config, keeps vhostfsd running
```

Without `--dry-run`, `fc-runner` configures Firecracker via its HTTP API:

- `PUT /boot-source`
- `PUT /machine-config`
- `PUT /vhost-user-devices/rootfs` (virtio-fs via generic vhost-user frontend)
- `PUT /actions` (`InstanceStart`)

Guest kernel cmdline includes `rootfstype=virtiofs root=/ root=rootfs`.

## Tests

```bash
cargo test
```

Integration test `vhost_user_virtiofs_daemon_handshake` verifies the vhost-user protocol end-to-end against a local fixture layer (no registry/KVM required).

## Benchmarks

```bash
cargo bench -p fc-oci-fs
```

Benchmarks build a synthetic `tar.gz` layer with hundreds of files and measure:

- **read_last_file_1kb** — random read of the last tar entry (exercises gzip seek + range-style decompression)
- **lookup_opt_data_file** — FUSE lookup through the overlay

## Design notes & limitations (example code)

This is an **example** implementation, not production hardened:

- **Read-only** rootfs; no whiteout/opaque-dir completeness guarantees beyond basic `.wh.*` handling
- **Single-platform** manifests only (no OCI index platform selection)
- Registry auth is minimal (anonymous/public images); extend `RegistryClient` for private registries
- Layer blobs are cached to disk on first full fetch; subsequent reads use the on-disk gzip index
- Firecracker virtio-fs requires a recent build with generic vhost-user; use Cloud Hypervisor as an alternative frontend

## References

- [dagdotdev explore README](https://github.com/jonjohnsonjr/dagdotdev/blob/main/pkg/explore/README.md) — gzip zran / range request strategy
- [`indexed_deflate`](https://crates.io/crates/indexed_deflate) — gzip random access indexes
- [`vhost-user-backend`](https://crates.io/crates/vhost-user-backend) — Rust vhost-user daemon framework
- [Firecracker generic vhost-user](https://github.com/firecracker-microvm/firecracker/pull/5773)
