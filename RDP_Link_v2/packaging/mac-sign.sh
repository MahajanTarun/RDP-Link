#!/usr/bin/env bash
# macOS packaging sign + (optional) notarize
# Usage:
#
#   APPLE_ID="..." APPLE_APP_SPECIFIC_PASSWORD="..." APPLE_TEAM_ID="..." packaging/mac-sign.sh
#
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist/macos"
[[ -d "$DIST/Office RDP.app" ]] || { echo "run packaging/build-macos.sh first" >&2; exit 1; }

# Codesign (ad-hoc by default; set CSC_IDENTITY_AUTO_DISCOVERY=false to use ad-hoc)
/usr/bin/codesign --force --deep --sign - "$DIST/Office RDP.app" || true

# Optional Apple notarization
if [[ -n "${APPLE_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" && -n "${APPLE_TEAM_ID:-}" ]]; then
  ZIP="$DIST/OfficeRDP-macos-notarize.zip"
  (cd "$DIST" && zip -r "$ZIP" "Office RDP.app" >/dev/null)
  echo "Submitting to Apple notary..."
  xcrun notarytool submit "$ZIP" \
    --apple-id "$APPLE_ID" \
    --password "$APPLE_APP_SPECIFIC_PASSWORD" \
    --team-id "$APPLE_TEAM_ID" \
    --wait
  echo "Stapling..."
  xcrun stapler staple "$DIST/Office RDP.app"
  echo "Notarized."
else
  echo "No APPLE_ID/APPLE_APP_SPECIFIC_PASSWORD/APPLE_TEAM_ID set — skipped notarization."
fi
