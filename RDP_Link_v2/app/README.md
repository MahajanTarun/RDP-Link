# 🔗 RDP Link

**Lightweight remote access.**
*Open source · Fast · Minimal*

A thin, open-source RDP launcher for macOS and Linux with full device
redirection — audio, microphone, camera, clipboard and file transfer — so a
Teams/Zoom call inside a remote Ubuntu session uses your local hardware.

Powered by FreeRDP. The GUI is a thin launcher with **zero runtime
dependencies beyond Python/Tk** — connecting through RDP Link feels exactly
like launching `sdl-freerdp` yourself, minus the typing.

```
┌─────────────────────┐        RDP (xrdp)         ┌──────────────────────┐
│  Mac / Linux client │◄─────────────────────────►│  Ubuntu host         │
│  ─────────────────  │   screen, keys, clipboard │  ──────────────────  │
│  your speakers  ◄───│──────────── /sound ───────│── remote audio       │
│  your mic      ────►│──── ffmpeg UDP 7273 ─────►│── "MacMic" source    │
│  your camera   ────►│──── ffmpeg UDP 7272 ─────►│── "MacCamera" webcam │
│  your files    ◄───►│──── /drive + /clipboard ─►│── copy/paste, files  │
└─────────────────────┘                           └──────────────────────┘
         receivers installed by the app over SSH in one click
```

> Previously "Ubuntu RDP Manager" — same engine, new name. Existing
> profiles and saved passwords migrate automatically on first run.

## Highlights (v4.2)

- **Per-device routing** — speaker / mic / camera each route to *this
  computer* or stay on the *remote PC*, per profile.
- **Direct connect** — saved passwords are piped to FreeRDP via stdin;
  double-click connects with no prompts. Passwords never appear on the
  command line.
- **Receiver handshake** — the app pings the host's receiver service and
  tells you *"receiver detected ✓"* or *"no response — set up receivers?"*
  instead of silently streaming into the void.
- **One-click host setup** — *Set up receivers on host…* pushes and installs
  both receiver **systemd services** over SSH in one terminal window
  (`sudo` password may be asked once). Survives reboots.
- **Native mic option** — for xrdp 0.10+ hosts, use raw RDP `/microphone`
  redirection instead of the bridge.
- **Keyboard auto-detect** — your currently selected Mac/Linux keyboard
  layout is sent to the session automatically (`/kbd:layout`), per profile
  overridable.
- **Scroll auto-detect** — the remote session follows the scroll direction
  of the mouse/trackpad in use (natural trackpad scrolling or traditional
  wheel scrolling), applied through FreeRDP's SDL config per profile.
- **Permissions asked once** — no device probing at launch; macOS prompts
  only on first actual use, then it's cached forever until revoked.
- **Camera guaranteed off with the app** — bridges run under a parent-death
  watchdog: crash or force-quit kills the camera/mic feed within ~1 s, no
  orphans possible; per-profile 🎥/🎤 streaming indicators plus a
  "Stop camera & mic now" action keep you in control even mid-session.
- **Full session log** — every FreeRDP/bridge warning lands in a session log,
  one menu click away.
- **CLI mode** — `rdp-link --connect HOST:PORT` (or a peer ID) connects and
  checks a host without opening the GUI; `--share` runs the host headless.
- **Cross-platform** — client runs on macOS and Linux; hosts are any Linux
  box running xrdp.

## Quick start

### One command (macOS and Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/YOURORG/RDP-Link/main/install.sh \
     -o /tmp/rdplink-install.sh && bash /tmp/rdplink-install.sh guest
```

(This installs the **guest/client** — both the GUI and the `rdplink` CLI,
from the same binary. Run `bash /tmp/rdplink-install.sh host` on the Ubuntu
machine to install the receiver services there. Other roles: `both`,
`--uninstall`.)

### Manual (macOS guest)
```sh
brew bundle --file=Brewfile          # freerdp + ffmpeg
./build-app.sh                       # build + install to ~/Applications
```

### Manual (Linux guest)
```sh
sudo apt install freerdp3-x11 ffmpeg python3-tk   # or freerdp2-x11
./build-linux.sh                     # produces dist/rdp-link
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

Profiles live in `~/Library/Application Support/RDP Link/profiles.json`
(macOS) or `~/.config/rdp-link/profiles.json` (Linux). Installs from before
the rename ("Ubuntu RDP Manager") are migrated automatically on first run,
so old profiles and passwords carry over untouched.

## CLI

The launcher is also a full headless client — no GUI, no tkinter, no display:

```sh
rdp-link --id                        # 9-digit RDP Link ID
rdp-link --status [--json]           # local status, or --status HOST
rdp-link --share --port 8088         # share this screen, headless
rdp-link --connect HOST:8088         # connect + verify
rdp-link --connect HOST --probe      # fetch one frame
rdp-link --connect HOST --save-frame shot.jpg
rdp-link --connect HOST --clipboard-get
rdp-link --connect HOST --click 640,400 --type "hello"
rdp-link --connect 123456789 --rendezvous https://relay.example.com
rdp-link --doctor | --displays | --peers
rdp-link --list                      # legacy FreeRDP profiles
rdp-link --connect-profile "MolCon Wired"
rdp-link --version
```

`--connect` accepts a peer ID, `host:port`, an `http(s)` URL, or a legacy
profile name. Add `--password-file` (or `RDPLINK_PASSWORD`) instead of
`--password` to keep the credential out of the process list.

macOS guests also get a `rdplink` alias for the same thing after
`./build-app.sh`.

## License

MIT — see [LICENSE](LICENSE). Contributions welcome — see
[CONTRIBUTING.md](CONTRIBUTING.md).
