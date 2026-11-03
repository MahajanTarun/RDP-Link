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
EOF

echo "==> Building app bundle with PyInstaller"
rm -rf build dist
"$PY" -m PyInstaller --noconfirm --clean "RDP Link.spec"

APP="dist/RDP Link.app"
if [[ ! -x "$APP/Contents/MacOS/RDP Link" ]]; then
  echo "error: PyInstaller did not produce $APP" >&2
  exit 1
fi
chmod +x "$APP/Contents/MacOS/RDP Link"
echo "Built: $ROOT/$APP"

if [[ "$NO_OPEN" == "0" ]]; then
  mkdir -p "$HOME/Applications"
  rm -rf "$HOME/Applications/RDP Link.app"
  cp -R "$APP" "$HOME/Applications/"
  open "$HOME/Applications/RDP Link.app"
fi
