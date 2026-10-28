#!/bin/bash
# mic-receiver.sh — run on the LINUX HOST (not the guest Mac/Linux client).
# Turns the client's microphone bridge stream into a local "MacMic" input
# that Teams / Zoom / browsers can select. Works with xrdp 0.9.x — it does
# NOT use the RDP audio channel at all.
#
# Zero-setup usage (from your client machine, one click in the app):
#   scp camera-receiver.sh mic-receiver.sh user@host:/tmp/
#   ssh -t user@host 'sudo -v; bash /tmp/mic-receiver.sh --install; \
#                     bash /tmp/camera-receiver.sh --install'
#
# On the host directly:
#   ./mic-receiver.sh --install     # deps + systemd user service + firewall
#   ./mic-receiver.sh --run         # foreground run (what the service runs)
#   systemctl --user status ubuntu-rdp-mic-receiver
#
# One-time manual deps:
#   sudo apt install -y ffmpeg pulseaudio-utils   # pactl; pipewire-pulse works too

set -u

DATA_PORT=7273
CTL_PORT=7275
FIFO="$XDG_RUNTIME_DIR/mac-mic.fifo"
[[ -d "$XDG_RUNTIME_DIR" ]] || FIFO=/tmp/mac-mic.fifo
SERVICE=ubuntu-rdp-mic-receiver
SUDO=""
[[ "$(id -u)" == "0" ]] || SUDO="sudo"

# --------------------------------------------------------------------------
start_responder() {
  DATA_PORT=$DATA_PORT CTL_PORT=$CTL_PORT ROLE=mic \
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
    t = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        t.bind(("0.0.0.0", data_port))
        state = "closed"; t.close()
    except OSError:
        state = "open"
    except Exception:
        state = "unknown"
    s.sendto(f"RDPCTL1 PONG role={role} data={state}".encode(), addr)
PY
  RESPONDER_PID=$!
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

check() {
  echo "== pulse/pipewire =="
  pactl info >/dev/null 2>&1 && echo "  sound server reachable" \
    || echo "  pactl failed — is a user sound server running?"
  echo "== virtual source =="
  pactl list short sources 2>/dev/null | grep -i macmic \
    || echo "  'MacMic' source not loaded"
  echo "== receiver =="
  systemctl --user is-active "$SERVICE" 2>/dev/null | grep -q active \
    && echo "  systemd user service '$SERVICE' is ACTIVE" \
    || pgrep -af "udp://0.0.0.0:$DATA_PORT" \
    || echo "  no receiver listening on udp port $DATA_PORT"
  echo "== control responder =="
  python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(2)
s.sendto(b'RDPCTL1 PING', ('127.0.0.1', $CTL_PORT))
print('  responding:', s.recvfrom(256)[0].decode())
" 2>/dev/null || echo "  control responder NOT running (install with --install)"
  check_firewall || true
}

install_deps() {
  local missing=()
  command -v ffmpeg >/dev/null || missing+=("ffmpeg")
  command -v pactl >/dev/null || missing+=("pulseaudio-utils")
  [[ ${#missing[@]} -eq 0 ]] && return 0
  echo "Installing missing packages: ${missing[*]}"
  if command -v apt >/dev/null; then
    $SUDO apt update -qq && $SUDO apt install -y "${missing[@]}"
  elif command -v dnf >/dev/null; then
    $SUDO dnf install -y ffmpeg pulseaudio-utils
  elif command -v pacman >/dev/null; then
    $SUDO pacman -Sy --noconfirm ffmpeg libpulse
  else
    echo "Unsupported package manager — install ${missing[*]} manually." >&2
    exit 1
  fi
}

do_install() {
  install_deps

  echo "Installing service binary…"
  mkdir -p "$HOME/.local/bin"
  install -m 0755 "${BASH_SOURCE[0]}" "$HOME/.local/bin/$SERVICE"

  echo "Installing systemd user service…"
  mkdir -p "$HOME/.config/systemd/user"
  cat > "$HOME/.config/systemd/user/$SERVICE.service" <<EOF
[Unit]
Description=Ubuntu RDP Manager mic receiver (MacMic)

[Service]
Type=simple
ExecStart=%h/.local/bin/$SERVICE --run
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now "$SERVICE"
  # let the service run before first login too (best effort)
  $SUDO loginctl enable-linger "$USER" 2>/dev/null || true

  if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then
    $SUDO ufw status | grep -qE "727[2-5]/udp" || $SUDO ufw allow 7272:7275/udp || true
  fi

  echo ""
  echo "Installed ✓ — user service '$SERVICE' is enabled and running."
  echo "In Teams/Zoom on this host, select the 'MacMic' input device."
  echo "Verify anytime with:  $SERVICE --check"
}

do_uninstall() {
  systemctl --user disable --now "$SERVICE" 2>/dev/null || true
  rm -f "$HOME/.config/systemd/user/$SERVICE.service" \
        "$HOME/.local/bin/$SERVICE"
  systemctl --user daemon-reload
  echo "Uninstalled."
}

# --------------------------------------------------------------------------
run_receiver() {
  check_firewall || true
  command -v pactl >/dev/null || { echo "pactl not found — run --install" >&2; exit 1; }

  # Reload the virtual source fresh each run
  if pactl list short modules 2>/dev/null | grep -q "module-pipe-source.*$FIFO"; then
    pactl unload-module "$(pactl list short modules | grep "module-pipe-source.*$FIFO" | cut -f1)"
  fi
  pactl load-module module-pipe-source file=$FIFO source_name=MacMic \
        source_properties=device.description=MacMic format=s16le rate=48000 channels=1

  start_responder
  trap 'kill $RESPONDER_PID 2>/dev/null' EXIT

  echo "Receiving Mac microphone on udp://0.0.0.0:$DATA_PORT -> source 'MacMic'"
  echo "In Teams/Zoom on this host, select the 'MacMic' input device."
  ffmpeg -hide_banner -loglevel error -fflags nobuffer \
    -i "udp://0.0.0.0:$DATA_PORT" -f s16le -ac 1 -ar 48000 "$FIFO"
}

case "${1:-}" in
  --check)    check ;;
  --install)  do_install ;;
  --uninstall) do_uninstall ;;
  --run|"")   run_receiver ;;
  *) echo "usage: $0 [--check|--install|--uninstall|--run]"; exit 2 ;;
esac
