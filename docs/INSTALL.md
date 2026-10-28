# Installation

## Client (guest side) — macOS

Requirements: macOS 11+, [Homebrew](https://brew.sh).

```sh
brew bundle --file=Brewfile     # freerdp + ffmpeg
./build-app.sh                  # build + install to ~/Applications + launch
```

The build:
- bundles the app with PyInstaller (no Xcode needed; ad-hoc signed, which
  Gatekeeper accepts for locally built apps),
- adds `NSCameraUsageDescription` / `NSMicrophoneUsageDescription` to
  Info.plist and re-signs — this is what makes macOS show proper Camera /
  Microphone permission prompts attributed to **Ubuntu RDP Manager**
  (running from source instead attributes prompts to *Terminal*),
- generates the icon via `tools/gen_icon.py` if missing.

Build without launching: `./build-app.sh --no-open`.

### macOS permissions

The app requests Camera/Microphone permission itself: at first launch if any
profile routes those devices locally, and again on any connect that uses
them. If previously denied, it detects this and offers to open the right
System Settings pane; right-click a profile → **Check Permissions…** to
re-check on demand.

## Client (guest side) — Linux

Requirements: Tk (`python3-tk`), FreeRDP 3 (`freerdp3-x11` on Ubuntu 24.04+;
`freerdp2-x11` on older), `ffmpeg`.

```sh
sudo apt install -y freerdp3-x11 ffmpeg python3-tk
./build-linux.sh
./dist/ubuntu-rdp-manager
```

The Linux build is a single self-contained executable. On other distros use
`dnf install freerdp ffmpeg python3-tkinter` or the pacman equivalents.

## Host setup — automatic (recommended)

Nothing to do manually. From the app:

**right-click a profile → “Set up receivers on host…”**

This runs, in a terminal window it opens:

```sh
scp -o ConnectTimeout=8 receiver/camera-receiver.sh receiver/mic-receiver.sh \
    user@host:/tmp/
ssh -tt -o ConnectTimeout=8 user@host "sudo -v; \
    bash /tmp/camera-receiver.sh --install; \
    bash /tmp/mic-receiver.sh --install; rm -f /tmp/urdp-*.sh"
```

### What `--install` does on the host

`camera-receiver.sh --install` (system service, root):
1. installs missing packages (`v4l2loopback-dkms`, `ffmpeg`, `v4l-utils`) via
   apt/dnf/pacman,
2. loads `v4l2loopback` with `exclusive_caps=1` (required by Teams/Chrome)
   and persists the module options,
3. installs `/usr/local/bin/ubuntu-rdp-camera-receiver` and a systemd
   service (`ubuntu-rdp-camera-receiver.service`) that starts on boot and
   restarts on failure,
4. opens UDP 7272–7275 in `ufw` if the firewall is active.

`mic-receiver.sh --install` (user service):
1. installs `ffmpeg` + `pulseaudio-utils` if missing,
2. installs `~/.local/bin/ubuntu-rdp-mic-receiver` and a systemd **user**
   service (`ubuntu-rdp-mic-receiver.service`),

3. enables `loginctl enable-linger` (best effort),
4. opens the same UDP ports in `ufw`.

Both are idempotent — re-running is always safe. Uninstall with
`--uninstall`.

## Host setup — manual (no SSH from the client)

Copy `receiver/*.sh` to the host any way you like, then on the host:

```sh
sudo ./camera-receiver.sh --install
./mic-receiver.sh --install      # user service; no sudo needed after deps
```

Or without systemd:

```sh
sudo apt install -y v4l2loopback-dkms ffmpeg v4l-utils pulseaudio-utils
./camera-receiver.sh &           # or --run
./mic-receiver.sh &
```

## Speaker audio (both routing modes)

Speaker audio to the client uses native RDP `/sound` and needs the xrdp
pulse-audio module on the host:

```sh
sudo apt install -y pulseaudio-module-xrdp   # or pipewire-module-xrdp
```

then re-login to the RDP session.

## Verify everything

```sh
ssh host -- '/usr/local/bin/ubuntu-rdp-camera-receiver --check'   # camera side
ssh host -- '~/.local/bin/ubuntu-rdp-mic-receiver --check'        # mic side
```

Each `--check` reports module state, device nodes, service status, the
control responder and firewall state.
