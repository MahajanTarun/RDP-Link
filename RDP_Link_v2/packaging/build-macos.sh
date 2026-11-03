#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$ROOT/app"
DIST="$ROOT/dist/macos"
mkdir -p "$DIST"

echo "==> Build Go binaries (darwin)"
mkdir -p "$DIST/bin"
(cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o "$DIST/bin/office-rdp-arm64" ./cmd/office-rdp)
(cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o "$DIST/bin/office-rdp-amd64" ./cmd/office-rdp)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$DIST/bin/office-rdp-rendezvous-arm64" ./cmd/rendezvous)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o "$DIST/bin/office-rdp-rendezvous-amd64" ./cmd/rendezvous)

echo "==> Build Python app bundle"
(cd "$APP_DIR" && bash build-app.sh --no-open)

APP_SRC="$APP_DIR/dist/RDP Link.app"
APP_DST="$DIST/Office RDP.app"
rm -rf "$APP_DST"
cp -R "$APP_SRC" "$APP_DST"

echo "==> Inject Go binaries"
mkdir -p "$APP_DST/Contents/MacOS/tools"
cp "$DIST/bin/"* "$APP_DST/Contents/MacOS/tools/"

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
DMG="$DIST/OfficeRDP-macos.dmg"
rm -f "$DMG"
hdiutil create -volname "Office RDP" -srcfolder "$APP_DST" -ov -format UDZO "$DMG"
echo "Built: $DMG"

if [[ -n "${APPLE_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" && -n "${APPLE_TEAM_ID:-}" ]]; then
  echo "==> Apple notarization requested"
  bash "$ROOT/packaging/mac-notarize.sh" "$DMG"
fi
