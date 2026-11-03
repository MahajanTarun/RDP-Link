#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
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

echo "== Python ($PYTHON) =="
cd app
"$PYTHON" -m pyflakes rdp_manager tests ubuntu_rdp_manager.py
"$PYTHON" -m pytest

echo "ALL TESTS PASSED"
