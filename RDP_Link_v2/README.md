# RDP Link

Cross-platform remote desktop with a Go core: macOS, Linux, Windows, and
Android can each share their screen and control another device. The UI follows
the AnyDesk/RustDesk model — a 9-digit RDP Link ID for WAN, a LAN address for the
local network, and a password for every connection.

This is the v2 tree. The shipping v4.5 Python app in the parent folder is
untouched.

## What works

| | Host | Guest |
| --- | --- | --- |
| macOS | live | desktop viewer (`rdp-link`) + browser |
| Linux | live (real X session) | desktop viewer (`rdp-link`) + browser |
| Windows | live via `--test-pattern` under Wine; real GDI capture needs Windows | `rdp-link.exe` (validated under Wine) + browser |
| Android | live on a physical phone (MediaProjection) | full-screen `ViewerActivity` + browser |

Every host also serves the browser viewer at `http://<host>:<port>/`, so any
device with a browser can be a guest without installing anything.

All surfaces use an Ant Design 5 light theme (see
[`docs/UI-DESIGN.md`](docs/UI-DESIGN.md)).

- [`docs/VALIDATION.md`](docs/VALIDATION.md) — OS × host/guest matrix and live evidence
- [`docs/TESTING.md`](docs/TESTING.md) — devices, tools, methodology, how to reproduce
- [`docs/UI-DESIGN.md`](docs/UI-DESIGN.md) — design tokens and per-surface mapping
- [`docs/SETUP.md`](docs/SETUP.md) — one-click setup, permissions and installer automation
- [`docs/SECURITY.md`](docs/SECURITY.md) — authentication model and hardening

## Downloads (v0.2.0)

Download the build for your operating system and install it. These files live in
the [`releases/`](releases/) folder of this repository, so the links work
directly from the GitHub page.

| Operating system | Package | How to install |
| --- | --- | --- |
| **macOS** (arm64 + x86-64) | [`RDPLink-macos.dmg`](releases/RDPLink-macos.dmg) (50 MB) | Open the DMG and drag **RDP Link** into Applications. |
| **macOS** (installer) | [`RDPLink-installer.pkg`](releases/RDPLink-installer.pkg) (46 MB) | Double-click; postinstall adds the firewall rule and login service. |
| **Windows** (x86-64) | [`RDPLink-setup.exe`](releases/RDPLink-setup.exe) (22 MB) | Run the installer; it ships the launcher, the Go agent and the rendezvous server. |
| **Linux** (arm64) | [`rdplink-linux-arm64.deb`](releases/rdplink-linux-arm64.deb) (23 MB) | `sudo dpkg -i rdplink-linux-arm64.deb` |
| **Linux** (x86-64) | [`rdplink-linux-amd64.deb`](releases/rdplink-linux-amd64.deb) (24 MB) | `sudo dpkg -i rdplink-linux-amd64.deb` |
| **Linux** (both arches) | [`rdplink-linux.tar.gz`](releases/rdplink-linux.tar.gz) (74 MB) | Extract and run `rdp-link` / `office-rdp` directly. |
| **Android** (phone/tablet) | [`RDPLink-android-release.apk`](releases/RDPLink-android-release.apk) (49 MB) | Sideload the APK (enable "Install unknown apps"). |
| **Android** (Play upload) | [`RDPLink-android-release.aab`](releases/RDPLink-android-release.aab) (19 MB) | Upload bundle to Google Play. |

The macOS build is ad-hoc signed (`codesign --verify --strict` passes); the
Android build is signed with the Office RDP release key. Windows and Linux
binaries are not code-signed (Authenticode / distro signing are external
blockers).

## Layout

- `core/` — identity, peers, signed rendezvous auth, mDNS, capture,
  input, clipboard, policy, host agent.
- `rendezvous/` — self-hostable registry and reverse-tunnel relay server.
- `cmd/office-rdp` — host agent and CLI.
- `cmd/rendezvous` — server entry.
- `app/` — desktop launcher (Python/Tk) and `office_viewer.py` guest.
- `android/` — native Kotlin host service + full-screen viewer.
- `packaging/` — macOS, Linux, Windows, Android build scripts.
- `docs/` — plan, ADRs, security, privacy, testing, UI design, releasing guide.

## Quick start

```sh
# Share this screen
office-rdp --host --port 8088 --password <secret>

# Control panel in the browser (works on every platform, no install)
office-rdp --ui

# Stream a synthetic test pattern instead of the screen (CI, headless, Wine)
office-rdp --host --test-pattern --port 8088 --password <secret>

# Check capture and input
office-rdp --doctor
office-rdp --displays

# Rendezvous server for WAN connections
office-rdp-rendezvous --listen :8080
```

A guest connects with any of:

- the desktop app: launch `rdp-link` (macOS/Linux) or `rdp-link.exe` (Windows)
  and use **Connect to a device**
- the viewer directly:
  `cd app && python3 office_viewer.py http://<host>:8088 --password <secret>`
- a browser at `http://<host>:8088/` (enter the password in the page)
- the Android app: enter `host:port` and tap Connect

The host prints its LAN address, WAN address and peer ID on startup. When no
`--password` is given, a random access password is generated once and stored in
`~/.config/office-rdp/host-password` (`0600`); the agent prints the path, never
the password. `--password` pins a password; `--install-service` points the user
service at the same private password file so unattended access survives
reboots.

## Build and test

```sh
go build ./...
bash scripts/test-all.sh        # go vet, go test, pyflakes, 72 pytest cases
bash scripts/security-scan.sh   # gosec, govulncheck, shellcheck
```

Artifacts:

```sh
bash packaging/build-macos.sh
bash packaging/build-linux.sh && bash packaging/build-linux-deb.sh
bash packaging/windows/build-installer.sh
bash packaging/build-android-core.sh   # optional gomobile AAR
cd android && gradle assembleRelease bundleRelease
```

See [`docs/RELEASING.md`](docs/RELEASING.md) for signing keys and
notarization.

## Security notes

- Every HTTP request is authenticated; passwords are compared in constant
  time and failures are throttled per IP (20/minute → `429`).
- The access password is never a URL parameter: browsers exchange it for an
  `HttpOnly`, `SameSite=Strict` session cookie, API clients send the
  `X-Office-Password` header, and the launcher keeps it in a `0600` file
  instead of the process list.
- Cross-site requests and WebSocket upgrades are rejected through an
  `Origin` check; `X-Forwarded-For` is only believed when the request comes
  from the in-process relay.
- The rendezvous only accepts requests signed with the device Ed25519 key, so
  a peer ID cannot be registered, hijacked or renamed by anyone else.
- Downloads are restricted to the inbox directory with a path-traversal guard;
  only regular files are served.
- Frames use a content hash ETag, so a static screen costs a `304`, not a
  full image, per poll.
- Android: `allowBackup=false`, network security config, foreground service
  with a stop action, and an accessibility service the user must enable.
- The agent binds `0.0.0.0` by design (guests must reach it); restrict it with
  a firewall or a private rendezvous server if needed.

See [`docs/SECURITY.md`](docs/SECURITY.md) for the full model.

## Record a session

```sh
office-rdp --host --record <name>.h264
```

Recordings land in `$HOME/.config/office-rdp/inbox/recordings/` with a
sanitized filename.
