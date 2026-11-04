# Validation and working-status matrix

Version 0.2.0. Last validated: 2026-11-04.

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
| **macOS 27.2 (arm64)** | **Working — live.** 1512×982 JPEG stream; `/info`, `/frame.jpg`, ETag `304`, `/input`, `/ui`, `/quit` verified (all behind the session/header credential). Served a live session to the Linux PC and to the Android phone. | **Working — live.** Desktop viewer rendered the live Linux desktop (1366×768) and the Android screen (1080×2340 · 35 fps). Browser guest works too (`1280×800 · 22 fps` with the session cookie). |
| **Linux (Ubuntu 24.04, X11)** | **Working — live.** Real X session at 1366×768 (not Xvfb): `/info`, JPEG `/frame.jpg` (92,546 bytes), ETag `304`, `/input` 204, `/ui` 200, `/quit` 204. arm64 `.deb` also fully validated. | **Working — live.** `rdp-link` viewer on the Linux PC displayed the macOS host at 1512×982 · 6 fps. |
| **Windows (installer + app)** | **Working under Wine — test pattern.** Real GDI capture needs Windows hardware; the rebuilt 2026-11-04 `office-rdp.exe --host --test-pattern` served `/info` 200, a 25,161-byte 1280×800 JPEG, `/input` 204, `/session` with an HttpOnly `SameSite=Strict` cookie, and a `POST /media-token` token that opened `/frame.jpg` but was rejected on `/info`. `?password=` returned 401. | **Working — live under Wine.** The rebuilt `rdp-link.exe` (PyInstaller, no Python needed) renders the Ant Design launcher; `--viewer --probe` returned a 26,423-byte 1280×800 frame from the Windows host. Browser guest shows `1280×800 · fps`. |
| **Android 16 (SM-S711B)** | **Working — live on a physical device.** MediaProjection host on port 8095: `/info`, JPEG frames (124,444 bytes), accessibility input injection changed the on-screen password (768516 → 227170). The host now encodes PNG on demand and answers with an ETag. | **Working — live.** Full-screen `ViewerActivity` displayed the macOS host; status tag `1512×982`. Portrait, landscape and 100 % custom scaling verified on the phone; the same APK also ran in landscape on the API 36 emulator, where the new session login rendered `1280×800 · 11 fps`. |

**WAN — working through the relay.** The host dials out to the rendezvous; no
port forwarding or static IP is required. Guests resolve the peer ID and are
routed through the tunnel (validated over HTTP and TLS).

**Browser guest (all platforms) — working.** Every host serves the same
`viewer.html`, so any device with a browser can connect without installing
anything: password login, JPEG stream, click/right-click/wheel/touch, keyboard,
clipboard, auto-fit or custom scale, fullscreen, ETag-aware polling,
responsive header.

## UI

### Display fitting and orientation

- **Fit** scales up as well as down and fills the available axis without
  cropping (the default).
- **Fill** covers the window and crops the overflow.
- **1:1** renders host pixels 1:1 with scrollbars.
- **50–150 %** custom scaling, remembered per host.
- The viewer lists the host's monitors from `/displays` and switches with
  `POST /display` when more than one exists.
- Tk adds **Match window**, which sizes the window to the host aspect ratio.
- Android and the browser reflow on rotation; the Android activity keeps the
  WebView session across orientation changes.

Validated: fit upscales, fill crops correctly, 1:1 and 150 % scroll, and the
macOS host reports `1512×982` through `/displays`.

### WAN relay

The rendezvous now carries a reverse tunnel, so hosts behind NAT and dynamic
IPs are reachable by peer ID:

```
$ curl -s 'http://192.168.1.14:18080/resolve?peerId=033976933'
{"peerId":"033976933", ..., "relay":true,
 "url":"http://192.168.1.14:18080/t/033976933/"}

$ curl -s -o f.jpg -w '%{http_code} %{size_download}\n' \
    -H "X-Office-Password: relay1" \
    'http://192.168.1.14:18080/t/033976933/frame.jpg'
200 26603

# TLS relay
$ curl -sk -o /dev/null -w '%{http_code}\n' \
    -H "X-Office-Password: relay2" \
    'https://127.0.0.1:18443/t/033976933/frame.jpg'
200
```

The Android app resolved `033976933` through the relay and streamed
`1280×800 · 11 fps`; the desktop/browser viewer does the same. See
[`WAN.md`](WAN.md).

### Device bridge

Optional host devices are exposed through `/capabilities` and authenticated
endpoints (see [`DEVICES.md`](DEVICES.md)):

```
GET /capabilities        {"audio":true,"mic":true,"camera":true,
                          "serial":"tcp:127.0.0.1:9101","ports":[9099]}
GET /audio (2 s)         200, 18,785 bytes, Opus 48 kHz mono
GET /audio via relay     200, 18,785 bytes
GET /camera.mjpg         200, MJPEG 640×480 (testsrc; real device needs
                         camera permission, otherwise 503)
GET /port/9099/          200 direct and via relay (allow-listed)
WS /serial               echo round-trip (direct; TCP and PTY paths)
WS /mic                  10 KB WebM consumed by the host player
```

Guest UI: the browser/Android **Devices** drawer (audio, microphone, camera,
serial console, USB list, port links) and the Tk viewer's **Audio** and
**Devices** buttons. Verified in the drawer: camera rendered 640×480, serial
echoed `drawer-serial`, speaker state reached `Playing`.

**Camera redirection** (guest → host apps) was verified on the Linux PC:
`--virtual-camera /dev/video10` published the guest's red 320×240 frame; the
host device returned 640×480 with centre pixel `(254,0,0)`. **USB/IP export**
was verified with `/usb/list`, `bind → 204`, `unbind → 204` on the Linux host.

Limitations are explicit: the relay carries audio/camera streams and port
proxies, but WebSocket endpoints (microphone, serial) are LAN-only; camera is a
preview, not a virtual device; USB is storage-level file sharing, because
generic USB passthrough needs kernel drivers.

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
scrollable stage; the physical Android phone rendered the macOS host in
landscape at 1512×982 · 9 fps and switched to 100 % (native 1512×982, cropped
and scrollable) from the same scale selector.

## Security hardening (2026-11-03)

A full code review produced six findings; all six are fixed and verified.

| # | Finding | Fix | Evidence |
| --- | --- | --- | --- |
| 1 | **Relay deadlock**: a guest that left an audio/camera stream kept the host streaming; the reply buffer filled and the reader blocked, freezing the whole tunnel | Non-blocking reader, `pending` entry always released, `cancel` frame sent to the host on disconnect, host cancels the local request | `TestReadLoopNeverBlocksOnAbandonedStream`, `TestProxyReleasesPendingEntry`, `TestStreamStopsWhenCancelled`; live relay: the audio stream was aborted after 3 s, the host `ffmpeg` was gone and the tunnel still served frames and a second stream |
| 2 | **Unauthenticated rendezvous**: anybody could register a peer ID, list the registry or rewrite a peer's policy | Ed25519 request signing (`core/peerauth`); the peer ID must match the signing key; key pinning; `/peers` needs a signature; `/resolve` is rate limited | `TestRegisterRequiresSignature`, `TestPeerCannotClaimAnotherPeerID`, `TestPolicyIsPerPeer`; live: anonymous `/peers` -> 401, `office-rdp --list-peers` -> the registry |
| 3 | **Password in URLs and process lists**: `?password=` in every device URL, `--password` in the argv, password in `Referer` | `POST /session` -> HttpOnly `SameSite=Strict` cookie; `X-Office-Password` header; `POST /media-token` issues a 2-minute read-only token for header-less players (`?token=`); `--password-file` for services and the launcher; generated passwords are stored `0600` and never printed | `TestPasswordInQueryIsRejected`, `TestSessionCookieAndToken`; live: `?password=` -> 401, cookie -> 200, media token -> 200 on `/frame.jpg` and 401 on `/info` |
| 4 | **Permissive WebSocket origin and open `/info`** | `Origin` check on upgrades and on state-changing requests; `/info` and `/displays` require credentials; bounded throttle table; `X-Forwarded-For` only trusted with the per-process relay secret | `TestCrossOriginRequestRejected`, `TestForwardedForNeedsRelaySecret`, `TestFailureTableIsBounded` |
| 5 | **Shell injection in the setup assistant** (the workspace path contains an apostrophe) | `shlex.quote` plus AppleScript escaping | `test_mac_firewall_script_quotes_paths_with_spaces`, `test_mac_firewall_script_escapes_quotes`; the generated script was executed with `osascript` and the path arrived intact |
| 6 | **Dead WebRTC signalling** (`ws`/`wss` bug, `/signal` route, `pion/webrtc` dependency) | Removed the signalling/transport/protocol packages, the `/signal` route and the WebRTC client page; the relay page now explains itself | `go build ./...` clean, `go.mod` no longer lists `pion/webrtc` as a direct dependency |

Additional fixes from the same review:

- **Browser guest file transfer**: the browser viewer had no upload/download
  UI even though the hosts exposed `/files`, `/upload` and `/download`. The
  Devices drawer now has a **Files** section; the desktop viewer passes the
  file name explicitly (`?name=`), so uploads to the Android host keep their
  name instead of becoming `upload.bin`.
- **Tk viewer responsiveness**: every mouse move opened a new TCP connection
  on the UI thread. Input now goes through a background sender that keeps the
  connection alive, coalesces pointer moves (40 ms) and never blocks Tk.
  Window resizing is debounced (60 ms) instead of re-scaling the frame on
  every intermediate size.
- **Android host CPU**: the host encoded a full-screen PNG **and** a JPEG for
  every captured frame. PNG is now encoded on demand, the bitmaps are reused,
  and `/frame.jpg` returns an `ETag` with `304 Not Modified` for a static
  screen.
- **Port proxy**: `/port/<p>/` now uses a reverse proxy, so cookies,
  redirects and root-relative assets keep working under the prefix.
- **Clipboard on Android**: clipboard reads and writes happen on the main
  thread, which is required from Android 10 onwards.
- **Android host serves the guest UI**: the viewer page and the logo are
  copied into the app assets, so an Android host can be opened from any
  browser.

### Live evidence for the new flows (2026-11-03)

| Flow | Where | Result |
| --- | --- | --- |
| Browser guest, session cookie | macOS host → Chromium | overlay without an error message, password → `1280×800 · 22 fps`, the cookie survives a reload |
| Browser guest, file transfer | macOS host → Chromium | Devices → Files: `browser-upload.txt` uploaded, listed and stored with the right name and content |
| Android guest, session auth | macOS host → Android emulator | overlay without an error message, password → `1280×800 · 11 fps` |
| Android Setup assistant | Android emulator | "Remote control (accessibility) · Action needed", "Notifications · Ready", "Battery optimisation · Action needed", "Screen capture · Ready" |
| Desktop viewer, background input | macOS host → Tk viewer | live frame at `1280×800 · 11 fps`; input events are queued off the UI thread |
| Desktop viewer, probe | macOS host → `office_viewer.py --probe` | `probe: OK 26003 bytes 1280x800` with the right password, `401` with a wrong one |
| `--password-file` | macOS host | the agent starts with the password from a `0600` file; the LaunchAgent and the systemd unit use the same flag |
| Generated password | macOS host | no password flag: a 24-byte random password is written to `~/.config/office-rdp/host-password` (`0600`) and is not printed or stored in `gui.json` |
| Media token scope | macOS host | `POST /media-token` with the session cookie returns a 2-minute token; it opens `/frame.jpg` but is rejected on `/info` |
| Android relay tunnel | Android host → relay | the Go relay client is started by `HostService`; requires a live device/relay re-run to confirm the tunnel appears in `/resolve` |
| Relay, signed tunnel | macOS host → relay | tunnel connected, frame `200` through `/t/<peer>/`, audio stream aborted by the guest, host `ffmpeg` stopped, the tunnel stayed healthy |
| Relay, admin token | relay | `--list-peers` without the token shows no address, with `--admin-token` the LAN address appears |

## Second review pass (2026-11-03)

The follow-up review found six more issues; all are fixed and verified.

| # | Finding | Fix | Evidence |
| --- | --- | --- | --- |
| 1 | **`sameOrigin` bypass on the device bridge**: the check lived in a local wrapper, so `/capabilities`, `/audio`, `/usb/*`, `/port/…` and `/session` skipped it | The check moved into `(*Agent).auth` and `(*Agent).handleSession`, which every endpoint uses | `TestDeviceBridgeRejectsCrossOrigin`, `TestSessionRejectsCrossOrigin`; live: `Origin: http://evil.example` → `403`, same-origin → `200` |
| 2 | **Session cookie collision on the relay**: the host set `Path=/`, so two tunnelled hosts on one relay shared and overwrote one `rdplink_sid` cookie | The relay narrows the path to `Path=/t/<peerId>/` without touching the other attributes | `TestScopeSetCookie`; live: the relay returns `Path=/t/033976933/` and the browser guest still streams `1280×800 · 6 fps` |
| 3 | **Clipboard poll every 500 ms**: `StartClipboardSync` spawned a helper process twice a second on every platform (`powershell.exe` on Windows) even with no guest | The clipboard is read on demand in `GET /clipboard` and cached for 750 ms; Windows with cgo uses the Win32 clipboard through robotgo | `TestClipboardReadsAreCached`; live: a browser write reached the macOS clipboard and `GET /clipboard` returned it |
| 4 | **Cancel/first-frame race** in the relay client: a cancel that arrived before the stream goroutine registered was lost, and the host kept the device open | The cancel function is registered before the goroutine starts; the reconnect loop and the background workers stop with the agent | `TestRunStopsWhenDone`; the streaming cancel test still passes |
| 5 | **Wrong platform defaults**: `handleMicWS` never fell back to `ffmpeg`, and the camera used `avfoundation` on Linux/Windows | The player, the audio output (`audiotoolbox`/`pulse`/`waveout`) and the camera input (`v4l2`/`dshow`/`avfoundation`) follow the platform | `go build` for linux/darwin/windows; the device tests still pass |
| 6 | **Hand-written Linux unit lost the password**, unbounded rendezvous tables, `Content-Disposition` injection, no clipboard write in the browser | The setup assistant calls `office-rdp --install-service`; the key/pair/peer tables are bounded; `mime.FormatMediaType` builds the header; the Devices drawer gained Send-to-host and paste forwarding | `TestDownloadIsAnAttachment`, `TestPairing`; live: the new Clipboard section sent text to the host |

## Evidence

### Tests and static analysis

```
$ bash scripts/test-all.sh
go vet ./...            clean
go test ./...           all packages pass
go build ./...          ok
pyflakes                clean
pytest                  80 passed
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
    -H "X-Office-Password: lintest" 'http://192.168.1.13:8099/frame.jpg'
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
    -H "X-Office-Password: 768516" 'http://127.0.0.1:18095/frame.jpg'
200 124444

# remote control enabled → green tag "Remote control enabled"
# inject a tap on the "New password" button
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'Content-Type: application/json' -d '{"type":"click","x":898,"y":770}' \
    -H "X-Office-Password: 768516" 'http://127.0.0.1:18095/input'
204
# password after the remote tap (uiautomator dump): 227170
```

Android guest → macOS host (physical SM-S711B, Android 16):

```
portrait:  1080×2340, logo header, status tag 1512×982
landscape: 2340×1080, status 1512×982 · 9 fps, scale selector "Auto fit"
custom:    selected "100%" → native 1512×982 frame, stage scrolls
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
    -H "X-Office-Password: wintest" 'http://127.0.0.1:18099/frame.jpg'
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

### Headless CLI (2026-11-04)

`rdp-link` was driven from a terminal with no GUI and no `DISPLAY` against a
live macOS host (`--host --test-pattern --port 18200`):

```
$ rdp-link --help                 → full usage, tkinter not imported
$ rdp-link --version              → RDP Link 0.2.0
$ rdp-link --id                   → 033 976 933
$ rdp-link --status --json        → peerId, alias, identity path, agent version
$ rdp-link --status 127.0.0.1:18200 --password testpw
  RDP Link ID : 033 976 933
  Alias       : taruns-macbook-pro
  LAN         : 192.168.1.14:18200
  WAN         : 110.226.114.183:18200
$ rdp-link --connect 127.0.0.1:18200 --password testpw --probe --save-frame /tmp/cli-shot.jpg
  frame: 25060 bytes 1280x800 image/jpeg
  saved: /tmp/cli-shot.jpg
$ rdp-link --connect 127.0.0.1:18200 --password testpw --clipboard-set "rdp-link cli" --clipboard-get
  rdp-link cli
$ rdp-link --share --port 18201 --test-pattern --password sharepw
  host agent starting — peer id: 033 976 933
  LAN: http://192.168.1.14:18201/
$ rdp-link --doctor               → Capture: OK, port free
$ rdp-link --displays             → 0
$ rdp-link --list                 → legacy FreeRDP profiles
```

Unit tests (`app/tests/test_cli.py`, 16 tests) cover probe/save, auth failure,
clipboard ordering, click/type payloads, remote status, profile dispatch,
`--json`, the “remote option without --connect” error, portable-tarball agent
lookup, `--install-cli`/`--uninstall-cli`, and a subprocess check that
importing the CLI does not load `tkinter`.

CLI installation on macOS and Windows:

```
# macOS: wrapper on PATH (tested with RDPLINK_CLI_BIN and the frozen .app)
$ rdp-link --install-cli
installed: /tmp/rdplink-cli/rdp-link
$ /tmp/rdplink-cli/rdp-link --version
RDP Link 0.2.0

# Windows: the NSIS installer manages the system PATH
$ wine RDPLink-setup.exe /S
$ wine reg query "HKLM\...\Environment" /v Path
  ...;C:\Program Files\RDP Link
$ wine "C:\Program Files\RDP Link\uninstall.exe" /S
  ... (the install directory is removed again)
```

On macOS the `.pkg` postinstall writes `/usr/local/bin/rdp-link`,
`office-rdp` and `office-rdp-rendezvous`; the `.dmg` exposes
`RDP Link.app/Contents/MacOS/rdp-link` and `--install-cli` links it.

## Artifacts

| Artifact | Size | Notes |
| --- | --- | --- |
| `dist/macos/RDPLink-macos.dmg` | 46 MB | `.app` with arm64 + amd64 Go tools, Ant Design UI, bundled LICENSE + third-party notices, ad-hoc signed (`codesign --verify --strict` passes) |
| `dist/macos/RDPLink-installer.pkg` | 41 MB | double-click installer; postinstall adds the agent to the Application Firewall and installs the login service |
| `dist/windows/RDPLink-setup.exe` | 22 MB | NSIS; ships `rdp-link.exe`, CGO `office-rdp.exe`, rendezvous, shortcuts, Add/Remove Programs, firewall rule |
| `dist/windows/rdp-link.exe` | 17 MB | PyInstaller onefile launcher + bundled viewer (no Python on target) |
| `dist/linux/rdplink-linux-arm64.deb` | 23 MB | `office-rdp`, `office-rdp-rendezvous`, `rdp-link`, docs; postinst loads the modules and the uinput rule |
| `dist/linux/rdplink-linux-amd64.deb` | 24 MB | same payload for x86-64 |
| `dist/linux/rdplink-linux.tar.gz` | 74 MB | both architectures plus the launcher |
| `dist/android/RDPLink-android-release.apk` | 49 MB | signed with the Office RDP release key (`CN=Office RDP, O=Eminent AI Labs`, SHA-256 `de7382e4…9283`), `versionCode 2`, Ant Design UI, guest viewer bundled |
| `dist/android/RDPLink-android-release.aab` | 19 MB | Play upload bundle, same release key |

The Android package grew because the Go core (identity, signed rendezvous
requests and the relay tunnel) is linked into the app as a native library for
all four ABIs.

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
- **H.264 recording path**: the HTTP JPEG path is what all viewers use; the
  Pion/OpenH264 track is built and started but not exercised end-to-end.
