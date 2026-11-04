#!/usr/bin/env bash
# Build the Windows NSIS installer.
#
# Cross-builds the Go agent binaries for windows/amd64 and packages them with
# NSIS. When a mingw-w64 cross compiler is available the host agent is built
# with CGO so it can capture the screen and inject input; otherwise it falls
# back to the stub build and prints a warning.
#
# The optional Python launcher (rdp-link.exe) is produced by
# packaging/build-windows.ps1 on a real Windows host. If it exists in
# dist/windows it is bundled automatically.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIST="$ROOT/dist/windows"
mkdir -p "$DIST"

cd "$ROOT"

# Remove artifacts from previous runs so the installer never picks up stale
# multi-arch binaries or a nested bin/ directory.
rm -rf "${DIST:?}/bin"
rm -f "$DIST/office-rdp.exe" "$DIST/office-rdp-rendezvous.exe" \
      "$DIST/RDPLink-setup.exe"

CGO_OK=0
if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
  CGO_OK=1
fi

if [[ "$CGO_OK" == "1" ]]; then
  echo "==> Building Windows host agent with CGO (mingw-w64)"
  CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
    GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
    CGO_LDFLAGS="-static-libgcc -static-libstdc++ -static" \
    go build -trimpath -ldflags "-s -w" -o "$DIST/office-rdp.exe" ./cmd/office-rdp
else
  echo "==> mingw-w64 not found; building Windows host agent without CGO"
  echo "    (screen capture and input need a CGO build from a Windows host)"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w" -o "$DIST/office-rdp.exe" ./cmd/office-rdp
fi

echo "==> Building rendezvous server"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o "$DIST/office-rdp-rendezvous.exe" ./cmd/rendezvous

cat > "$DIST/README.txt" <<'EOF'
RDP Link for Windows
======================

Developed by Eminent AI Labs Pvt. Ltd. — https://www.eminentailabs.com
Licensed under the MIT License (see LICENSE). Third-party notices are in
THIRD_PARTY_NOTICES.md.

Start the desktop app (share this screen and control other devices):

    rdp-link.exe

Share this screen from a terminal:

    office-rdp.exe --host --port 8088 --password <secret>

Open the browser control panel instead:

    office-rdp.exe --ui

Check that capture and input work:

    office-rdp.exe --doctor
    office-rdp.exe --displays

Stream a synthetic test pattern (CI / no capturable desktop):

    office-rdp.exe --host --test-pattern --port 8088 --password <secret>

Run a rendezvous server for WAN connections:

    office-rdp-rendezvous.exe --listen :8080

Guests can connect with the desktop launcher (rdp-link.exe), the macOS/Linux
app, the Android app, or any browser pointed at http://<this-pc>:8088/.

The host agent needs an interactive desktop session. Windows Firewall rules
for office-rdp.exe are added by the installer for private networks.
EOF

echo "==> Building installer"
# NSIS has no compile-time file-existence test, so the optional launcher is
# pulled in through a generated include.
LAUNCHER_INC="$ROOT/packaging/windows/optional-launcher.nsh"
if [[ -f "$DIST/rdp-link.exe" ]]; then
  printf '  File "../../dist/windows/rdp-link.exe"\n' > "$LAUNCHER_INC"
else
  printf '; rdp-link.exe not present\n' > "$LAUNCHER_INC"
fi

build_with_docker() {
  echo "==> Local makensis unavailable or crashed; building in Docker"
  local stage="${NSIS_STAGE:-$HOME/.cache/officerdp-nsis}"
  rm -rf "${stage:?}"
  mkdir -p "$stage/packaging/windows" "$stage/dist/windows"
  cp "$ROOT/packaging/windows/installer.nsi" "$stage/packaging/windows/"
  cp "$LAUNCHER_INC" "$stage/packaging/windows/optional-launcher.nsh"
  cp "$DIST/office-rdp.exe" "$DIST/office-rdp-rendezvous.exe" \
     "$DIST/README.txt" "$stage/dist/windows/"
  [[ -f "$DIST/rdp-link.exe" ]] && cp "$DIST/rdp-link.exe" "$stage/dist/windows/"
  docker run --rm -v "$stage:/work" debian:bookworm-slim \
    sh -c 'apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq nsis >/dev/null 2>&1 && cd /work/packaging/windows && makensis installer.nsi'
  cp "$stage/dist/windows/RDPLink-setup.exe" "$DIST/RDPLink-setup.exe"
}

if command -v makensis >/dev/null 2>&1; then
  if ! makensis "$ROOT/packaging/windows/installer.nsi" \
       || [[ ! -s "$DIST/RDPLink-setup.exe" ]]; then
    build_with_docker
  fi
else
  build_with_docker
fi

ls -lh "$DIST/office-rdp.exe" "$DIST/office-rdp-rendezvous.exe" \
       "$DIST/RDPLink-setup.exe"
echo "Built: $DIST/RDPLink-setup.exe"
