# Validation and working-status matrix

Version 0.2.0. Last validated: 2026-11-03.

Product name: **RDP Link** (binary identifiers such as `office-rdp` stay for
compatibility). This page records what was actually exercised, not just what
compiles. The method, tools and full evidence log live in
[`TESTING.md`](TESTING.md); the visual language is documented in
[`UI-DESIGN.md`](UI-DESIGN.md).

"Live" means a real host and a real guest exchanged frames and input during
this validation pass, on physical hardware.

## OS × Host / Guest matrix

| Platform | Host (share this screen) | Guest (control another device) |
| --- | --- | --- |
| **macOS 27.2 (arm64)** | **Working — live.** 1512×982 JPEG stream; `/info`, `/frame.jpg`, ETag `304`, `/input`, `/ui`, `/quit` verified. Served a live session to the Linux PC and to the Android phone. | **Working — live.** Desktop viewer rendered the live Linux desktop (1366×768) and the Android screen (1080×2340 · 35 fps). Browser guest works too. |
| **Linux (Ubuntu 24.04, X11)** | **Working — live.** Real X session at 1366×768 (not Xvfb): `/info`, JPEG `/frame.jpg` (92,546 bytes), ETag `304`, `/input` 204, `/ui` 200, `/quit` 204. arm64 `.deb` also fully validated. | **Working — live.** `rdp-link` viewer on the Linux PC displayed the macOS host at 1512×982 · 6 fps. |
| **Windows (installer + app)** | **Working under Wine — test pattern.** Real GDI capture needs Windows hardware; under Wine `office-rdp.exe --host --test-pattern` served `/info`, a 1280×800 JPEG, changing ETags, `/input` 204 and `/ui` 200. | **Working — live under Wine.** `rdp-link.exe` (PyInstaller, no Python needed) renders the Ant Design launcher; `--viewer --probe` returned a 1512×982 frame. Browser guest shows `1280×800 · fps`. |
| **Android 16 (SM-S711B)** | **Working — live on a physical device.** MediaProjection host on port 8095: `/info`, JPEG frames (124,444 bytes), accessibility input injection changed the on-screen password (768516 → 227170). | **Working — live.** Full-screen `ViewerActivity` displayed the macOS host; status tag `1512×982`. Portrait verified on the phone, portrait + landscape on the API 36 emulator with the same APK. |

**Browser guest (all platforms) — working.** Every host serves the same
`viewer.html`, so any device with a browser can connect without installing
anything: password login, JPEG stream, click/right-click/wheel/touch, keyboard,
clipboard, auto-fit or custom scale, fullscreen, ETag-aware polling,
responsive header.

## UI

All four surfaces use the Ant Design 5 light theme (Tailwind-style spacing,
white cards on `#F5F5F5`, `#1677FF` primary, 6/8 px radii, Ant Design tags) and
the RDP Link monitor mark (the supplied logo, cropped for small sizes):

- desktop launcher (macOS, Linux, Windows),
- desktop guest viewer,
- browser viewer and control panel,
- Android host screen and launcher icon.

See [`UI-DESIGN.md`](UI-DESIGN.md) for tokens and per-surface mapping.

### Display scaling and orientation

| Feature | Desktop (Tk) | Browser / Android guest |
| --- | --- | --- |
| Auto fit (default) | Scales the host resolution into the window, preserving aspect ratio | `max-width/height: 100%`, recalculated on resize/orientation change |
| Custom scale | 50 / 75 / 100 / 125 / 150 %, with scrollbars above fit | Same six options in the header select; the stage scrolls at >fit |
| Native `1:1` | 100 % option | 100 % option |
| Coordinate mapping | Uses `canvasx/canvasy` minus the centring offset, so clicks stay accurate while scrolled | Uses `getBoundingClientRect`, which follows scroll and zoom |
| Orientation | Window resize triggers a re-render | Android activities keep state across rotation (`configChanges`) and the CSS reflows |

Verified: 100 % on a 1280×800 stream produced a 1280×800 frame with a
scrollable stage; Android landscape rendered the macOS host at 1512×982 in the
same APK.

## Evidence

### Tests and static analysis

```
$ bash scripts/test-all.sh
go vet ./...            clean
go test ./...           all packages pass
go build ./...          ok
pyflakes                clean
pytest                  72 passed
ALL TESTS PASSED

$ bash scripts/security-scan.sh
gosec (medium+)         pass (documented #nosec on 16 call sites)
govulncheck             no reachable vulnerabilities
shellcheck              pass
SECURITY SCANS PASSED
```

### Cross-device sessions (physical hardware)

Linux host → macOS viewer:

```
$ ssh tarun@MCITLPTP810.local
$ DISPLAY=:0 office-rdp --host --port 8099 --password lintest
LAN:  http://192.168.1.13:8099/
$ curl -s http://192.168.1.13:8099/info
{"alias":"mcitlptp810","lanIp":"192.168.1.13","peerId":"388 300 819", ...}
$ curl -s -o f.jpg -w '%{http_code} %{size_download}\n' \
    'http://192.168.1.13:8099/frame.jpg?password=lintest'
200 92546
```

macOS viewer against that host: live Ubuntu desktop at `1366×768`.

macOS host → Linux viewer:

```
$ "RDP Link.app/Contents/MacOS/office-rdp" --host --port 18420 --password mactest
$ rdp-link --viewer http://192.168.1.14:18420 --password mactest
status: 1512×982 · 6 fps
```

macOS guest probe (bundled viewer, headless):

```
$ "RDP Link.app/Contents/MacOS/RDP Link" --viewer --probe \
      http://127.0.0.1:18420 --password mactest
probe: OK 160989 bytes 1512x982
```

### Android (physical SM-S711B, Android 16)

```
$ adb install -r RDPLink-android-release.apk
Success
$ adb shell am start -n com.eminentailabs.officerdp/.MainActivity
# Started sharing, chose "Share entire screen"
$ adb forward tcp:18095 tcp:8095
$ curl -s http://127.0.0.1:18095/info
{"peerId":"950 270 362","alias":"Android","status":"online","port":8095,
 "version":"0.2.0","platform":"android"}
$ curl -s -o a.jpg -w '%{http_code} %{size_download}\n' \
    'http://127.0.0.1:18095/frame.jpg?password=768516'
200 124444

# remote control enabled → green tag "Remote control enabled"
# inject a tap on the "New password" button
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'Content-Type: application/json' -d '{"type":"click","x":898,"y":770}' \
    'http://127.0.0.1:18095/input?password=768516'
204
# password after the remote tap (uiautomator dump): 227170
```

Android guest → macOS host:

```
ViewerActivity, full screen: status 1512×982
header wraps to two rows in portrait; status tag never truncates
```

macOS viewer → Android host:

```
status: 1080×2340 · 35 fps
```

### Windows (Wine)

```
$ wine rdp-link.exe --viewer --probe http://127.0.0.1:18420 --password mactest
probe: OK 117187 bytes 1512x982

$ wine office-rdp.exe --host --test-pattern --port 18099 --password wintest
$ curl -s -o f.jpg -w '%{http_code} %{size_download}\n' \
    'http://127.0.0.1:18099/frame.jpg?password=wintest'
200 25846
# ETag changes between frames; /input → 204; /ui → 200
```

Silent installer check:

```
$ wine RDPLink-setup.exe /S
# C:\Program Files\RDP Link\ contains rdp-link.exe, office-rdp.exe,
# office-rdp-rendezvous.exe, README.txt, uninstall.exe
# Start-menu shortcut "RDP Link" launches rdp-link.exe
```

### Browser guest

In-app Chromium against a host:

```
status: 1280×800 · 6 fps (test pattern)
overlay hidden after password login
buttons: Clipboard, Fit, Fullscreen, Disconnect
```

Control panel (`/ui`): RDP Link ID, LAN/WAN addresses, password reveal/copy,
sharing tag, connect form — all rendered with the Ant Design palette.

### Linux packages

```
$ dpkg -i rdplink-linux-arm64.deb
$ office-rdp --version      → office-rdp 0.2.0
$ rdp-link --version        → RDP Link 0.2.0
$ DISPLAY=:99 office-rdp --doctor
Displays: 1
Capture: OK
$ curl .../frame.jpg        → 200 16593 image/jpeg
$ conditional ETag request  → 304
$ rdp-link --viewer --probe → probe: OK 16593 bytes
```

The amd64 `.deb` installs and reports `Capture: OK` under the x86 Colima
profile; its live stream could not be re-validated because the emulated
container's Xvfb drops the X connection inside the third-party `xgb` library.
The native arm64 package is fully validated and the code path is identical.

## Artifacts

| Artifact | Size | Notes |
| --- | --- | --- |
| `dist/macos/RDPLink-macos.dmg` | 55 MB | `.app` with arm64 + amd64 Go tools, Ant Design UI, ad-hoc signed (`codesign --verify --strict` passes) |
| `dist/windows/RDPLink-setup.exe` | 23 MB | NSIS; ships `rdp-link.exe`, CGO `office-rdp.exe`, rendezvous, shortcuts, Add/Remove Programs, firewall rule |
| `dist/windows/rdp-link.exe` | 17 MB | PyInstaller onefile launcher + bundled viewer (no Python on target) |
| `dist/linux/rdplink-linux-arm64.deb` | 24 MB | `office-rdp`, `office-rdp-rendezvous`, `rdp-link`, docs |
| `dist/linux/rdplink-linux-amd64.deb` | 25 MB | same payload for x86-64 |
| `dist/linux/rdplink-linux.tar.gz` | 80 MB | both architectures plus the launcher |
| `dist/android/RDPLink-android-release.apk` | 21 MB | release-signed, `versionCode 2`, Ant Design UI |
| `dist/android/RDPLink-android-release.aab` | 8.4 MB | Play upload bundle |

## Platform notes and not verified here

- **Windows GDI capture** requires a real Windows session; Wine cannot capture
  the desktop, so the host runs with `--test-pattern` there. The installer and
  launcher are fully exercised.
- **Apple Developer ID signing/notarization** requires Apple credentials. The
  ad-hoc path is verified; `packaging/mac-notarize.sh` runs when `APPLE_ID`,
  `APPLE_APP_SPECIFIC_PASSWORD` and `APPLE_TEAM_ID` are set.
- **macOS self-connect**: a viewer on the same Mac connecting to that Mac's own
  LAN IP can be reset by the OS before it reaches the Go listener. Use
  `127.0.0.1` for same-machine tests; external clients (Linux PC, Android)
  work normally.
- **Linux amd64 live capture** under emulation: see above.
- **Android** validated on one physical device (Samsung SM-S711B, Android 16);
  vendor ROMs may differ.
- **WebRTC media path**: the HTTP JPEG path is what all viewers use; the
  Pion/OpenH264 track is built and started but not exercised end-to-end.
