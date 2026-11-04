#!/usr/bin/env bash
# Copy the guest UI and the logo into the Android app so an Android host can
# serve the same viewer page as the desktop host.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/android/app/src/main/assets"
mkdir -p "$DEST"
cp "$ROOT/core/host/viewer.html" "$DEST/viewer.html"
cp "$ROOT/core/host/logo.png" "$DEST/logo.png"
cp "$ROOT/LICENSE" "$DEST/LICENSE"
cp "$ROOT/THIRD_PARTY_NOTICES.md" "$DEST/THIRD_PARTY_NOTICES.md"
echo "synced assets -> $DEST"
