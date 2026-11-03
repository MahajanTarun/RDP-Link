#!/usr/bin/env bash
# Build .deb packages for arm64 and amd64 from the Linux artifact set.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
DIST="$ROOT/dist/linux"
# Docker only shares $HOME reliably on macOS, and the repo path contains
# spaces/apostrophes, so stage the package tree under $HOME.
STAGE="${STAGE:-$HOME/.cache/officerdp-deb}"
mkdir -p "$STAGE"

build_deb() {
  local arch="$1"
  local art="$STAGE/debroot-$arch"
  rm -rf "$art"
  mkdir -p "$art/DEBIAN" "$art/usr/bin" "$art/usr/share/applications"
  cat > "$art/DEBIAN/control" <<PCONTROL
Package: office-rdp
Version: 0.2.0
Architecture: ${arch}
Maintainer: RDP Link <root@example.com>
Description: Cross-platform desktop sharing agent and viewer
Depends: libc6, libx11-6, libxext6, libxrandr2, libxinerama1, libxcursor1, libxi6, libxtst6
PCONTROL
  install -m 0755 "$DIST/bin/office-rdp-linux-${arch}" "$art/usr/bin/office-rdp"
  install -m 0755 "$DIST/bin/office-rdp-rendezvous-linux-${arch}" "$art/usr/bin/office-rdp-rendezvous"
  if [[ -f "$DIST/bin/rdp-link-${arch}" ]]; then
    install -m 0755 "$DIST/bin/rdp-link-${arch}" "$art/usr/bin/rdp-link"
  fi
  mkdir -p "$art/usr/share/doc/office-rdp"
  cat > "$art/usr/share/doc/office-rdp/README" <<'PREADME'
RDP Link for Linux
====================

  office-rdp --host --port 8088 --password <secret>   share this desktop
  office-rdp --doctor                                 check capture/input
  office-rdp --displays                               list displays
  office-rdp-rendezvous --listen :8080                run a rendezvous server
  rdp-link                                            open the desktop launcher

The host agent captures the screen and injects input, so it needs an X11
session (Wayland works only through XWayland). Guests can connect with the
desktop viewer, the Android app, or any browser at http://<host>:8088/.
PREADME
  cat > "$art/usr/share/applications/office-rdp.desktop" <<PDESKTOP
[Desktop Entry]
Type=Application
Name=RDP Link
Exec=/usr/bin/office-rdp --host --port 8088
Terminal=false
NoDisplay=true
PDESKTOP
  # Build from a copy inside the container: tar on a shared Docker volume on
  # macOS can see metadata churn and abort with "file changed as we read it".
  docker run --rm -v "$art":/work:ro -v "$STAGE":/out \
    alpine:3.19 sh -c "apk add dpkg >/dev/null && cp -a /work /tmp/pkg && dpkg-deb --build /tmp/pkg /out/rdplink-linux-${arch}.deb"
  mv "$STAGE/rdplink-linux-${arch}.deb" "$DIST/rdplink-linux-${arch}.deb"
  rm -rf "$art"
}

build_deb arm64
build_deb amd64

host_arch="$(uname -m)"
case "$host_arch" in
  arm64|aarch64) native=arm64 ;;
  *) native=amd64 ;;
esac
cp "$DIST/rdplink-linux-${native}.deb" "$DIST/rdplink-linux.deb"
echo "Built: $DIST/rdplink-linux-{arm64,amd64}.deb"
