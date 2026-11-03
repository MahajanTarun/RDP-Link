#!/usr/bin/env bash
# Seed the three test devices with each other's LAN entries so LAN/WAN testing
# needs no typing. Safe to re-run: existing entries are kept, these are
# promoted to the front of the 8-entry recent list.
#
#   bash scripts/seed-connections.sh [mac_host:port] [linux_host:port] [android_host:port]
#
# Linux is reached over SSH (override with LINUX_SSH), Android over adb
# (override with ANDROID_SERIAL). Android's address defaults to the Wi-Fi IP
# reported by the device.
set -euo pipefail

MAC="${1:-192.168.1.14:8088}"
LINUX="${2:-192.168.1.13:8088}"
ANDROID="${3:-}"
LINUX_SSH="${LINUX_SSH:-tarun@MCITLPTP810.local}"
ANDROID_SERIAL="${ANDROID_SERIAL:-}"

if [[ -z "$ANDROID" ]] && command -v adb >/dev/null 2>&1; then
  serial_args=()
  [[ -n "$ANDROID_SERIAL" ]] && serial_args=(-s "$ANDROID_SERIAL")
  ip=$(adb "${serial_args[@]}" shell ip -f inet addr show wlan0 2>/dev/null \
        | awk '/inet /{print $2}' | cut -d/ -f1 | head -1)
  [[ -n "$ip" ]] && ANDROID="$ip:8088"
fi
ANDROID="${ANDROID:-192.168.1.10:8088}"

echo "mac=$MAC linux=$LINUX android=$ANDROID"

python3 - "$MAC" "$LINUX" "$ANDROID" <<'PY'
import json, os, sys
mac, linux, android = sys.argv[1:4]
path = os.path.expanduser("~/.config/office-rdp/gui.json")
os.makedirs(os.path.dirname(path), exist_ok=True)
try:
    data = json.load(open(path))
except Exception:
    data = {}
recent = [x for x in data.get("recent", []) if x]
for entry in (android, linux, mac):
    if entry in recent:
        recent.remove(entry)
    recent.insert(0, entry)
data["recent"] = recent[:8]
json.dump(data, open(path, "w"), indent=2)
print("seeded", path)
PY

if command -v sshpass >/dev/null 2>&1 && [[ -n "${LINUX_PASSWORD:-}" ]]; then
  sshpass -p "$LINUX_PASSWORD" ssh -o StrictHostKeyChecking=no -o PubkeyAuthentication=no \
    -o PreferredAuthentications=password "$LINUX_SSH" \
    "python3 - '$MAC' '$ANDROID' <<'PY'
import json, os, sys
mac, android = sys.argv[1:3]
path = os.path.expanduser('~/.config/office-rdp/gui.json')
os.makedirs(os.path.dirname(path), exist_ok=True)
try:
    data = json.load(open(path))
except Exception:
    data = {}
recent = [x for x in data.get('recent', []) if x]
for entry in (android, mac):
    if entry in recent:
        recent.remove(entry)
    recent.insert(0, entry)
data['recent'] = recent[:8]
json.dump(data, open(path, 'w'), indent=2)
print('seeded', path)
PY" || echo "warning: could not seed the Linux device"
fi

if command -v adb >/dev/null 2>&1; then
  serial_args=()
  [[ -n "$ANDROID_SERIAL" ]] && serial_args=(-s "$ANDROID_SERIAL")
  adb "${serial_args[@]}" shell am start -n com.eminentailabs.officerdp/.MainActivity \
    --es seed "$MAC,$LINUX" >/dev/null 2>&1 \
    && echo "seeded Android recents" || echo "warning: could not seed Android"
fi
