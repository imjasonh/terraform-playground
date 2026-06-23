#!/usr/bin/env bash
# Download the latest official Firecracker release binary.
# Note: released Firecracker does not yet include generic vhost-user / virtio-fs
# (see scripts/build-firecracker-virtiofs.sh). Use this binary for block/net
# workflows or as a baseline install.
set -euo pipefail

DEPS_DIR="${DEPS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/.deps}"
mkdir -p "$DEPS_DIR"
cd "$DEPS_DIR"

ARCH="$(uname -m)"
release_url="https://github.com/firecracker-microvm/firecracker/releases"
latest="$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${release_url}/latest")")"

echo "Downloading Firecracker ${latest} for ${ARCH}..."
curl -fsSL "${release_url}/download/${latest}/firecracker-${latest}-${ARCH}.tgz" | tar -xz

BIN="release-${latest}-${ARCH}/firecracker-${latest}-${ARCH}"
install -m 0755 "$BIN" "${DEPS_DIR}/firecracker"
echo "Installed: ${DEPS_DIR}/firecracker"
"${DEPS_DIR}/firecracker" --version
