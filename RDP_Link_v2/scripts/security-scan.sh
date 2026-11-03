#!/usr/bin/env bash
# Security gates: static analysis, vulnerability scan, and shell lint.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/packaging/env.sh"
cd "$ROOT"

GO_BIN="${GO_BIN:-$(go env GOPATH)/bin}"
GOSEC="$GO_BIN/gosec"
GOVULN="$GO_BIN/govulncheck"

if [[ ! -x "$GOSEC" ]]; then
  echo "installing gosec…"
  go install github.com/securego/gosec/v2/cmd/gosec@latest
fi
if [[ ! -x "$GOVULN" ]]; then
  echo "installing govulncheck…"
  go install golang.org/x/vuln/cmd/govulncheck@latest
fi

echo "==> gosec (medium and above)"
# .build/ holds toolchains and caches, never product code.
CGO_ENABLED=0 "$GOSEC" -quiet -severity medium -exclude-dir=.build ./...

echo "==> govulncheck"
CGO_ENABLED=0 "$GOVULN" ./...

if command -v shellcheck >/dev/null 2>&1; then
  echo "==> shellcheck"
  shellcheck packaging/*.sh packaging/windows/*.sh scripts/*.sh
else
  echo "shellcheck not installed; skipping"
fi

echo "SECURITY SCANS PASSED"
