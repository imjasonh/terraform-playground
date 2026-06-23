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

| Requirement | Notes |
|-------------|-------|
| Rust 1.96+ | `source /usr/local/cargo/env` in the devcontainer |
| `/dev/kvm` read/write | `[ -w /dev/kvm ] && echo OK` — add yourself to the `kvm` group if needed |
| Docker config (optional) | `~/.docker/config.json` for private registries |
| Host tools for VM path | `git`, `curl`, `docker`, `sudo` (kernel + Firecracker builds use Firecracker's `devtool`) |

**Important:** virtio-fs needs two things that official Firecracker release artifacts do not provide out of the box:

1. A **guest `vmlinux` with `CONFIG_VIRTIO_FS=y`** — the CI kernels from S3 do not enable virtio-fs.
2. A **Firecracker binary with generic vhost-user** ([PR #5773](https://github.com/firecracker-microvm/firecracker/pull/5773)) — release builds only expose vhost-user **block**, not virtio-fs.

The scripts under `scripts/` automate both builds into `.deps/`.

## Build this project

```bash
source /usr/local/cargo/env
cd firecracker-container-vm
cargo build --release
```

## Quickstart (full path)

### 1. Host dependencies (Firecracker + vmlinux)

One-shot (builds from source; needs Docker and sudo):

```bash
cd firecracker-container-vm
./scripts/setup-host.sh
```

Or step by step:

```bash
export DEPS_DIR="$PWD/.deps"

# Firecracker with PUT /vhost-user-devices/{id} (PR #5773 branch)
./scripts/build-firecracker-virtiofs.sh

# Guest kernel with virtio-fs enabled (patches Firecracker's 6.1 CI config)
./scripts/build-vmlinux-virtiofs.sh
```

To download a **stock** Firecracker release only (no virtio-fs frontend):

```bash
./scripts/download-firecracker.sh   # installs .deps/firecracker
```

### 2. Verify KVM

```bash
[ -r /dev/kvm ] && [ -w /dev/kvm ] && echo "KVM OK" || echo "Fix KVM access first"
```

### 3. Run the lazy rootfs daemon

```bash
./target/release/fc-vhostfsd \
  --image docker.io/library/alpine:3.20 \
  --socket /tmp/fc-vhostfs.sock \
  --cache-dir /tmp/fc-oci-cache \
  --tag rootfs \
  --metrics-addr 127.0.0.1:9100
```

In another terminal, confirm metrics and that the socket exists:

```bash
curl -s localhost:9100/metrics | rg '^fc_startup_ready_milliseconds'
ls -l /tmp/fc-vhostfs.sock
```

### 4. Boot Firecracker with the container image as rootfs

```bash
./target/release/fc-runner \
  --image docker.io/library/alpine:3.20 \
  --firecracker "$PWD/.deps/firecracker-virtiofs" \
  --kernel "$PWD/.deps/vmlinux-virtiofs" \
  --cache-dir /tmp/fc-oci-cache \
  --vhost-socket /tmp/fc-vhostfs.sock \
  --tag rootfs \
  --memory-mib 512
```

Use `--dry-run` to print the Firecracker API payload without starting the VM (still starts `fc-vhostfsd`).

Guest cmdline (set by `fc-runner`):

```
console=ttyS0 reboot=k panic=1 pci=off init=/bin/sh rootfstype=virtiofs root=/ root=rootfs
```

Alpine provides `/bin/sh` on the merged root. You should get a shell on the serial console when the VM starts.

### 5. Smoke test without a VM (daemon only)

If you only want to validate lazy pulls + vhost-user handshake:

```bash
cargo test
# vhost-user protocol test against a local fixture layer (no KVM/registry)
cargo test -p fc-runner --test vhost_e2e
```

## Run the vhost-fs daemon (reference)

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

You need a **Linux guest kernel with virtio-fs over virtio-mmio** (Firecracker's bus).

### Do not use the stock CI vmlinux for this example

Firecracker's [getting-started guide](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md) downloads kernels from S3:

```bash
ARCH="$(uname -m)"
release_url="https://github.com/firecracker-microvm/firecracker/releases"
latest_version=$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${release_url}/latest")")
CI_VERSION=${latest_version%.*}
latest_kernel_key=$(curl "http://spec.ccfc.min.s3.amazonaws.com/?prefix=firecracker-ci/$CI_VERSION/$ARCH/vmlinux-&list-type=2" \
  | grep -oP "(?<=<Key>)(firecracker-ci/$CI_VERSION/$ARCH/vmlinux-[0-9]+\.[0-9]+\.[0-9]{1,3})(?=</Key>)" \
  | sort -V | tail -1)
wget -O vmlinux-ci "https://s3.amazonaws.com/spec.ccfc.min/${latest_kernel_key}"
```

That kernel is fine for block-device rootfs smoke tests, but its config has `# CONFIG_VIRTIO_FS is not set`. Use `./scripts/build-vmlinux-virtiofs.sh` instead.

### Recommended: build virtio-fs-enabled vmlinux

```bash
./scripts/build-vmlinux-virtiofs.sh
# output: .deps/vmlinux-virtiofs
```

This clones Firecracker, enables `CONFIG_VIRTIO_FS=y` in the `6.1` CI guest config, and runs `./tools/devtool build_ci_artifacts kernels 6.1`.

Minimum kernel options (already present in Firecracker CI configs except virtio-fs):

- `CONFIG_VIRTIO_MMIO=y`
- `CONFIG_VIRTIO_FS=y`
- `CONFIG_FUSE_FS=y`
- `CONFIG_SERIAL_8250_CONSOLE=y` for `console=ttyS0`

Manual build details: [Firecracker rootfs and kernel setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/rootfs-and-kernel-setup.md).

## Firecracker binary

### For virtio-fs (`fc-runner`)

Released binaries from [GitHub releases](https://github.com/firecracker-microvm/firecracker/releases) do **not** include `PUT /vhost-user-devices/{id}` yet. Build from PR #5773:

```bash
./scripts/build-firecracker-virtiofs.sh
# output: .deps/firecracker-virtiofs
```

### Download latest official release (block/net only)

```bash
./scripts/download-firecracker.sh
# output: .deps/firecracker
curl -fsSL https://github.com/firecracker-microvm/firecracker/releases/latest/download/firecracker-$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/firecracker-microvm/firecracker/releases/latest | xargs basename)-$(uname -m).tgz | tar -xz
```

Or manually (from upstream docs):

```bash
ARCH="$(uname -m)"
release_url="https://github.com/firecracker-microvm/firecracker/releases"
latest=$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${release_url}/latest")")
curl -fsSL "${release_url}/download/${latest}/firecracker-${latest}-${ARCH}.tgz" | tar -xz
mv "release-${latest}-${ARCH}/firecracker-${latest}-${ARCH}" firecracker
```

## Run the Firecracker example (reference)

```bash
./target/release/fc-runner \
  --image docker.io/library/alpine:3.20 \
  --firecracker "$PWD/.deps/firecracker-virtiofs" \
  --kernel "$PWD/.deps/vmlinux-virtiofs" \
  --cache-dir /tmp/fc-oci-cache \
  --dry-run
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
- Layer blobs are cached to disk on first full fetch; subsequent reads use the on-disk gzip index
- `fc-runner` is Firecracker-specific; other VMMs run `fc-vhostfsd` directly (see table above)

## References

- [dagdotdev explore README](https://github.com/jonjohnsonjr/dagdotdev/blob/main/pkg/explore/README.md) — gzip zran / range request strategy
- [`indexed_deflate`](https://crates.io/crates/indexed_deflate) — gzip random access indexes
- [`vhost-user-backend`](https://crates.io/crates/vhost-user-backend) — Rust vhost-user daemon framework
- [Firecracker generic vhost-user](https://github.com/firecracker-microvm/firecracker/pull/5773)
