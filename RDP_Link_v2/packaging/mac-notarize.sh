#!/usr/bin/env bash
# Optional Apple notarization helper for Office RDP macOS artifacts.
# Usage:
#   packaging/mac-notarize.sh dist/macos/OfficeRDP-macos.dmg
#
# Required env:
#   APPLE_ID, APPLE_APP_SPECIFIC_PASSWORD, APPLE_TEAM_ID
set -euo pipefail
FILE="${1:-}"
[[ -n "$FILE" && -f "$FILE" ]] || { echo "usage: $0 <path/to/OfficeRDP-macos.dmg>"; exit 2; }
: "${APPLE_ID:?APPLE_ID required}"
: "${APPLE_APP_SPECIFIC_PASSWORD:?APPLE_APP_SPECIFIC_PASSWORD required}"
: "${APPLE_TEAM_ID:?APPLE_TEAM_ID required}"
xcrun notarytool submit "$FILE" --apple-id "$APPLE_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
xcrun stapler staple "$FILE"
echo "Notarized: $FILE"
