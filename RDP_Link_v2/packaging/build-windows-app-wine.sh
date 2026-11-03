#!/usr/bin/env bash
# Build the Windows desktop app (rdp-link.exe) on macOS/Linux through Wine.
#
# The Windows launcher is the same Python/Tk application used on macOS and
# Linux, frozen with PyInstaller so it needs no Python installation on the
# target machine. Wine supplies a real Windows Python runtime for the build.
#
#   bash packaging/build-windows-app-wine.sh
#
# Environment:
#   WINE        path to the Wine binary (default: Wine Staging.app bundle)
#   WINEPREFIX  Wine prefix (default: ~/.wine-officerdp)
#   PYROOT      Windows Python tree (default: ~/.local/python-win)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist/windows"
mkdir -p "$DIST"

# Wine, the Windows Python tree, the Wine prefix and temp files live in
# .build/ (SSD, git-ignored); see packaging/env.sh.
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
PY_VERSION="3.12.16+20261026"
PY_URL="https://github.com/astral-sh/python-build-standalone/releases/download/20261026/cpython-3.12.16%2B20261026-x86_64-pc-windows-msvc-install_only.tar.gz"

export WINEDEBUG=-all WINEDLLOVERRIDES="mscoree,mshtml="

if [[ ! -x "$WINE" ]]; then
  echo "error: Wine not found at $WINE (set WINE=...)" >&2
  exit 1
fi

if [[ ! -x "$PYROOT/python.exe" ]]; then
  echo "==> Fetching Windows Python $PY_VERSION"
  mkdir -p "$PYROOT"
  tmp="$(mktemp -d)"
  curl -L --fail -sS -o "$tmp/python.tar.gz" "$PY_URL"
  tar -xzf "$tmp/python.tar.gz" -C "$PYROOT" --strip-components=1
  rm -rf "$tmp"
fi

"$WINE" wineboot -i >/dev/null 2>&1 || true

if ! "$WINE" "$PYROOT/python.exe" -c "import PyInstaller" >/dev/null 2>&1; then
  echo "==> Installing PyInstaller into the Wine Python"
  "$WINE" "$PYROOT/python.exe" -m pip install --disable-pip-version-check \
    --no-warn-script-location --upgrade pyinstaller pillow >/dev/null
fi

cd "$ROOT/app"
if [[ ! -f assets/icon.ico ]]; then
  "$WINE" "$PYROOT/python.exe" - <<'PY'
from PIL import Image
src = Image.open(r"assets\icon_512.png").convert("RGBA")
src.save(r"assets\icon.ico", format="ICO",
         sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
PY
fi

echo "==> Building rdp-link.exe with PyInstaller"
"$WINE" "$PYROOT/python.exe" -m PyInstaller \
  --noconfirm --clean --onefile --name rdp-link \
  --icon assets/icon.ico \
  --hidden-import office_viewer \
  --add-data "receiver;receiver" \
  --exclude-module pytest --exclude-module unittest \
  --exclude-module pydoc_data --exclude-module test \
  ubuntu_rdp_manager.py 2>&1 | tail -3

cp "dist/rdp-link.exe" "$DIST/rdp-link.exe"
echo "Built: $DIST/rdp-link.exe"
