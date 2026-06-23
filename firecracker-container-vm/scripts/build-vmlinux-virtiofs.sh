#!/usr/bin/env bash
# Build a Firecracker-compatible guest vmlinux with CONFIG_VIRTIO_FS=y.
#
# Official CI vmlinux artifacts from S3 do NOT enable virtio-fs; you need this
# step (or an equivalent custom kernel build) for fc-runner.
#
# Requires: docker, git, sudo. Expect roughly 15–30 minutes on a typical dev VM.
set -euo pipefail

DEPS_DIR="${DEPS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/.deps}"
FC_SRC="${DEPS_DIR}/firecracker-src-kernel"
ARCH="$(uname -m)"
KERNEL_VERSION="${KERNEL_VERSION:-6.1}"

mkdir -p "$DEPS_DIR"

if [ ! -d "$FC_SRC/.git" ]; then
  git clone --depth 1 https://github.com/firecracker-microvm/firecracker "$FC_SRC"
fi

cd "$FC_SRC"

CFG="resources/guest_configs/microvm-kernel-ci-${ARCH}-${KERNEL_VERSION}.config"
if [ ! -f "$CFG" ]; then
  echo "Kernel config not found: $CFG" >&2
  exit 1
fi

# Enable virtio-fs in the guest kernel (FUSE_FS is already enabled in CI configs).
if grep -q '^CONFIG_VIRTIO_FS=y' "$CFG"; then
  echo "virtio-fs already enabled in $CFG"
else
  echo "Patching $CFG to enable CONFIG_VIRTIO_FS=y"
  sed -i 's/# CONFIG_VIRTIO_FS is not set/CONFIG_VIRTIO_FS=y/' "$CFG"
fi

echo "Building guest kernel ${KERNEL_VERSION} via firecracker devtool..."
sudo systemctl start docker 2>/dev/null || true
sudo ./tools/devtool build_ci_artifacts kernels "${KERNEL_VERSION}"

OUT_DIR="resources/${ARCH}"
KERNEL="$(ls -1 "${OUT_DIR}"/vmlinux-"${KERNEL_VERSION}"* 2>/dev/null | grep -v debug | head -1)"
if [ -z "$KERNEL" ]; then
  echo "Kernel build finished but no vmlinux found under ${OUT_DIR}" >&2
  ls -la "$OUT_DIR" || true
  exit 1
fi

install -m 0644 "$KERNEL" "${DEPS_DIR}/vmlinux-virtiofs"
echo "Installed: ${DEPS_DIR}/vmlinux-virtiofs"
file "${DEPS_DIR}/vmlinux-virtiofs"
