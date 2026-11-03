#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../android"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}"
echo "==> bundleRelease"
gradle bundleRelease
echo "==> assembleRelease"
gradle assembleRelease
echo "Artifacts:"
ls -lh app/build/outputs/apk/release app/build/outputs/bundle/release
