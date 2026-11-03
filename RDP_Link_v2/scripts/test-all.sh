#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
cd "$ROOT"

# Pick a Python that actually has the test dependencies. Several interpreters
# may be on PATH (system, Homebrew, python.org), and only one of them may have
# pyflakes and pytest installed.
if [[ -z "${PYTHON:-}" ]]; then
  for candidate in python3 /Library/Frameworks/Python.framework/Versions/3.14/bin/python3 /opt/homebrew/bin/python3; do
    if command -v "$candidate" >/dev/null 2>&1 \
       && "$candidate" -c "import pyflakes, pytest" >/dev/null 2>&1; then
      PYTHON="$candidate"
      break
    fi
  done
fi
PYTHON="${PYTHON:-python3}"

echo "== Go =="
go vet ./...
go test ./...
go build ./...

echo "== Assets =="
# The Android app ships the same guest UI as the desktop host; the copy in
# the app must not drift away from the original.
if ! cmp -s "$ROOT/core/host/viewer.html" "$ROOT/android/app/src/main/assets/viewer.html"; then
  echo "android assets are out of date — run scripts/sync-android-assets.sh" >&2
  exit 1
fi
if ! cmp -s "$ROOT/core/host/logo.png" "$ROOT/android/app/src/main/assets/logo.png"; then
  echo "android logo is out of date — run scripts/sync-android-assets.sh" >&2
  exit 1
fi
echo "assets in sync"

echo "== Python ($PYTHON) =="
cd app
"$PYTHON" -m pyflakes rdp_manager tests ubuntu_rdp_manager.py
"$PYTHON" -m pytest

echo "ALL TESTS PASSED"
