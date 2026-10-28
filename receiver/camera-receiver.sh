#!/bin/bash
# camera-receiver.sh — run on the LINUX HOST (not the guest Mac/Linux client).
# Turns the client's camera bridge stream into a local virtual webcam
# (/dev/video*, card "MacCamera") that Teams/Zoom/browsers can select.
#
# Zero-setup usage (from your client machine, one click in the app):
#   scp camera-receiver.sh mic-receiver.sh user@host:/tmp/
#   ssh -t user@host 'sudo -v; bash /tmp/camera-receiver.sh --install; \
#                     bash /tmp/mic-receiver.sh --install'
#
# On the host directly:
#   ./camera-receiver.sh --install    # deps + systemd service + firewall (sudo)
#   ./camera-receiver.sh --check      # diagnose why the camera isn't showing
#   ./camera-receiver.sh --run        # foreground run (what the service runs)
#   ./camera-receiver.sh              # same as --run
#   sudo systemctl status ubuntu-rdp-camera-receiver
#
# One-time manual deps (the installer does this too):
#   sudo apt install -y v4l2loopback-dkms ffmpeg v4l-utils

set -u

LABEL=MacCamera
DATA_PORT=7272
CTL_PORT=7274
PARAMS="devices=1 video_nr=10 card_label=$LABEL exclusive_caps=1"
SERVICE=ubuntu-rdp-camera-receiver
SUDO=""
[[ "$(id -u)" == "0" ]] || SUDO="sudo"

die() { echo "ERROR: $*" >&2; exit 1; }

# --------------------------------------------------------------------------
# Control responder: answers the client's UDP handshake so the app can say
# "receiver detected" instead of streaming into the void.
start_responder() {
  DATA_PORT=$DATA_PORT CTL_PORT=$CTL_PORT ROLE=camera \
  python3 - <<'PY' &
import socket, os
role = os.environ["ROLE"]
ctl = int(os.environ["CTL_PORT"])
data_port = int(os.environ["DATA_PORT"])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", ctl))
while True:
    try:
        data, addr = s.recvfrom(256)
    except OSError:
        continue
    if data.strip() != b"RDPCTL1 PING":
        continue
    # If ffmpeg's data receiver is bound we cannot bind the same port.
    t = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        t.bind(("0.0.0.0", data_port))
        state = "closed"      # nothing listening -> ffmpeg receiver is down
        t.close()
    except OSError:
        state = "open"        # port in use -> data receiver is listening
    except Exception:
        state = "unknown"
    s.sendto(f"RDPCTL1 PONG role={role} data={state}".encode(), addr)
PY
  RESPONDER_PID=$!
}

# --------------------------------------------------------------------------
find_node() {
  for dev in /dev/video*; do
    [[ -e "$dev" ]] || continue
    if v4l2-ctl -d "$dev" --all 2>/dev/null | grep -q "Driver name.*v4l2loopback"; then
      echo "$dev"; return 0
    fi
  done
  return 1
}

ensure_module() {
  if ! lsmod | grep -q v4l2loopback; then
    echo "Loading v4l2loopback ($PARAMS)…"
    $SUDO modprobe v4l2loopback $PARAMS || die "modprobe v4l2loopback failed"
  fi
  if [[ "$($SUDO cat /sys/module/v4l2loopback/parameters/exclusive_caps 2>/dev/null)" != "1" ]]; then
    echo "Reloading module with exclusive_caps=1 (required by Teams/Chrome)…"
    $SUDO modprobe -r v4l2loopback || true
    $SUDO modprobe v4l2loopback $PARAMS || die "modprobe failed"
  fi
}

check_firewall() {
  command -v ufw >/dev/null 2>&1 || return 0
  if $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then
    if ! $SUDO ufw status 2>/dev/null | grep -qE "727[2-5]/udp"; then
      echo "WARNING: ufw is active but UDP 7272-7275 is not allowed."
      echo "  Fix:  sudo ufw allow 7272:7275/udp"
      return 1
    fi
  fi
  return 0
}

# --------------------------------------------------------------------------
check() {
  echo "== kernel module =="
  lsmod | grep v4l2loopback >/dev/null \
    && echo "  v4l2loopback loaded" \
    || echo "  v4l2loopback NOT loaded -> sudo modprobe v4l2loopback $PARAMS"
  echo "== loopback device =="
  if node=$(find_node); then
    echo "  found: $node"
    caps=$(cat /sys/module/v4l2loopback/parameters/exclusive_caps 2>/dev/null)
    echo "  exclusive_caps=$caps  (must be 1 or Teams/Chrome won't list it)"
  else
    echo "  no v4l2loopback device node found"
  fi
  echo "== receiver =="
  systemctl is-active "$SERVICE" 2>/dev/null | grep -q active \
    && echo "  systemd service '$SERVICE' is ACTIVE" \
    || pgrep -af "udp://0.0.0.0:$DATA_PORT" \
    || echo "  no receiver listening on udp port $DATA_PORT"
  echo "== control responder =="
  python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(2)
s.sendto(b'RDPCTL1 PING', ('127.0.0.1', $CTL_PORT))
print('  responding:', s.recvfrom(256)[0].decode())
" 2>/dev/null || echo "  control responder NOT running (install with --install)"
  echo "== firewall =="
  check_firewall || true
  echo "== all video devices =="
  v4l2-ctl --list-devices 2>/dev/null || true
}

# --------------------------------------------------------------------------
install_deps() {
  local missing=()
  command -v ffmpeg >/dev/null || missing+=("ffmpeg")
  command -v v4l2-ctl >/dev/null || missing+=("v4l-utils")
  modinfo v4l2loopback >/dev/null 2>&1 || missing+=("v4l2loopback-dkms")
  [[ ${#missing[@]} -eq 0 ]] && return 0
  echo "Installing missing packages: ${missing[*]}"
  if command -v apt >/dev/null; then
    $SUDO apt update -qq && $SUDO apt install -y "${missing[@]}"
  elif command -v dnf >/dev/null; then
    $SUDO dnf install -y v4l2loopback v4l-utils ffmpeg
  elif command -v pacman >/dev/null; then
    $SUDO pacman -Sy --noconfirm v4l2loopback-dkms v4l-utils ffmpeg
  else
    die "unsupported package manager — install ${missing[*]} manually"
  fi
}

do_install() {
  install_deps
  ensure_module
  # persist across reboots
  echo "v4l2loopback" | $SUDO tee /etc/modules-load.d/v4l2loopback.conf >/dev/null
  echo "options v4l2loopback $PARAMS" | $SUDO tee /etc/modprobe.d/v4l2loopback.conf >/dev/null

  echo "Installing service binary…"
  $SUDO install -m 0755 "${BASH_SOURCE[0]}" "/usr/local/bin/$SERVICE"

  echo "Installing systemd service (system level — needs root for the kernel module)…"
  $SUDO tee "/etc/systemd/system/$SERVICE.service" >/dev/null <<EOF
[Unit]
Description=Ubuntu RDP Manager camera receiver (MacCamera)
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/$SERVICE --run
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now "$SERVICE"

  if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then
    $SUDO ufw status | grep -qE "727[2-5]/udp" || $SUDO ufw allow 7272:7275/udp || true
  fi

  echo ""
  echo "Installed ✓  — service '$SERVICE' is enabled and running."
  echo "In Teams/Zoom on this host, select the '$LABEL' camera."
  echo "Verify anytime with:  $SERVICE --check"
}

do_uninstall() {
  $SUDO systemctl disable --now "$SERVICE" 2>/dev/null || true
  $SUDO rm -f "/etc/systemd/system/$SERVICE.service" "/usr/local/bin/$SERVICE"
  $SUDO systemctl daemon-reload
  echo "Uninstalled."
}

# --------------------------------------------------------------------------
run_receiver() {
  ensure_module
  persist_conf() {
    echo "v4l2loopback" | $SUDO tee /etc/modules-load.d/v4l2loopback.conf >/dev/null 2>&1 || true
    echo "options v4l2loopback $PARAMS" | $SUDO tee /etc/modprobe.d/v4l2loopback.conf >/dev/null 2>&1 || true
  }
  persist_conf
  check_firewall || true

  NODE=$(find_node || true)
  [[ -n "$NODE" ]] || die "no v4l2loopback device node appeared"

  start_responder
  trap 'kill $RESPONDER_PID 2>/dev/null' EXIT

  echo "Receiving camera on udp://0.0.0.0:$DATA_PORT -> $NODE (Ctrl-C to stop)"
  echo "In Teams/Zoom on this host, select the '$LABEL' camera."
  exec_ffmpeg() {
    ffmpeg -hide_banner -loglevel error -fflags nobuffer -flags low_delay \
      -i "udp://0.0.0.0:$DATA_PORT" -f v4l2 -pix_fmt yuyv422 "$NODE"
  }
  exec_ffmpeg
}

case "${1:-}" in
  --check)    check ;;
  --install)  do_install ;;
  --uninstall) do_uninstall ;;
  --run|"")   run_receiver ;;
  *) echo "usage: $0 [--check|--install|--uninstall|--run]"; exit 2 ;;
esac
