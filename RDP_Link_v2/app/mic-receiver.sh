#!/bin/bash
# Compatibility shim — the real scripts live in receiver/ now.
exec "$(dirname "$0")/receiver/mic-receiver.sh" "$@"
