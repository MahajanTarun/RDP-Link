#!/bin/bash
# Build a Linux binary: dist/ubuntu-rdp-manager (single executable).
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
  --name ubuntu-rdp-manager \
  --add-data "receiver:receiver" \
  --exclude-module pytest \
  --exclude-module unittest \
  --exclude-module pydoc_data \
  --exclude-module test \
  ubuntu_rdp_manager.py

echo "Built: dist/ubuntu-rdp-manager"
echo "Run:   ./dist/ubuntu-rdp-manager            (GUI)"
echo "       ./dist/ubuntu-rdp-manager --list      (list profiles)"
echo "       ./dist/ubuntu-rdp-manager --connect 'Name'"
