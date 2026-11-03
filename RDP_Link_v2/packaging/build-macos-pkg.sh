#!/usr/bin/env bash
# Build a double-click .pkg that installs RDP Link and runs the postinstall
# automation (firewall + login service). Privacy permissions are handled by the
# in-app Setup assistant, because macOS forbids pre-granting them.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist/macos"
APP="$DIST/RDP Link.app"
PKG="$DIST/RDPLink-installer.pkg"
STAGE="${PKG_STAGE:-$HOME/.cache/rdplink-pkg}"

[[ -d "$APP" ]] || { echo "error: build the app first (packaging/build-macos.sh)" >&2; exit 1; }

rm -rf "$STAGE"
mkdir -p "$STAGE/Applications"
cp -R "$APP" "$STAGE/Applications/"

pkgbuild --root "$STAGE" \
  --identifier com.eminentailabs.rdplink \
  --version 0.2.0 \
  --scripts "$ROOT/packaging/macos/scripts" \
  --install-location / \
  "$PKG"

if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
  productsign --sign "$CODESIGN_IDENTITY" "$PKG" "$PKG.signed"
  mv "$PKG.signed" "$PKG"
fi

echo "Built: $PKG"
