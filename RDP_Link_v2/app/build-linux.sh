#!/bin/bash
# Build a Linux binary: dist/rdp-link (single executable).
# Requirements: python3-tk (Tk), pip install pyinstaller
#
#   ./build-linux.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PY="${PYTHON:-python3}"

"$PY" -m pip install --user --upgrade pyinstaller >/dev/null

if [[ ! -f assets/icon_256.png ]]; then
  "$PY" tools/gen_icon.py || true
fi

"$PY" -m PyInstaller \
  --onefile \
  --noconfirm \
  --clean \
  --name rdp-link \
  --add-data "receiver:receiver" \
  --exclude-module pytest \
  --exclude-module unittest \
  --exclude-module pydoc_data \
  --exclude-module test \
  ubuntu_rdp_manager.py

echo "Built: dist/rdp-link"
echo "Run:   ./dist/rdp-link            (GUI)"
echo "       ./dist/rdp-link --list      (list profiles)"
echo "       ./dist/rdp-link --connect 'Name' | --list | --version"
