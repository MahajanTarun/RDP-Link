# Testing, validation and evaluation

This document explains **how** RDP Link is tested, which devices and tools
were used, and what evidence each claim in [`VALIDATION.md`](VALIDATION.md)
rests on. It is written so the whole pass can be repeated by someone else.

Last full pass: 2026-11-07 (version 0.2.0).

---

## 1. Test bed

| Device | Role in this pass | Details |
| --- | --- | --- |
| **Mac (this PC)** | Host, guest, build machine | macOS 27.2 (build 26B123), Apple silicon (arm64), 1512×982 logical desktop. Python 3.14.0 + Tk 8.6, Go 1.26.4. |
| **Linux PC** | Host and guest | Lenovo V14 G2 ITL, Ubuntu 24.04.1 LTS, kernel 7.0.0-38, X11 session at 1366×768. Reached over LAN at `MCITLPTP810.local` (192.168.1.13) with SSH. |
| **Android phone** | Host and guest | Samsung SM-S711B, Android 16, 1080×2340. Connected over USB debugging (serial `RZCX91FNKRY`) and on the same Wi-Fi LAN. |
| **Wine (macOS)** | Windows build + runtime validation | Wine Staging 11.21 in a dedicated prefix. Used to build and exercise the Windows launcher/agent where real Windows hardware is unavailable. |

All three physical devices are on the same L2 network (`192.168.1.0/24`), so
any pair can be tested host ↔ guest without a rendezvous server.

---

## 2. Software and libraries

### Build and runtime

| Purpose | Tool / library |
| --- | --- |
| Host agent, rendezvous server | Go 1.26 (`net/http`, `embed`), `github.com/kbinani/screenshot` (X11/GDI capture), `github.com/jezek/xgb` (X11), Pion (`mediadevices`, `openh264`) for the optional H.264 recording track |
| Desktop launcher and guest viewer | Python 3.12–3.14, Tkinter/ttk, Pillow (JPEG decode), standard library only otherwise |
| Windows launcher packaging | PyInstaller 6.22 (onefile), Pillow for the `.ico`, NSIS 3 for the installer, mingw-w64 for the CGO agent |
| Android host and guest | Kotlin, Android SDK 36, NanoHTTPD, `MediaProjection`, `AccessibilityService`, `WebView` |
| Browser viewer / control panel | Plain HTML/CSS/JS embedded with `go:embed` (no framework, no CDN) |
| Cross-building | Docker + Colima (arm64 and Rosetta x86 profile), Ubuntu 24.04 images |
| Windows cross-building | Wine Staging 11.21 + python-build-standalone 3.12.16 (Tk 8.6) |

### Verification tooling

| Tool | What it checks |
| --- | --- |
| `go vet`, `go test`, `go build` | Compilation, races in unit tests, protocol/identity/pairing logic |
| `pytest` (96 tests) + `pyflakes` | Launcher helpers, CLI (headless connect/probe/clipboard/input), profile store, keyboard/scroll mapping, viewer argv, panning logic |
| `gosec` | Go static security analysis (medium and above) |
| `govulncheck` | Known vulnerabilities reachable from the module graph |
| `shellcheck` | All build and test shell scripts |
| `curl` | HTTP contract: status codes, content types, ETag/304, byte counts |
| `adb` (`shell`, `forward`, `uiautomator`, `screencap`, `input`) | Android install, UI automation, screen capture, host port forwarding, input verification |
| In-app Chromium (OpenCode browser tools) | Browser guest and control-panel rendering, DOM/snapshot assertions |
| `screencapture` + Swift/CoreGraphics | macOS window capture for UI evidence |
| `scrot` | Linux screen capture over SSH |
| `codesign` | macOS bundle signature verification |
| `lsb_release`, `xdpyinfo`, `hostnamectl` | Linux environment fingerprint |

---

## 3. What "working" means

A build is accepted only if **all** of the following hold for the tested pair:

1. **Discovery** — the host answers `/info` with peer id, alias, LAN/WAN IP and port.
2. **Streaming** — `/frame.jpg` returns `200 image/jpeg`, the bytes start with
   the JPEG SOI marker `ff d8 ff`, and the decoded size matches the host screen.
3. **Caching** — an `If-None-Match` request with the previous ETag returns `304`
   when the screen has not changed, and a new frame otherwise.
4. **Input** — `/input` accepts events with `204`; on Android the effect is
   proven by a visible state change (see §6.4).
5. **Control panel** — `/ui` returns the HTML page and the browser renders it.
6. **Guest app** — the desktop viewer or Android `ViewerActivity` displays the
   remote screen and reports a resolution/fps status.
7. **Shutdown** — `/quit` (authenticated) returns `204` and the port stops
   answering.
8. **UI consistency** — every surface uses the Ant Design light theme tokens
   described in [`UI-DESIGN.md`](UI-DESIGN.md).

---

## 4. Test levels

### 4.1 Static and unit

```sh
cd RDP_Link_v2
bash scripts/test-all.sh          # go vet/test/build + pyflakes + pytest
bash scripts/security-scan.sh     # gosec + govulncheck + shellcheck
```

Expected tail: `ALL TESTS PASSED` and `SECURITY SCANS PASSED`.

### 4.2 Protocol checks (host, any platform)

```sh
curl -s -H "X-Office-Password: PW" http://HOST:8099/info
curl -s -o /tmp/f.jpg -w '%{http_code} %{size_download} %{content_type}\n' \
  -H "X-Office-Password: PW" 'http://HOST:8099/frame.jpg'
od -An -tx1 -N3 /tmp/f.jpg                     # ff d8 ff
etag=$(curl -sD- -o /dev/null -H "X-Office-Password: PW" 'http://HOST:8099/frame.jpg' \
  | tr -d '\r' | awk -F': ' 'tolower($1)=="etag"{print $2}')
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "If-None-Match: $etag" -H 'X-Office-Password: PW' \
  'http://HOST:8099/frame.jpg'
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  -H 'Content-Type: application/json' -H 'X-Office-Password: PW' \
  -d '{"type":"move","x":10,"y":10}' 'http://HOST:8099/input'
curl -s -o /dev/null -w '%{http_code}\n' http://HOST:8099/ui
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  -H 'X-Office-Password: PW' http://HOST:8099/quit
```

### 4.2b Display scaling and orientation

```sh
# Browser: pick 100% and confirm the native frame size and a scrollable stage
# (DevTools/DOM): zoom.value = '1'; dispatchEvent(new Event('change'))
# expect frame.style.width == '<host width>px' and stage.scrollWidth >
# stage.clientWidth when the host is wider than the viewport.

# Android: rotate and confirm the activity survives and reflows
adb -s <serial> shell settings put system accelerometer_rotation 0
adb -s <serial> shell settings put system user_rotation 1    # landscape
adb -s <serial> exec-out screencap -p > /tmp/landscape.png   # expect 2400x1080
adb -s <serial> shell settings put system user_rotation 0    # portrait
```

Both viewers must offer Auto fit, 50 %, 75 %, 100 %, 125 % and 150 %.

### 4.3 Desktop guest checks

```sh
# Headless: fetch one frame through the real viewer code path
rdp-link --viewer --probe http://HOST:8099 --password PW
#   probe: OK <bytes> <width>x<height>

# Interactive: open the window and capture it
rdp-link --viewer http://HOST:8099 --password PW
```

macOS window capture for evidence:

```sh
swift -e 'import CoreGraphics; …CGWindowListCopyWindowInfo…'   # find window id
screencapture -x -l <id> /tmp/evidence.png
```

Linux capture over SSH:

```sh
DISPLAY=:0 scrot /tmp/evidence.png
```

### 4.4 Browser guest checks

Open `http://HOST:8099/` in Chromium and type the password into the prompt. In
the DOM the status tag must read `<width>×<height> · <n> fps`; the overlay must
disappear after the password is accepted. On narrow (phone) viewports the
header wraps to two rows instead of truncating the status.

The control panel is `http://HOST:8099/ui`; it prompts for the password and
must then show the RDP Link ID, LAN/WAN addresses, a green “Sharing” tag and
working copy buttons.

A `#pw=` fragment is still accepted for scripted runs. It never reaches the
server, and the page removes it from the address bar as soon as it is read.

### 4.5 Android checks

```sh
adb -s <serial> install -r app-release.apk
adb -s <serial> shell am start -n com.eminentailabs.officerdp/.MainActivity

# Host: start sharing, choose "Share entire screen", then
adb -s <serial> forward tcp:18095 tcp:8095
curl -s -H 'X-Office-Password: PW' http://127.0.0.1:18095/info
curl -s -o /tmp/a.jpg -w '%{http_code} %{size_download}\n' \
  -H 'X-Office-Password: PW' 'http://127.0.0.1:18095/frame.jpg'

# Guest: enter http://MAC:8099/ and tap Connect, then
adb -s <serial> exec-out screencap -p > /tmp/guest.png

# UI inspection
adb -s <serial> shell uiautomator dump /sdcard/ui.xml
adb -s <serial> shell cat /sdcard/ui.xml

# Remote-control proof: inject a tap via the host API and read the result back
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  -H 'Content-Type: application/json' -H 'X-Office-Password: PW' \
  -d '{"type":"click","x":898,"y":770}' 'http://127.0.0.1:18095/input'
```

`/input` must return `204`, and a follow-up `uiautomator dump` must show the
changed value.

### 4.2c WAN relay

```sh
office-rdp-rendezvous --listen :18080 --public-url http://192.168.1.14:18080
office-rdp --host --port 18530 --password relay1 \
  --rendezvous http://192.168.1.14:18080

curl -s 'http://192.168.1.14:18080/resolve?peerId=033976933'
# {"relay":true,"url":"http://192.168.1.14:18080/t/033976933/",...}

curl -s -o f.jpg -w '%{http_code} %{size_download}\n' \
  -H "X-Office-Password: relay1" \
  'http://192.168.1.14:18080/t/033976933/frame.jpg'
```

TLS: add `--tls-cert/--tls-key` to the relay, `--relay-insecure` (or
`--relay-ca`) to the host, use `https://…` and `curl -k` for a self-signed
certificate. The viewer must be opened at the **trailing-slash** URL.

### 4.2d Android parity (clipboard and files)

```sh
adb forward tcp:18088 tcp:8088
PW=552436
curl -s -H "X-Office-Password: $PW" \
  "http://127.0.0.1:18088/clipboard"                               # GET
curl -s -X POST -H 'Content-Type: application/json' \
  -H "X-Office-Password: $PW" \
  -d '{"text":"hello"}' "http://127.0.0.1:18088/clipboard"
curl -s -H "X-Office-Password: $PW" "http://127.0.0.1:18088/files"
curl -s -H "X-Office-Password: $PW" -F "file=@/tmp/parity.txt;filename=parity.txt" \
  "http://127.0.0.1:18088/upload?name=parity.txt"
curl -s -H "X-Office-Password: $PW" -o out.txt \
  "http://127.0.0.1:18088/download?name=parity.txt"
```

### 4.7 Device bridge

```sh
# Host with devices enabled
office-rdp --host --port 8088 --password PW \
  --audio-input "lavfi:sine=frequency=440" \
  --camera-device testsrc \
  --serial tcp:127.0.0.1:9101 \
  --forward 9099

# Capabilities (always authenticated)
curl -s "http://HOST:8088/capabilities-H 'X-Office-Password: PW'"

# Speaker (2 s, valid Opus/WebM)
curl -s -m 2 -o a.webm "http://HOST:8088/audio-H 'X-Office-Password: PW'"
ffmpeg -i a.webm            # Audio: opus, 48000 Hz, mono

# Camera (testsrc avoids camera permissions)
curl -s -m 2 -o c.mjpeg "http://HOST:8088/camera.mjpg-H 'X-Office-Password: PW'"
# MJPEG 640x480 frames

# Port allow-list
curl -s "http://HOST:8088/port/9099/-H 'X-Office-Password: PW'"

# Serial WebSocket (direct)
go run ./scripts/wsprobe -url "ws://HOST:8088/serial-H 'X-Office-Password: PW'" -send $'ping\n'

# Microphone (direct): send a WebM/Opus chunk
ffmpeg -f lavfi -i "sine=frequency=660" -t 1 -c:a libopus -f webm mic.webm
go run ./scripts/wsprobe -url "ws://HOST:8088/mic-H 'X-Office-Password: PW'" -file mic.webm -no-wait
```

Evaluation: `/capabilities` lists only configured devices; audio decodes as
Opus; MJPEG frames are valid JPEG; the serial echo matches; `/port/<p>/` only
works for allow-listed ports.

### 4.6 Evaluation criteria

| Area | Pass condition |
| --- | --- |
| Frame latency / smoothness | Interactive desktop stream is usable (>3 fps on a static desktop, >10 fps while moving) |
| Image fidelity | Full screen resolution preserved; no colour inversion or cropping |
| Input accuracy | Click lands on the intended widget (proof: password regenerated) |
| Resilience | Killing/restarting the host reconnects without restarting the viewer; static screens return `304` |
| UI consistency | Same palette, radii, typography, logo and component shapes on HTML, Tk and Android |
| Scaling | Auto fit and every fixed scale render the full host frame; input stays aligned after scrolling |
| Orientation | Android guest survives rotation without losing the session; browser reflows |
| Host display picker | `/displays` lists monitors and `POST /display` switches them |
| WAN relay | `/resolve` returns a relay URL; `/t/<id>/*` streams and accepts input |
| Android parity | Clipboard GET/POST and file upload/list/download round-trip |
| Device bridge | Audio/Opus, MJPEG camera, serial echo and allow-listed port proxy verified |
| Device limits | Mic/serial are LAN-only over the relay; USB is storage-level |
| Security | No unauthenticated frame/input access; `/quit` requires the password; scans are clean |

---

## 5. Devices and how they were reached

### 5.1 macOS (local)

- Build and run directly from the repo.
- Window IDs found with `CGWindowListCopyWindowInfo` through `swift -e`.
- The app bundle was rebuilt after each UI change and re-signed ad-hoc
  (`codesign --verify --strict` passes).

### 5.2 Linux PC (LAN + SSH)

```sh
sshpass -p '…' ssh tarun@MCITLPTP810.local
scp rdplink-linux-amd64.deb tarun@MCITLPTP810.local:~
ssh tarun@MCITLPTP810.local 'echo … | sudo -S dpkg -i ~/officerdp-amd64.deb'
ssh tarun@MCITLPTP810.local 'DISPLAY=:0 setsid office-rdp --host --port 8099 --password lintest'
```

The host ran on the real X session (`:0`, 1366×768), not Xvfb. The viewer was
started on the same machine against the macOS host; the screen was captured
with `scrot` and copied back with `scp`.

### 5.3 Android (USB + LAN)

- `adb devices` lists both the USB serial and the Wi-Fi pair.
- Screen-share consent on Android 16 requires choosing **Share entire screen**
  in the system dialog; the first run also asks for notification permission
  (`pm grant … POST_NOTIFICATIONS` avoids the prompt on later runs).
- Remote control is enabled through the app's accessibility service; after
  enabling, the status tag turns green (`Remote control enabled`).

### 5.4 Windows (Wine)

```sh
source packaging/env.sh           # Wine + Windows Python live in .build/
"$WINE" rdp-link.exe --viewer --probe http://127.0.0.1:8099 --password PW
"$WINE" office-rdp.exe --host --test-pattern --port 18099 --password wintest
"$WINE" RDPLink-setup.exe /S    # silent install check
```

Wine cannot capture the desktop (`BitBlt` fails), so the Windows *host* is
validated with `--test-pattern`; the *guest* is fully validated under Wine.

---

## 6. Evidence from the 2026-11-03 pass

### 6.1 Cross-device matrix actually exercised

| Host → Guest | Result |
| --- | --- |
| Linux (1366×768, real X) → macOS viewer | Live desktop rendered; `/frame.jpg` 98,111 bytes; status `1366×768` |
| macOS (1512×982, real capture) → Linux viewer | Live desktop rendered at `1512×982 · 5–6 fps`; `/frame.jpg` 183,804 bytes |
| Android (1080×2340, MediaProjection) → macOS viewer | Live phone screen at `1080×2340 · 35 fps`; `/frame.jpg` 124,444 bytes |
| macOS → Android guest (physical phone) | Portrait 1080×2340, landscape 2340×1080, status `1512×982 · 9 fps`; selecting 100 % switches to native size with a scrollable stage; same APK also ran on the API 36 emulator |
| macOS → Android `ViewerActivity` | Full-screen guest with Ant Design header; status `1512×982` |
| Windows (Wine, test pattern) → in-app Chromium | `1280×800 · 6 fps` |
| Any host → browser | Password overlay, stream, input, fit/fullscreen, clipboard |

### 6.2 Protocol results

| Host | `/info` | `/frame.jpg` | ETag `304` | `/input` | `/ui` | `/quit` |
| --- | --- | --- | --- | --- | --- | --- |
| macOS | 200 | 200, JPEG | yes | 204 | 200 | 204 |
| Linux (arm64 deb) | 200 | 200, 16,593 B | yes | 204 | 200 | 204 |
| Windows (Wine) | 200 | 200, 25,846 B | changes per frame | 204 | 200 | 204 |
| Android | 200 | 200, 124,444 B | yes | 204 | 200 | 204 |

### 6.3 Automated suites

```
go vet ./...            clean
go test ./...           all packages pass
go build ./...          ok
pyflakes                clean
pytest                  80 passed
gosec (medium+)         pass
govulncheck             no reachable vulnerabilities
shellcheck              pass
```

### 6.3b Relay and Android parity (this pass)

```
resolve → relay:true, url .../t/033976933/
/t/033976933/ui            200
/t/033976933/frame.jpg     200 26,603 bytes
TLS variant (self-signed)  200 26,782 bytes
Android guest through relay 1280×800 · 11 fps
Android host clipboard     POST 204 → GET returns the text
Android host files         upload 204 → ["parity.txt"] → download 200, MATCH
```

### 6.3c Security pass (this pass)

```
# credentials
/info without credentials          401
/info?password=PW                   401      (the password is not a URL credential)
/info with X-Office-Password       200
POST /session                      200 + rdplink_sid cookie (HttpOnly, SameSite=Strict)
/info with the cookie              200
/info?token=<session token>        401      (a session token is not a URL credential)
POST /media-token                  200 + 2-minute read-only token
/frame.jpg?token=<media token>     200/503  (503 when no frame is captured)
/info?token=<media token>          401      (media token scope enforced)
cross-origin POST /input           403
spoofed X-Forwarded-For            ignored (no relay secret)
unsigned POST /register            401
peer claiming another peer ID      rejected (peer ID must match the key)
anonymous GET /peers               401
office-rdp --list-peers <relay>    the registry

# relay
signed tunnel                      connected
frame through /t/<peer>/           200, JPEG
audio stream, guest disconnects    host ffmpeg stops, tunnel stays healthy
second stream after that           200

# browser guest
password → session cookie          the frame renders at 1280×800 · 22 fps
Devices drawer → Files             upload + list + download work

# second pass
cross-origin /capabilities         403 (device bridge included)
cookie through the relay           Path=/t/<peerId>/
browser → host clipboard           text landed on pbpaste and in GET /clipboard
relay client with the agent gone   the reconnect loop returns
```

### 6.4 Input-injection proof (Android)

1. Read the access password from the UI: `768516`.
2. `POST /input {"type":"click","x":898,"y":770}` on the “New” button → `204`.
3. Re-read the UI: password is now `227170`.
4. Status tag reads **Remote control enabled** (green Ant Design tag).

### 6.5 UI evidence

- Launcher (macOS): white header bar, card panels, Ant Design blue primary
  buttons, striped table, pinned white footer.
- Viewer (macOS/Linux): white toolbar, blue mark, resolution/fps tag,
  outlined action buttons, light stage, keyboard bar.
- Control panel + browser viewer: identical tokens, responsive header.
- Android: same palette, rounded 6–8 dp cards/inputs/buttons, tag-coloured
  status, logo in the header and launcher.
- Windows launcher under Wine: same layout and palette.
- Brand: the monitor/chevron mark and the two-tone "RDP Link" wordmark appear
  in the launcher, both viewers, the browser pages and the Android header;
  the same mark is the macOS, Windows and Android application icon.

Screenshots were captured for each surface during the pass.

---

## 7. Known limitations

- **Windows GDI capture** needs real Windows hardware. Under Wine the host
  only runs with `--test-pattern`; the installer and launcher are otherwise
  fully exercised.
- **macOS notarization** requires Apple credentials. The ad-hoc signature is
  verified.
- **Linux amd64 live capture** could not be re-validated under the x86-emulated
  container (its Xvfb drops the X connection inside the third-party `xgb`
  library). The native arm64 `.deb` is fully validated and the code is
  identical.
- **macOS self-connect**: connecting a local viewer to the same Mac's LAN IP
  can be reset by the OS before it reaches the Go listener; use `127.0.0.1`
  for same-machine tests. External LAN clients (Linux PC, Android) work.
- **H.264 recording track** is built and started but the product viewers use the HTTP
  JPEG path; the Pion track is not exercised end-to-end.
- **Android** was validated on one physical device (Android 16); behaviour can
  differ on vendor ROMs.

---

## 8. Reproducing the full pass

```sh
# 1. Build everything
bash packaging/build-macos.sh
bash packaging/build-linux.sh && bash packaging/build-linux-deb.sh
bash packaging/build-windows-app-wine.sh
bash packaging/windows/build-installer.sh
(cd android && gradle assembleRelease)

# 2. Static + unit gates
bash scripts/test-all.sh
bash scripts/security-scan.sh

# 3. Host + guest on each device (see §4.2–§4.5)
# 4. Record evidence and update docs/VALIDATION.md
```

The build toolchain lives in `.build/` (git-ignored) and is configured by
`packaging/env.sh`; see [`RELEASING.md`](RELEASING.md#build-environment).
