# Validation and working-status matrix

Version 0.2.0. Last validated: 2026-11-03.

This page records what was actually exercised, not just what compiles. Each
cell links to the evidence below. "Live" means a real host and a real guest
exchanged frames and input during this validation pass.

## OS × Host / Guest matrix

| Platform | Host (share this screen) | Guest (control another device) |
| --- | --- | --- |
| **macOS** | **Working — live.** 1512×982 JPEG stream at ~11 fps; remote clicks moved the macOS cursor; `/ui` control panel, `/quit`, ETag `304`. | **Working.** Browser guest verified live against this host. Desktop Tk viewer shares the same validated code path (verified under Xvfb; the macOS window could not be shown while the screen was locked). |
| **Linux** | **Working — live.** Ubuntu 24.04 + Xvfb: `/info`, JPEG `/frame.jpg` (16.6 KB), content ETag `304`, `/ui`, `--doctor` → `Displays: 1, Capture: OK`. `.deb` installs and runs on arm64 and amd64. | **Working — live.** Tk viewer → Android host (1080×2400) and → macOS host (1512×982), JPEG + ETag streaming, scaling, input. |
| **Windows** | **Built and inspected, not executed here.** `office-rdp.exe` is a CGO build (GDI32/USER32 screen capture and input), statically linked so it needs no MinGW runtime DLLs. No Windows machine was available in this session; the CI `windows-installer` job builds and runs it. | **Working via browser.** The Windows GUI is the browser control panel (`office-rdp.exe --ui`); the browser guest is the same validated viewer. A native `rdp-link.exe` can be produced with `packaging/build-windows.ps1` on a Windows host. |
| **Android** | **Working — live.** Release APK on an API 36 emulator: `/info`, JPEG `/frame.jpg` (~99 KB), accessibility tap injection changed the on-screen password (936752 → 552436). | **Working — live.** Release APK full-screen `ViewerActivity` → macOS host at 1512×982, 11 fps, touch and keyboard input. |

Browser guest (any OS) is **Working — live**: password login, 1512×982
stream, click/right-click/wheel/touch, keyboard, clipboard, fit/1:1,
fullscreen, ETag-aware streaming.

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
govulncheck             No vulnerabilities found (Go 1.26.10)
shellcheck              pass
SECURITY SCANS PASSED
```

### macOS host

```
$ office-rdp --host --port 18410 --password browser1
host agent starting — peer id: 033 976 933
LAN:  http://192.168.1.14:18410/
WAN:  http://110.226.115.245:18410/
mDNS: advertised _office-rdp._tcp

$ curl -s http://127.0.0.1:18410/info
{"alias":"taruns-macbook-pro","lanIp":"192.168.1.14","peerId":"033 976 933",
 "port":18410,"wanIp":"110.226.115.245"}

$ curl -sD- -o /dev/null http://127.0.0.1:18410/frame.jpg?password=browser1
HTTP/1.1 200 OK
Content-Type: image/jpeg
Etag: "j7874348427109231691"

# identical screen → content hash unchanged → 304
$ curl -s -o /dev/null -w '%{http_code}' -H 'If-None-Match: "j7874348427109231691"' \
    'http://127.0.0.1:18410/frame.jpg?password=browser1'
304

$ curl -s -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:18410/quit
401
$ curl -s -o /dev/null -w '%{http_code}' -X POST -H 'X-Office-Password: browser1' \
    http://127.0.0.1:18410/quit
204
# server exits gracefully afterwards
```

Frame size dropped from 1.64 MB (PNG served as `image/jpeg`) to ~150 KB JPEG;
PNG is encoded lazily only when `/frame.png` is requested (1.6 MB, 0.28 s
first hit, cached per frame afterwards).

Browser guest (in-app Chromium) against this host:

```
status: 1512×982 · 6–11 fps
overlay hidden after password login
frame.jpg requests served with ETag; /input POST accepted
```

Android guest against this host:

```
ViewerActivity full-screen, hash prefill (#pw=...) auto-connects
status: 1512×982 · 11 fps
```

### Android host (release APK)

```
$ adb install dist/android/OfficeRDP-android-release.apk
Success
versionCode=2 versionName=0.2.0

$ curl -s http://127.0.0.1:18088/info
{"peerId":"740 033 067","alias":"Android","status":"online","port":8088,
 "version":"0.2.0","platform":"android"}

$ curl -s -o /tmp/f.jpg -w '%{http_code} %{size_download} %{content_type}\n' \
    'http://127.0.0.1:18088/frame.jpg?password=936752'
200 99121 image/jpeg

# accessibility injection: tap the on-screen NEW button
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' \
    -d '{"type":"click","x":920,"y":510}' \
    'http://127.0.0.1:18088/input?password=936752'
204
password after: 552436     # changed by the remote tap
```

The release APK is signed with a local release keystore
(`~/.config/officerdp/officerdp-release.keystore`, password in
`android-signing.env`, both `0600`). Replace it with your Play/F-Droid key:
see `docs/RELEASING.md`.

### Linux host

```
$ apt-get install -y ./officerdp-linux-arm64.deb
Architecture: arm64
Version: 0.2.0
Depends: libc6, libx11-6, libxext6, libxrandr2, libxinerama1,
         libxcursor1, libxi6, libxtst6

$ office-rdp --version
office-rdp 0.2.0

$ DISPLAY=:99 office-rdp --doctor
Displays: 1
Capture: OK

$ curl -s -o /tmp/f.jpg -w '%{http_code} %{size_download} %{content_type}\n' \
    'http://127.0.0.1:8099/frame.jpg?password=linuxtest'
200 16593 image/jpeg
# bytes start with ff d8 ff (JPEG SOI)
# content ETag conditional request → 304
$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8099/ui
200
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    -H 'X-Office-Password: linuxtest' http://127.0.0.1:8099/quit
204        # agent exits; the port stops answering
```

The amd64 `.deb` installs and runs the same way under a Rosetta-enabled
Colima profile.

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
launchers ship.

## Artifacts

| Artifact | Size | Notes |
| --- | --- | --- |
| `dist/macos/OfficeRDP-macos.dmg` | 47 MB | `.app` with arm64 + amd64 Go tools, ad-hoc signed, verifies with `codesign --verify --strict` |
| `dist/windows/OfficeRDP-setup.exe` | 6.1 MB | NSIS; ships CGO `office-rdp.exe`, rendezvous, Start-menu + desktop shortcuts, Add/Remove Programs, firewall rule |
| `dist/linux/officerdp-linux-arm64.deb` | 24 MB | `office-rdp`, `office-rdp-rendezvous`, `rdp-link`, docs |
| `dist/linux/officerdp-linux-amd64.deb` | 25 MB | same payload for x86-64 |
| `dist/linux/officerdp-linux.tar.gz` | 96 MB | both architectures plus the launcher |
| `dist/android/OfficeRDP-android-release.apk` | 21 MB | release-signed, `versionCode 2` |
| `dist/android/OfficeRDP-android-release.aab` | 8.3 MB | Play upload bundle |

## Not verified in this session

- **Apple Developer ID signing/notarization**: requires an Apple account.
  The ad-hoc path is verified; `packaging/mac-notarize.sh` runs when
  `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, and `APPLE_TEAM_ID` are set.
- **Windows execution**: no Windows host or Wine here. The binary is
  cross-built with MinGW, statically linked, and inspected (PE32+, imports
  GDI32/USER32 only). CI runs the real thing.
- **Tk window on macOS**: the desktop was locked, so the window could not be
  captured; the identical code was validated under Xvfb.
- **Physical Android device**: validated on an API 36 emulator only.
- **WebRTC media path**: the HTTP JPEG path is what all viewers use; the
  Pion/OpenH264 track is built and started but not exercised end-to-end here.
