#!/bin/bash
# install.sh — one-command installer for Ubuntu RDP Manager.
#
# Guest (client) + GUI and CLI in one binary/app:
#   curl -fsSL https://raw.githubusercontent.com/YOURORG/UbuntuRDPManagerApp/main/install.sh -o /tmp/urdm-install.sh
#   bash /tmp/urdm-install.sh guest          # guest on macOS or Linux
#
# Host (the Ubuntu machine being connected to):
#   bash /tmp/urdm-install.sh host
#
# From a local checkout instead of the network:
#   ./install.sh guest | host | both | --uninstall
#
# After installing the guest you get BOTH interfaces:
#   macOS:  "Ubuntu RDP Manager" app (GUI)   +  `urdm` command (CLI)
#   Linux:  `ubuntu-rdp-manager` / `urdm`    (GUI with no args, CLI with args)
set -euo pipefail

REPO_URL="https://github.com/YOURORG/UbuntuRDPManagerApp.git"   # adjust for your org
APP_NAME="Ubuntu RDP Manager"
ROLE="${1:-guest}"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARNING:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

SUDO=""
[[ "$(id -u)" == 0 ]] || SUDO="sudo"

# ---------------------------------------------------------------- source ---
# Use the checkout we're in, or clone to a temp dir.
if [[ -f "$(dirname "$0")/receiver/camera-receiver.sh" ]]; then
  ROOT="$(cd "$(dirname "$0")" && pwd)"
elif have git; then
  ROOT="$(mktemp -d)/UbuntuRDPManagerApp"
  log "Fetching $APP_NAME source…"
  git clone --depth 1 "$REPO_URL" "$ROOT" >/dev/null 2>&1 \
    || die "couldn't clone $REPO_URL — check the URL"
else
  die "git not found — install git, or run this from a project checkout"
fi

OS="$(uname -s)"

# ---------------------------------------------------------------- uninstall --
do_uninstall() {
  case "$OS" in
    Darwin)
      rm -rf "$HOME/Applications/$APP_NAME.app"
      $SUDO rm -f /usr/local/bin/urdm /usr/local/bin/ubuntu-rdp-manager
      log "macOS guest uninstalled (brew formulae freerdp/ffmpeg kept)."
      ;;
    Linux)
      $SUDO rm -f /usr/local/bin/ubuntu-rdp-manager /usr/local/bin/urdm \
              /usr/local/bin/ubuntu-rdp-camera-receiver
      systemctl --user disable --now ubuntu-rdp-mic-receiver 2>/dev/null || true
      rm -f "$HOME/.config/systemd/user/ubuntu-rdp-mic-receiver.service" \
            "$HOME/.local/bin/ubuntu-rdp-mic-receiver"
      systemctl --user daemon-reload 2>/dev/null || true
      log "uninstalled."
      ;;
  esac
}

# ---------------------------------------------------------------- guest ----
install_guest_mac() {
  have brew || die "Homebrew missing — install it from https://brew.sh first"
  log "Installing runtime dependencies (freerdp, ffmpeg)…"
  brew install freerdp ffmpeg
  log "Building $APP_NAME.app…"
  (cd "$ROOT" && ./build-app.sh --no-open)
  log "Installing CLI companion (same app binary — GUI if no args)…"
  $SUDO tee /usr/local/bin/urdm >/dev/null <<EOF
#!/bin/bash
exec "$HOME/Applications/$APP_NAME.app/Contents/MacOS/$APP_NAME" "\$@"
EOF
  $SUDO chmod +x /usr/local/bin/urdm
  log "Installed ✓"
  echo "    GUI: open '$APP_NAME' (~/Applications) — or Launchpad"
  echo "    CLI: urdm --list | urdm --connect \"Profile\""
}

install_guest_linux() {
  log "Installing runtime dependencies…"
  if have apt; then
    $SUDO apt-get update -qq
    $SUDO apt-get install -y git python3 python3-pip python3-tk ffmpeg \
        freerdp3-x11 2>/dev/null || $SUDO apt-get install -y git python3 \
        python3-pip python3-tk ffmpeg freerdp2-x11
  elif have dnf; then
    $SUDO dnf install -y git python3 python3-tkinter ffmpeg freerdp
  elif have pacman; then
    $SUDO pacman -Sy --noconfirm --needed git python tk ffmpeg freerdp
  else
    die "unsupported distro — install git, python3-tk, ffmpeg and freerdp3 manually"
  fi
  log "Building single-file binary…"
  (cd "$ROOT" && ./build-linux.sh)
  log "Installing to /usr/local/bin (GUI and CLI in one binary)…"
  $SUDO install -m 0755 "$ROOT/dist/ubuntu-rdp-manager" /usr/local/bin/ubuntu-rdp-manager
  $SUDO ln -sf /usr/local/bin/ubuntu-rdp-manager /usr/local/bin/urdm
  log "Installed ✓"
  echo "    GUI: run 'ubuntu-rdp-manager' (no args)"
  echo "    CLI: ubuntu-rdp-manager --list | --connect \"Profile\""
}

# ---------------------------------------------------------------- host -----
install_host() {
  [[ "$OS" == "Linux" ]] || die "the host side installs on Linux (Ubuntu) only"
  log "Installing camera receiver (deps + systemd service + firewall)…"
  "$ROOT/receiver/camera-receiver.sh" --install
  log "Installing microphone receiver…"
  "$ROOT/receiver/mic-receiver.sh" --install
  log "Host ready ✓"
  echo "    Verify: ubuntu-rdp-camera-receiver --check"
}

install_host_reminder() { :; }

case "$ROLE" in
  guest)
    case "$OS" in
      Darwin) install_guest_mac ;;
      Linux)  install_guest_linux ;;
      *)      die "unsupported OS: $OS" ;;
    esac
    install_host_reminder ;;
  host)        install_host ;;
  both)
    case "$OS" in
      Darwin) install_guest_mac
              warn "host role skipped — hosting (Ubuntu) installs on Linux only" ;;
      Linux)  install_guest_linux
              install_host ;;
      *)      die "unsupported OS: $OS" ;;
    esac ;;
  --uninstall) do_uninstall ;;
  -h|--help)   sed -n '2,20p' "$0" ;;
  *)           die "usage: $0 [guest|host|both|--uninstall]" ;;
esac
