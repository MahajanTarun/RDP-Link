#!/bin/zsh
# Build "RDP Link.app" from source (macOS).
# No Xcode required — PyInstaller ad-hoc signs the bundle.
#
#   ./build-app.sh             build + install to ~/Applications + launch
#   ./build-app.sh --no-open   build + install, don't launch
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PY="${PYTHON:-}"
if [[ -z "$PY" ]]; then
  for candidate in python3 /Library/Frameworks/Python.framework/Versions/3.14/bin/python3 /opt/homebrew/bin/python3; do
    if command -v "$candidate" >/dev/null 2>&1 \
       && "$candidate" -c "import tkinter, PyInstaller" >/dev/null 2>&1; then
      PY="$candidate"
      break
    fi
  done
fi
PY="${PY:-python3}"
NO_OPEN=0
[[ "${1:-}" == "--no-open" ]] && NO_OPEN=1

echo "==> Checking Python + Tkinter"
"$PY" - <<'EOF'
import sys, tkinter
print(f"Python {sys.version.split()[0]}, Tk {tkinter.TkVersion}")
