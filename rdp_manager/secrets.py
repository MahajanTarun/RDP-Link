"""Secret storage with zero dependencies.

- macOS: Keychain (generic password), same service id as v3 so existing
  users' passwords keep working.
- Linux: `secret-tool` (libsecret) when available; otherwise a 0600 JSON
  file under the config dir (documented plaintext fallback).

Hex scheme (fixes a macOS 26 quirk): `security -w` prints NON-ASCII stored
passwords hex-encoded, corrupting them on read. We now always store
"hex:" + hexdump; reads transparently decode the prefix and fall back to
raw values for passwords written by v3.
"""

import json
import os
import subprocess
import sys

from . import SECRET_SERVICE
from .paths import ASKPASS_PATH, SUPPORT_DIR, ensure_dirs

IS_MAC = sys.platform == "darwin"

_SECRETS_FILE = os.path.join(SUPPORT_DIR, "secrets.json")
_HEX = "hex:"


# --------------------------------------------------------------- low level --

def _security(*args):
    try:
        return subprocess.run(["/usr/bin/security", *args], capture_output=True)
    except OSError:
        return None


def _secret_tool(*args, input_bytes=None):
    """secret-tool reads the secret from STDIN when storing — must be piped."""
    try:
        return subprocess.run(["secret-tool", *args], capture_output=True,
                              input=input_bytes)
    except OSError:
        return None


def _file_read(account):
    try:
        with open(_SECRETS_FILE, "r", encoding="utf-8") as f:
            return json.load(f).get(account)
    except (OSError, ValueError, json.JSONDecodeError):
        return None


def _file_write(account, secret_hex):
    ensure_dirs()
    data = {}
    try:
        with open(_SECRETS_FILE, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError, json.JSONDecodeError):
        data = {}
    if secret_hex is None:
        data.pop(account, None)
    else:
        data[account] = secret_hex
    tmp = _SECRETS_FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(data, f)
    os.replace(tmp, _SECRETS_FILE)
    try:
        os.chmod(_SECRETS_FILE, 0o600)
    except OSError:
        pass


# ----------------------------------------------------------------- public --

def store_set(account, password):
    """Store a password; returns True on success. Stored as hex+prefix."""
    blob = _HEX + password.encode("utf-8").hex()
    if IS_MAC:
        r = _security("add-generic-password", "-s", SECRET_SERVICE, "-a", account,
                      "-w", blob, "-U")
        if r is not None and r.returncode == 0:
            return True
        # fall through to file if Keychain refused
    r = _secret_tool("store", "service", "rdp-link",
                     "username", account, input_bytes=(blob + "\n").encode())
    if r is not None and r.returncode == 0:
        return True
    _file_write(account, blob)
    return True


def store_get(account):
    """Return the password or None."""
    raw = None
    if IS_MAC:
        r = _security("find-generic-password", "-s", SECRET_SERVICE, "-a", account, "-w")
        if r is not None and r.returncode == 0:
            out = r.stdout.decode("utf-8", "replace")
            raw = out[:-1] if out.endswith("\n") else out
    else:
        r = _secret_tool("lookup", "service", "rdp-link",
                         "username", account)
        if r is not None and r.returncode == 0 and r.stdout.strip():
            raw = r.stdout.decode("utf-8", "replace").strip()
    if raw is None:
        raw = _file_read(account)
    if raw is None:
        return None
    if raw.startswith(_HEX):
        try:
            return bytes.fromhex(raw[len(_HEX):]).decode("utf-8")
        except (ValueError, UnicodeDecodeError):
            return None
    return raw  # legacy v3 item (raw ASCII)


def store_has(account):
    return store_get(account) is not None


def store_delete(account):
    if IS_MAC:
        _security("delete-generic-password", "-s", SECRET_SERVICE, "-a", account)
    _file_write(account, None)


# ---------------------------------------------------------------- askpass --

def install_askpass():
    """Write the askpass helper (fallback for FreeRDP builds without
    /from-stdin support). Decodes the hex: scheme; xxd with perl fallback."""
    script = """#!/bin/zsh
set -e
PROFILE_ID="${RDP_PROFILE_ID:-}"
[[ -n "$PROFILE_ID" ]] || exit 1
VAL=$(/usr/bin/security find-generic-password -s "%s" -a "$PROFILE_ID" -w)
case "$VAL" in
  hex:*) PRINTED=${VAL#hex:}
         { /usr/bin/xxd -r -p 2>/dev/null || /usr/bin/perl -ne 'print pack("H*", $_)' ; } <<< "$PRINTED" ;;
  *)     print -r -- "$VAL" ;;
esac
""" % SECRET_SERVICE
    try:
        ensure_dirs()
        with open(ASKPASS_PATH, "w", encoding="utf-8") as f:
            f.write(script)
        os.chmod(ASKPASS_PATH, 0o700)
    except OSError:
        pass
