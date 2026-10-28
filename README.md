# 🖥 Ubuntu RDP Manager

**A lightweight, open-source RDP launcher for macOS and Linux with full device
redirection** — audio, microphone, camera, clipboard and file transfer — so a
Teams/Zoom call inside a remote Ubuntu session uses your local hardware.

Powered by FreeRDP. The GUI is a thin launcher with **zero runtime
dependencies beyond Python/Tk** — connecting through the app feels exactly
like launching `sdl-freerdp` yourself, minus the typing.

```
┌─────────────────────┐        RDP (xrdp)         ┌──────────────────────┐
│  Mac / Linux client │◄─────────────────────────►│  Ubuntu host          │
│  ─────────────────  │   screen, keys, clipboard  │  ──────────────────  │
│  your speakers  ◄───│──────────── /sound ────────│── remote audio        │
│  your mic      ────►│──── ffmpeg UDP 7273 ──────►│── "MacMic" source     │
│  your camera   ────►│──── ffmpeg UDP 7272 ──────►│── "MacCamera" webcam  │
│  your files    ◄───►│──── /drive + /clipboard ──►│── copy/paste, files   │
└─────────────────────┘                            └──────────────────────┘
         receivers installed by the app over SSH in one click
```

## Highlights (v4)

- **Per-device routing** — speaker / mic / camera each route to *this
  computer* or stay on the *remote PC*, per profile.
- **Direct connect** — saved passwords are piped to FreeRDP via stdin;
  double-click connects with no prompts. Passwords never appear on the
  command line and are stored in the macOS Keychain (or a secret service /
  0600 file on Linux).
- **Receiver handshake** — the app pings the host's receiver service and
  tells you *"receiver detected ✓"* or *"no response — set up receivers?"*
  instead of silently streaming into the void.
- **One-click host setup** — *Set up receivers on host…* pushes and installs
  both receiver **systemd services** over SSH in a terminal window
  (`sudo` password may be asked once). Survives reboots.
- **Native mic option** — for xrdp 0.10+ hosts, use raw RDP `/microphone`
  redirection instead of the bridge.
- **Full session log** — every FreeRDP/bridge warning lands in a session log,
  one menu click away.
- **CLI mode** — `ubuntu-rdp-manager --connect "Name"` connects without
  opening the GUI at all.
- **Cross-platform** — client runs on macOS and Linux; hosts are any Linux
  box running xrdp.

## Quick start

### One command (macOS and Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/YOURORG/UbuntuRDPManagerApp/main/install.sh \
     -o /tmp/urdm-install.sh && bash /tmp/urdm-install.sh guest
```

(This installs the **guest/client** — both the GUI and the `urdm` CLI, from
the same binary. Run `bash /tmp/urdm-install.sh host` on the Ubuntu machine
to install the receiver services there. Other roles: `both`, `--uninstall`.)

### Manual (macOS guest)
```sh
brew bundle --file=Brewfile          # freerdp + ffmpeg
./build-app.sh                       # build + install to ~/Applications
```

### Manual (Linux guest)
```sh
sudo apt install freerdp3-x11 ffmpeg python3-tk   # or freerdp2-x11
./build-linux.sh                     # produces dist/ubuntu-rdp-manager
```

### Or straight from source (both platforms)
```sh
python3 ubuntu_rdp_manager.py
```

### Host (Ubuntu — fully automatic)

1. Click **＋ Add** → enter host, username, password.
2. Route your devices (speaker / mic / camera) per connection.
3. Click **Save & Connect**.
4. First time only: right-click the profile → **Set up receivers on host…**
   The app opens a terminal, copies the receiver scripts over SSH and
   installs them as services. Done — they persist and auto-start.

No manual host setup. No package juggling. No remembering which script to
run — the app tells you if a receiver isn't responding and offers to fix it.

### macOS permissions — asked once, never nagged

The app doesn't probe devices at launch. The first time a connection
actually routes camera/mic to the Mac, macOS shows its standard prompt;
once granted, the app caches that and never touches the devices again at
startup. If a bridge later fails (e.g. access was revoked), the cache is
cleared automatically and **Check Permissions…** re-verifies on demand.

## Documentation

| Doc | Contents |
|---|---|
| [docs/INSTALL.md](docs/INSTALL.md) | Detailed install/build for macOS, Linux and source; what the receiver installer does |
| [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) | Symptom → fix matrix, diagnostics, logs |
| [docs/SAME-SCREEN.md](docs/SAME-SCREEN.md) | Port 3390 (same screen / console) vs 3389 (new virtual session) — how to choose, configure and secure |
| [docs/SECURITY.md](docs/SECURITY.md) | Where secrets live, how streams are protected, hardening options |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Architecture, module map, tests, release checklist |

## Profiles

Profiles live in `~/Library/Application Support/Ubuntu RDP Manager/profiles.json`
(macOS) or `~/.config/ubuntu-rdp-manager/profiles.json` (Linux), and are
backward-compatible with v1–v3 files — old boolean device flags migrate
automatically.

## License

MIT — see [LICENSE](LICENSE). Contributions welcome!
