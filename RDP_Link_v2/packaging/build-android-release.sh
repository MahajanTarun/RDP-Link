#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../android"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}"
# Load the release keystore credentials stored by the developer (or CI).
# Exporting them matters: Gradle only sees exported environment variables.
if [[ -f "$HOME/.config/officerdp/android-signing.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$HOME/.config/officerdp/android-signing.env"
  set +a
fi
echo "==> bundleRelease"
gradle bundleRelease
echo "==> assembleRelease"
gradle assembleRelease
echo "Artifacts:"
ls -lh app/build/outputs/apk/release app/build/outputs/bundle/release
