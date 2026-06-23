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

### Registry authentication

`fc-vhostfsd` / `fc-oci-fs` read credentials the same way the Docker CLI does:

- `~/.docker/config.json` (or `$DOCKER_CONFIG/config.json`)
- `auths` entries (inline base64 user/pass)
- per-registry `credHelpers`
- global `credsStore` (e.g. `docker-credential-gcr`, `osxkeychain`, `pass`)

Run `docker login` (or your cloud provider's helper) once; private images should work without extra flags.

## Metrics

`fc-vhostfsd` exposes Prometheus text on `http://127.0.0.1:9100/metrics` by default (`--metrics-addr`).

| Metric | Meaning |
|--------|---------|
| `fc_bytes_fetched_from_registry` | Compressed bytes actually pulled from the registry |
| `fc_bytes_saved_vs_full_pull` | `sum(layer sizes) - fetched` (data you did **not** download vs a full `docker pull`) |
| `fc_layer_compressed_bytes_total` | Total compressed layer bytes in the resolved image |
| `fc_registry_range_requests_total` | HTTP range requests to blob URLs |
| `fc_full_blob_downloads_total` | Layers fully copied to local cache (first-time index build) |
| `fc_gzip_index_builds_total` | Gzip index builds |
| `fc_fuse_requests_total` / `fc_fuse_reads_total` | virtio-fs / FUSE traffic |
| `fc_startup_ready_milliseconds` | Image open → vhost socket listening |
| `fc_process_rss_bytes` | Daemon resident memory |
| `fc_cache_dir_bytes_on_disk` | Local cache size (blobs + indexes) |

```bash
curl -s localhost:9100/metrics | rg '^fc_'
```

## Which VMM can use this? (virtio-fs frontends)

`fc-vhostfsd` is a **vhost-user backend**. Something in the VMM must act as the **vhost-user frontend** and connect to `--socket`. Your options:

| Frontend | Status | Notes |
|----------|--------|-------|
| **Firecracker** (generic vhost-user) | Needs recent build | [PR #5773](https://github.com/firecracker-microvm/firecracker/pull/5773) adds `PUT /vhost-user-devices/{id}` so virtio-fs works without native Firecracker virtio-fs code. `fc-runner` targets this API. |
| **Cloud Hypervisor** | Works today | First-class virtio-fs + vhost-user; point `--socket` at the same path. No Firecracker-specific API. |
| **QEMU** | Works today | `virtiofsd` / custom daemon via `-chardev socket` + `vhost-user-fs-pci` device. |
| **crosvm** | Works today | Can run vhost-user fs backends against a virtio-fs device. |
| **Stock Firecracker (released)** | No virtio-fs | Only block/net/vsock unless you build from the generic vhost-user branch. |

**You do not need to change `fc-vhostfsd` between these** — only the VMM configuration differs. `fc-runner` is Firecracker-specific; for Cloud Hypervisor or QEMU, run `fc-vhostfsd` manually and wire the socket in that VMM's config.

## Guest kernel (`vmlinux`)

You need a **Linux guest kernel with virtio-fs**, not a container host kernel. Minimum config:

- `CONFIG_VIRTIO_MMIO=y` (Firecracker uses MMIO virtio, not PCI)
- `CONFIG_VIRTIO_FS=y` and `CONFIG_VIRTIO_FS_VIRTIO_MEM=y` (if available)
- `CONFIG_EXT4` / `CONFIG_BLK_DEV` not required when root is virtiofs
- `CONFIG_SERIAL_8250_CONSOLE=y`, `CONFIG_VT=y` for `console=ttyS0`
- `CONFIG_DEVTMPFS=y`, `CONFIG_TMPFS=y`

**Easiest path for Firecracker:** use the upstream CI-built kernel artifacts attached to [Firecracker releases](https://github.com/firecracker-microvm/firecracker/releases) (the `vmlinux` asset), or build from [`resources/guest_configs`](https://github.com/firecracker-microvm/firecracker/tree/main/resources/guest_configs) with `virtio_fs` enabled.

Example cmdline (what `fc-runner` generates):

```
console=ttyS0 reboot=k panic=1 pci=off init=/bin/sh rootfstype=virtiofs root=/ root=rootfs
```

The image must contain `/bin/sh` (or change `init=`). Alpine works well for smoke tests. The `root=rootfs` tag must match `--tag` on `fc-vhostfsd`.

**Cloud Hypervisor / QEMU** often use the same `vmlinux`; only the virtio transport differs (PCI vs MMIO) — match the kernel to your VMM's virtio bus.

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
