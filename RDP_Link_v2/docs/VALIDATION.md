# Validation and working-status matrix

Version 0.2.0. Last validated: 2026-11-03.

This page records what was actually exercised, not just what compiles. Each
cell refers to evidence below. "Live" means a real host and a real guest
exchanged frames and input during this validation pass.

## OS × Host / Guest matrix

| Platform | Host (share this screen) | Guest (control another device) |
| --- | --- | --- |
| **macOS** | **Working — live.** 1512×982 JPEG stream at ~11 fps from `Office RDP.app`; `/ui`, `/input`, `/quit`, content ETag `304`. LAN clients verified from the Android emulator against this host. | **Working — live.** Bundled `rdp-link` viewer window showed the live screen at 1512×982 · 11 fps; `--viewer --probe` returned a 160,989-byte frame. Browser guest works too. |
| **Linux** | **Working — live (arm64).** Ubuntu 24.04 + Xvfb: `/info`, JPEG `/frame.jpg` (16,593 bytes), content ETag `304`, `/input`, `/ui`, `/quit`, `--doctor` → `Displays: 1, Capture: OK`. The amd64 `.deb` installs and runs `--version`/`--doctor`; live capture there could not be re-validated because the x86-emulated container's Xvfb resets the X connection (third-party xgb path), not a native target. | **Working — live.** Bundled `rdp-link` viewer `--viewer --probe` against the container host (16,593 bytes); Tk viewer previously driven live against macOS and Android hosts. |
| **Windows** | **Working under Wine — test pattern.** Real GDI capture needs Windows hardware; under Wine `office-rdp.exe --host --test-pattern` served `/info`, a 1280×800 JPEG (25,846 bytes), changing ETags, `/input` 204 and `/ui` 200. | **Working — live under Wine.** `rdp-link.exe` (PyInstaller, no Python needed) rendered the light launcher with Office ID `001 796 798`; `--viewer --probe` returned a 1512×982, 117,187-byte frame. Browser guest showed `1280×800 · 6 fps`. |
| **Android** | **Working — live.** Release APK on an API 36 emulator: `/info`, JPEG `/frame.jpg` (~99 KB), accessibility tap injection changed the on-screen password. | **Working — live.** Release APK full-screen `ViewerActivity` → macOS host at 1512×982, 11 fps, touch and keyboard input. |

**Browser guest (all platforms) — working.** The host serves the same
`viewer.html` on macOS, Linux, Windows and Android, so any device with a
browser can connect without installing anything: password login, JPEG stream,
click/right-click/wheel/touch, keyboard, clipboard, fit/1:1, fullscreen, and
ETag-aware polling.

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
govulncheck             No vulnerabilities found
shellcheck              pass
SECURITY SCANS PASSED
```

### macOS host and guest

```
$ "Office RDP.app/Contents/MacOS/office-rdp" --host --port 18410 --password browser1
host agent starting — peer id: 033 976 933
LAN:  http://192.168.1.14:18410/
WAN:  http://110.226.115.245:18410/
mDNS: advertised _office-rdp._tcp

$ curl -s http://127.0.0.1:18410/info
{"alias":"taruns-macbook-pro","lanIp":"192.168.1.14","peerId":"033 976 933",
 "port":18410,"wanIp":"110.226.115.245"}

$ "Office RDP.app/Contents/MacOS/RDP Link" --viewer --probe \
      http://127.0.0.1:18410 --password browser1
probe: OK 160989 bytes 1512x982

# bundled viewer window, light theme
status: 1512×982 · 11 fps
```

LAN reachability from a second device (Android emulator, which leaves the host
network stack instead of looping back):

```
$ adb shell 'printf "GET /info HTTP/1.0\r\n\r\n" | toybox nc 192.168.1.14 18471'
{"alias":"taruns-macbook-pro","lanIp":"192.168.1.14","peerId":"033 976 933", ...}

$ adb shell 'printf "GET /frame.jpg?password=emu1 HTTP/1.0\r\n\r\n" | \
      toybox nc 192.168.1.14 18471 | wc -c'
26937        # HTTP headers + JPEG payload
```

### Windows (Wine) — launcher, guest, host

```
$ wine rdp-link.exe --viewer --probe http://127.0.0.1:18410 --password browser1
probe: OK 117187 bytes 1512x982

# launcher GUI: light theme, Office ID 001 796 798, Start sharing / Connect

$ wine office-rdp.exe --host --test-pattern --port 18099 --password wintest
$ curl -s http://127.0.0.1:18099/info
{"alias":"taruns-macbook-pro","lanIp":"192.168.1.14","peerId":"001 796 798",
 "port":18099,"wanIp":"110.226.115.245"}

$ curl -s -o /tmp/f.jpg -w '%{http_code} %{size_download} %{content_type}\n' \
    'http://127.0.0.1:18099/frame.jpg?password=wintest'
200 25846 image/jpeg          # ff d8 ff …, 1280×800
# ETag changes between frames: j3464622305461821911 → j17036180434706660559
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'Content-Type: application/json' -d '{"type":"click","x":120,"y":120}' \
    'http://127.0.0.1:18099/input?password=wintest'
204
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18099/ui
200
```

Browser guest (in-app Chromium) against the Windows host:

```
status: 1280×800 · 6 fps   (test pattern)
```

`office-rdp.exe` is a CGO build (GDI32/USER32), statically linked, so the
installer needs no MinGW DLLs. Wine cannot capture the desktop (`BitBlt`
fails), which is why the host evidence uses `--test-pattern`.

### Linux host and guest (both .deb architectures)

```
$ uname -m && office-rdp --version && rdp-link --version
aarch64
office-rdp 0.2.0
RDP Link 0.2.0

$ DISPLAY=:99 office-rdp --doctor
Displays: 1
Capture: OK

$ curl -s -o /tmp/f.jpg -w '%{http_code} %{size_download} %{content_type}\n' \
    'http://127.0.0.1:8098/frame.jpg?password=armtest'
200 16593 image/jpeg
# conditional request with the previous ETag → 304
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'Content-Type: application/json' -d '{"type":"move","x":10,"y":10}' \
    'http://127.0.0.1:8098/input?password=armtest'
204
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8098/ui
200
$ rdp-link --viewer --probe http://127.0.0.1:8098 --password armtest
probe: OK 16593 bytes
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'X-Office-Password: armtest' http://127.0.0.1:8098/quit
204
```

The amd64 `.deb` installs and `--doctor` reports `Capture: OK` under the x86 Colima profile. Its live stream could not be re-validated in this pass: the emulated container's Xvfb drops the connection inside the third-party xgb library. The code path is identical to the fully validated arm64 build.

### Desktop guest (Tk viewer)

Linux Tk viewer against the Android host:

```
Office RDP   1080×2400 · 8 fps
```

Linux Tk viewer against the macOS host:

```
Office RDP   1512×982 · 3 fps (static lock screen: most polls are 304)
```

The viewer decodes JPEG off the Tk thread, reuses ETags, and scales to the
window. It is the same `app/office_viewer.py` that the macOS and Windows
launchers ship, frozen into `rdp-link.exe` on Windows.

## Artifacts

| Artifact | Size | Notes |
| --- | --- | --- |
| `dist/macos/OfficeRDP-macos.dmg` | 56 MB | `.app` with arm64 + amd64 Go tools, ad-hoc signed, verifies with `codesign --verify --strict` |
| `dist/windows/OfficeRDP-setup.exe` | 23 MB | NSIS; ships `rdp-link.exe`, CGO `office-rdp.exe`, rendezvous, Start-menu + desktop shortcuts, Add/Remove Programs, firewall rule |
| `dist/windows/rdp-link.exe` | 17 MB | PyInstaller onefile launcher + bundled viewer (no Python on target) |
| `dist/linux/officerdp-linux-arm64.deb` | 24 MB | `office-rdp`, `office-rdp-rendezvous`, `rdp-link`, docs |
| `dist/linux/officerdp-linux-amd64.deb` | 25 MB | same payload for x86-64 |
| `dist/linux/officerdp-linux.tar.gz` | 80 MB | both architectures plus the launcher |
| `dist/android/OfficeRDP-android-release.apk` | 21 MB | release-signed, `versionCode 2` |
| `dist/android/OfficeRDP-android-release.aab` | 8.3 MB | Play upload bundle |

## Platform notes and not verified here

- **macOS firewall:** the Application Firewall is enabled by default. External
  LAN clients were verified from the Android emulator; connecting from the
  same Mac to its own LAN address can be dropped before it reaches a local Go
  process, so use `127.0.0.1` (or the mDNS name) for same-machine tests.
- **Windows GDI capture:** requires a real Windows session; Wine cannot
  capture the desktop, so only the test-pattern path runs here. CI's
  `windows-installer` job exercises real capture.
- **Apple Developer ID signing/notarization:** requires Apple credentials. The
  ad-hoc path is verified; `packaging/mac-notarize.sh` runs when `APPLE_ID`,
  `APPLE_APP_SPECIFIC_PASSWORD` and `APPLE_TEAM_ID` are set.
- **Physical Android device:** validated on an API 36 emulator only.
- **WebRTC media path:** the HTTP JPEG path is what all viewers use; the
  Pion/OpenH264 track is built and started but not exercised end-to-end here.
