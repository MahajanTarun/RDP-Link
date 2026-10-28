#!/bin/zsh
# Build "RDP Link.app" from source (macOS).
# No Xcode required — PyInstaller ad-hoc signs the bundle.
#
#   ./build-app.sh             build + install to ~/Applications + launch
#   ./build-app.sh --no-open   build + install, don't launch
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PY="${PYTHON:-python3}"
NO_OPEN=0
[[ "${1:-}" == "--no-open" ]] && NO_OPEN=1

echo "==> Checking Python + Tkinter"
"$PY" - <<'EOF'
import sys, tkinter
print(f"Python {sys.version.split()[0]}, Tk {tkinter.TkVersion}")
EOF

echo "==> Ensuring PyInstaller"
"$PY" -m pip install --user --upgrade pyinstaller >/dev/null

echo "==> Generating icon (if needed)"
if [[ ! -f assets/icon.icns ]]; then
  "$PY" tools/gen_icon.py
fi

echo "==> Building .app bundle"
# Ultra-light build: exclude test/doc/tooling modules from the bundle.
"$PY" -m PyInstaller \
  --windowed \
  --noconfirm \
  --clean \
  --name "RDP Link" \
  --osx-bundle-identifier com.eminentailabs.rdplink \
  --icon assets/icon.icns \
  --add-data "receiver:receiver" \
  --exclude-module pytest \
  --exclude-module unittest \
  --exclude-module pydoc_data \
  --exclude-module test \
  ubuntu_rdp_manager.py

APP="dist/RDP Link.app"
[[ -d "$APP" ]] || { echo "Build failed: $APP not found"; exit 1; }

# Info.plist extras: minimum OS + camera/mic usage descriptions so macOS
# shows proper TCC permission prompts attributed to this app.
PLIST="$APP/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Add :LSMinimumSystemVersion string 11.0" "$PLIST" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Add :NSMicrophoneUsageDescription string \"RDP Link sends your microphone to the remote session when microphone redirection is enabled.\"" "$PLIST" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Add :NSCameraUsageDescription string \"RDP Link can stream your camera to the remote machine when the camera bridge is enabled.\"" "$PLIST" 2>/dev/null || true

# Re-sign after editing Info.plist so the signature matches its contents.
/usr/bin/codesign --force --deep --sign - "$APP"

mkdir -p "$HOME/Applications"
rm -rf "$HOME/Applications/RDP Link.app"
cp -R "$APP" "$HOME/Applications/"
echo "Installed: $HOME/Applications/RDP Link.app"

if [[ $NO_OPEN == 1 ]]; then
  echo "(--no-open: skipping launch)"
else
  open "$HOME/Applications/RDP Link.app"
fi
