#!/usr/bin/env bash
# Fetch/build host dependencies for a full fc-runner smoke test.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export DEPS_DIR="${DEPS_DIR:-$ROOT/.deps}"

echo "==> Firecracker with generic vhost-user (for virtio-fs)"
"$ROOT/scripts/build-firecracker-virtiofs.sh"

echo "==> Guest vmlinux with virtio-fs"
"$ROOT/scripts/build-vmlinux-virtiofs.sh"

echo
echo "Done. Use:"
echo "  export PATH=\"$DEPS_DIR:\$PATH\""
echo "  fc-runner --firecracker $DEPS_DIR/firecracker-virtiofs --kernel $DEPS_DIR/vmlinux-virtiofs ..."
