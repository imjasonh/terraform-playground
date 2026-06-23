#!/usr/bin/env bash
# Build Firecracker from the generic vhost-user frontend PR branch.
# fc-runner needs PUT /vhost-user-devices/{id}, which is not in release builds yet.
# Requires: docker, git, sudo (for devtool).
set -euo pipefail

DEPS_DIR="${DEPS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/.deps}"
FC_SRC="${DEPS_DIR}/firecracker-src"
PR_REF="${FC_VHOST_PR_REF:-refs/pull/5773/head}"
ARCH="$(uname -m)"

mkdir -p "$DEPS_DIR"

if [ ! -d "$FC_SRC/.git" ]; then
  git clone https://github.com/firecracker-microvm/firecracker "$FC_SRC"
fi

cd "$FC_SRC"
git fetch origin "$PR_REF:feat/generic-vhost-user" || {
  echo "Failed to fetch ${PR_REF}. Check that PR #5773 is still available." >&2
  exit 1
}
git checkout feat/generic-vhost-user

echo "Building Firecracker (generic vhost-user) — this may take several minutes..."
sudo systemctl start docker 2>/dev/null || true
sudo ./tools/devtool build --release

TARGET="build/cargo_target/${ARCH}-unknown-linux-musl/release/firecracker"
install -m 0755 "$TARGET" "${DEPS_DIR}/firecracker-virtiofs"
echo "Installed: ${DEPS_DIR}/firecracker-virtiofs"
"${DEPS_DIR}/firecracker-virtiofs" --version
