#!/usr/bin/env bash
# License audit: every Go module that can be linked into an RDP Link binary must
# carry a permissive license. Copyleft (GPL/AGPL/LGPL) in a linked module is a
# release blocker; the only accepted GPL component is PyInstaller, which is a
# build tool covered by its Bootloader Exception and is not linked as Go code.
#
#   bash scripts/license-audit.sh
#
# Runs as part of scripts/security-scan.sh and in CI.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

targets=("darwin arm64" "linux amd64" "linux arm64" "windows amd64" "android arm64")

echo "==> collecting modules linked for every target"
mods="$(
  for t in "${targets[@]}"; do
    # shellcheck disable=SC2086
    set -- $t
    CGO_ENABLED=1 GOOS="$1" GOARCH="$2" \
      go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' \
      ./cmd/office-rdp ./cmd/rendezvous ./mobile 2>/dev/null || true
  done | sort -u | grep -v '^$' | grep -v '^github.com/eminentailabs/office-rdp$'
)"

fail=0
count=0
printf '%-52s %-10s %s\n' "MODULE" "VERSION" "LICENSE"
printf '%-52s %-10s %s\n' "------" "-------" "-------"

while read -r mod; do
  [ -n "$mod" ] || continue
  info="$(go list -m -f '{{.Version}}|{{.Dir}}' "$mod" 2>/dev/null || true)"
  ver="${info%%|*}"
  dir="${info##*|}"
  lic="UNKNOWN"
  if [ -n "$dir" ] && [ -d "$dir" ]; then
    file=""
    for candidate in LICENSE LICENSE.md LICENSE.txt LICENCE COPYING; do
      if [ -f "$dir/$candidate" ]; then file="$dir/$candidate"; break; fi
    done
    if [ -n "$file" ]; then
      if grep -qiE 'GNU (LESSER |AFFERO )?GENERAL PUBLIC LICENSE' "$file"; then
        lic="COPYLEFT"
      elif grep -qi 'Apache License' "$file" && grep -qi 'Version 2.0' "$file"; then
        lic="Apache-2.0"
      elif grep -qi 'Neither the name' "$file" && grep -qi 'Redistributions' "$file"; then
        lic="BSD-3-Clause"
      elif grep -qi 'Redistributions' "$file"; then
        lic="BSD-2-Clause"
      elif grep -qi 'Permission is hereby granted, free of charge' "$file"; then
        lic="MIT"
      elif grep -qi 'Permission to use, copy, modify, and/or distribute' "$file"; then
        lic="ISC"
      else
        lic="REVIEW"
      fi
    fi
  fi
  printf '%-52s %-10s %s\n' "$mod" "${ver:-?}" "$lic"
  count=$((count + 1))
  case "$lic" in
    COPYLEFT|UNKNOWN|REVIEW)
      echo "  ^ release blocker: $mod has a non-permissive or unclassified license"
      fail=1
      ;;
  esac
done <<<"$mods"

echo
echo "checked $count modules"
if [ "$fail" -ne 0 ]; then
  echo "LICENSE AUDIT FAILED: resolve the modules marked above (see THIRD_PARTY_NOTICES.md)"
  exit 1
fi
echo "LICENSE AUDIT PASSED: all linked Go modules are permissive (MIT/BSD/Apache)"
