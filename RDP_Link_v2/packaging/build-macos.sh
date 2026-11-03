#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Build caches and temp files live in .build/ (SSD, git-ignored).
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
APP_DIR="$ROOT/app"
DIST="$ROOT/dist/macos"
mkdir -p "$DIST"

echo "==> Build Go binaries (darwin)"
mkdir -p "$DIST/bin"
(cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o "$DIST/bin/office-rdp-arm64" ./cmd/office-rdp)
(cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o "$DIST/bin/office-rdp-amd64" ./cmd/office-rdp)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$DIST/bin/office-rdp-rendezvous-arm64" ./cmd/rendezvous)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o "$DIST/bin/office-rdp-rendezvous-amd64" ./cmd/rendezvous)

echo "==> Build permission helper (TCC)"
if command -v swiftc >/dev/null 2>&1; then
  swiftc -O -o "$APP_DIR/assets/rdp-link-tcc" "$ROOT/macos/tcc-helper.swift"
else
  echo "warning: swiftc not found; the setup assistant will use deep links only"
fi

echo "==> Build Python app bundle"
(cd "$APP_DIR" && bash build-app.sh --no-open)

APP_SRC="$APP_DIR/dist/RDP Link.app"
APP_DST="$DIST/RDP Link.app"
# A running copy keeps the old bundle busy and codesign then fails with
# "Write permissions error", so stop any instance before replacing the app.
pkill -f "$APP_DST/Contents/MacOS" >/dev/null 2>&1 || true
sleep 1
rm -rf "$APP_DST"
cp -R "$APP_SRC" "$APP_DST"

echo "==> Inject Go binaries"
mkdir -p "$APP_DST/Contents/MacOS/tools"
cp "$DIST/bin/"* "$APP_DST/Contents/MacOS/tools/"
cp "$APP_DIR/assets/rdp-link-tcc" "$APP_DST/Contents/MacOS/tools/" 2>/dev/null || true
cp "$APP_DIR/assets/rdp-link-tcc" "$DIST/bin/" 2>/dev/null || true
chmod +x "$APP_DST/Contents/MacOS/tools/rdp-link-tcc" 2>/dev/null || true

echo "==> Create universal-ish launcher wrapper (arch select)"
cat > "$APP_DST/Contents/MacOS/office-rdp" <<'SH'
#!/bin/sh
DIR="$(cd "$(dirname "$0")" && pwd)"
ARCH="$(uname -m)"
case "$ARCH" in
  arm64) exec "$DIR/tools/office-rdp-arm64" "$@" ;;
  *)     exec "$DIR/tools/office-rdp-amd64" "$@" ;;
esac
SH
chmod +x "$APP_DST/Contents/MacOS/office-rdp"

echo "==> Codesign"
IDENTITY="${CODESIGN_IDENTITY:--}"
SIGN_OPTS=("--force" "--deep" "--sign" "$IDENTITY")
if [[ -n "${CODESIGN_PROVISIONING_PROFILE:-}" ]]; then
  SIGN_OPTS+=("--provisioning-profile" "$CODESIGN_PROVISIONING_PROFILE")
fi
/usr/bin/codesign "${SIGN_OPTS[@]}" "$APP_DST"
if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
  /usr/bin/codesign --verify --strict --verbose=2 "$APP_DST"
fi

echo "==> Create DMG"
DMG="$DIST/RDPLink-macos.dmg"
rm -f "$DMG"
hdiutil create -volname "RDP Link" -srcfolder "$APP_DST" -ov -format UDZO "$DMG"
echo "Built: $DMG"

if [[ -n "${APPLE_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" && -n "${APPLE_TEAM_ID:-}" ]]; then
  echo "==> Apple notarization requested"
  bash "$ROOT/packaging/mac-notarize.sh" "$DMG"
fi
