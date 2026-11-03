#!/usr/bin/env bash
# Build the gomobile AAR that binds the Go core (identity) into the Android app.
# Requires: Android SDK + NDK, Go, and gomobile in PATH.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
NDK_VERSION="${NDK_VERSION:-29.0.14206865}"
export ANDROID_NDK_HOME="${ANDROID_NDK_HOME:-$ANDROID_HOME/ndk/$NDK_VERSION}"

if ! command -v gomobile >/dev/null 2>&1; then
  echo "gomobile not found; run: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
  exit 1
fi

mkdir -p "$ROOT/android/app/libs"
cd "$ROOT"
gomobile bind -target=android -androidapi 29 -o android/app/libs/mobile.aar ./mobile
ls -lh android/app/libs/mobile.aar
