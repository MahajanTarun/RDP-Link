#!/usr/bin/env bash
# Build Linux artifacts for both arm64 and amd64.
#
# On Apple Silicon, amd64 must be built in an x86-capable environment. A
# Colima profile with Rosetta binfmt works well:
#
#   colima start -p x86 --arch aarch64 --vm-type vz --vz-rosetta
#
# The script picks it up automatically when it exists. Plain qemu emulation is
# known to crash the Go toolchain, so it is not used.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Build caches and temp files live in .build/ (SSD, git-ignored).
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
DIST="$ROOT/dist/linux"
mkdir -p "$DIST/bin"

# Optional per-arch Docker contexts (Colima profiles).
CTX_ARM64="${CTX_ARM64:-}"
CTX_AMD64="${CTX_AMD64:-colima-x86}"

# Starting the x86 Colima profile switches the current Docker context, which
# would otherwise run the arm64 build under emulation. Prefer the native
# Colima profile when it exists.
if [[ -z "$CTX_ARM64" ]] && docker context inspect colima >/dev/null 2>&1; then
  CTX_ARM64=colima
fi

docker_for() {
  local arch="$1"; shift
  local ctx=""
  case "$arch" in
    arm64) ctx="$CTX_ARM64" ;;
    amd64) ctx="$CTX_AMD64" ;;
  esac
  if [[ -n "$ctx" ]] && docker context inspect "$ctx" >/dev/null 2>&1; then
    docker --context "$ctx" "$@"
  else
    docker "$@"
  fi
}

build_arch() {
  local arch="$1" platform="$2" suffix="$3"
  local image="officerdp-linux-builder-${arch}"
  echo "==> Building ${arch} (${platform})"
  docker_for "$arch" build --platform "$platform" \
    -f "$ROOT/packaging/Dockerfile.linux" -t "$image" "$ROOT" >/dev/null
  local cid
  cid=$(docker_for "$arch" create --platform "$platform" "$image")
  docker_for "$arch" cp "$cid:/src/dist/office-rdp-linux" "$DIST/bin/office-rdp-linux-${suffix}"
  docker_for "$arch" cp "$cid:/src/dist/office-rdp-rendezvous-linux" "$DIST/bin/office-rdp-rendezvous-linux-${suffix}"
  docker_for "$arch" cp "$cid:/src/app/dist/rdp-link" "$DIST/bin/rdp-link-${suffix}"
  docker_for "$arch" rm "$cid" >/dev/null
}

build_arch arm64 linux/arm64 arm64
build_arch amd64 linux/amd64 amd64

# Native aliases for the host architecture.
host_arch="$(uname -m)"
case "$host_arch" in
  arm64|aarch64) native=arm64 ;;
  *) native=amd64 ;;
esac
cp "$DIST/bin/office-rdp-linux-${native}" "$DIST/bin/office-rdp-linux"
cp "$DIST/bin/office-rdp-rendezvous-linux-${native}" "$DIST/bin/office-rdp-rendezvous-linux"
cp "$DIST/bin/rdp-link-${native}" "$DIST/rdp-link"
chmod +x "$DIST/bin/"* "$DIST/rdp-link"

echo "==> Package tarball"
cp "$ROOT/LICENSE" "$DIST/LICENSE"
cp "$ROOT/THIRD_PARTY_NOTICES.md" "$DIST/THIRD_PARTY_NOTICES.md"
cd "$DIST"
tar czf "$DIST/rdplink-linux.tar.gz" rdp-link bin LICENSE THIRD_PARTY_NOTICES.md
echo "Built: $DIST/rdplink-linux.tar.gz"
