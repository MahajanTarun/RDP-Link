#!/usr/bin/env bash
# Shared toolchain paths for Office RDP.
#
# The Wine runtime, Windows Python, alternate Go toolchain, build caches and
# temporary files live in .build/ inside the project, on the external SSD, so
# the system volume stays free. .build/ is git-ignored. Every value can be
# overridden from the environment.
#
#   source packaging/env.sh
#
_office_rdp_root="${OFFICE_RDP_REPO_ROOT:-}"
if [ -z "$_office_rdp_root" ]; then
  if [ -n "${BASH_SOURCE:-}" ]; then
    _office_rdp_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  elif [ -n "${ROOT:-}" ] && [ -d "$ROOT/packaging" ]; then
    _office_rdp_root="$ROOT"
  else
    _office_rdp_root="$(pwd)"
  fi
fi

OFFICE_RDP_BUILD_ROOT="${OFFICE_RDP_BUILD_ROOT:-$_office_rdp_root/.build}"
export OFFICE_RDP_BUILD_ROOT

mkdir -p "$OFFICE_RDP_BUILD_ROOT/toolchains" \
         "$OFFICE_RDP_BUILD_ROOT/cache" \
         "$OFFICE_RDP_BUILD_ROOT/tmp" \
         "$OFFICE_RDP_BUILD_ROOT/wineprefix"

export TMPDIR="${OFFICE_RDP_TMPDIR:-$OFFICE_RDP_BUILD_ROOT/tmp}"
export GOCACHE="${GOCACHE:-$OFFICE_RDP_BUILD_ROOT/cache/go-build}"
export WINEPREFIX="${WINEPREFIX:-$OFFICE_RDP_BUILD_ROOT/wineprefix/officerdp}"
export WINE="${WINE:-$OFFICE_RDP_BUILD_ROOT/toolchains/Wine Staging.app/Contents/Resources/wine/bin/wine}"
export PYROOT="${PYROOT:-$OFFICE_RDP_BUILD_ROOT/toolchains/python-win}"
