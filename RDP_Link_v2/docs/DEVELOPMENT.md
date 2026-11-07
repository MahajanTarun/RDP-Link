# Development guide

How to set up a dev environment, build every platform, run the test gates, and
contribute changes to RDP Link. For architecture, see
[`ARCHITECTURE.md`](ARCHITECTURE.md); for releasing, see
[`RELEASING.md`](RELEASING.md).

---

## 1. Requirements

| Purpose | Tool |
| --- | --- |
| Host agent, relay, core | Go 1.26+ (with CGO; Xcode CLT on macOS) |
| Android app | Gradle (system install), Android SDK 36, NDK-adjacent gomobile (`golang.org/x/mobile`) |
| Desktop launcher/viewer | Python 3.12–3.14 (Tk), pyflakes + pytest for the test gate |
| Packaging | PyInstaller 6 (desktop bundles), NSIS via Wine (Windows), `dpkg-deb` (Linux) |
| Optional docs/diagrams | Mermaid (rendered by GitHub automatically) |

Most build prerequisites are installed into `.build/` (git-ignored) by
`packaging/env.sh`; see [`RELEASING.md`](RELEASING.md#build-environment).

## 2. Repository layout

```
cmd/office-rdp         host agent + viewer launcher + CLI
cmd/rendezvous         rendezvous/relay server
core/                  host, identity, peerauth, capture, input, clipboard,
                       policy, peers, relay client (Go)
rendezvous/            registry, signed auth, reverse-tunnel proxy
mobile/                gomobile bindings used by the Android app
app/                   Python/Tk launcher, headless CLI, viewer, assets
android/               Kotlin host service + viewer + accessibility input
macos/                 TCC permission helper (Swift)
tools/relay-monitor/   lightweight VPS telemetry dashboard
packaging/             build scripts per platform
releases/              built artifacts + SHA256SUMS
scripts/               test-all, security-scan, sync-android-assets, wsprobe
docs/                  this documentation set
```

## 3. Build & run from source

```sh
# Go core (agent + relay)
go build ./cmd/office-rdp
go build ./cmd/rendezvous

# Run a host from source
go run ./cmd/office-rdp --host --port 8099 --password dev123

# Desktop launcher / viewer (source checkout)
cd app
python3 -m pip install pyflakes pytest pillow
python3 ubuntu_rdp_manager.py                 # GUI
python3 ubuntu_rdp_manager.py --id            # headless CLI

# Android (host + viewer)
scripts/sync-android-assets.sh                 # copy viewer.html + logo into the app
cd android && gradle assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk

# Android gomobile core after changing mobile/ or core/
bash packaging/build-android-core.sh           # writes android/app/libs/mobile.aar
```

## 4. Test gates (must pass before a merge/release)

```sh
bash scripts/test-all.sh       # go vet + go test + go build, asset sync check, pyflakes, pytest
bash scripts/security-scan.sh  # gosec (medium+), govulncheck, shellcheck
```

Expected tails: `ALL TESTS PASSED` and `SECURITY SCANS PASSED`. The Android
UI changes must additionally be checked on a device/emulator: portrait,
landscape, and at least one different screen size (`adb shell wm size`).

## 5. Coding conventions

- **Go:** `gofmt`; keep handlers small; `http.MaxBytesReader` on every body;
  constant-time compares for secrets; no credentials in URLs or logs.
- **Kotlin:** minimal-dependency stdlib style; responsive `values-swXXXdp`
  resources; never report input success when the accessibility service is
  unbound.
- **Python:** stdlib + Pillow only in the viewer; launcher must run without a
  display server when given a CLI flag (no tkinter import on the CLI path).
- **Security defaults:** new authenticated endpoints go through the shared
  `auth()` path; document any new credential type in `docs/SECURITY.md`.

## 6. Contributing

1. Open an issue/discussion for anything beyond a small fix.
2. Keep changes minimal and covered by the existing gates; add tests for new
   behaviour (core Go packages have table-driven tests; Python has pytest).
3. Update docs touched by the change (README, WAN.md, SECURITY.md, …) and
   `docs/VALIDATION.md` when behaviour was re-verified on hardware.
4. PRs run the same gates in CI per `.github/workflows/ci.yml`.

## 7. Releasing

`bash scripts/test-all.sh && bash scripts/security-scan.sh`, then the
packaging scripts per platform, then regenerate `releases/SHA256SUMS` and
bump the version name/code in `android/app/build.gradle` and the README
badges. Full checklist: [`RELEASING.md`](RELEASING.md).

## 8. Debugging tips

- `office-rdp --doctor` — peer ID, displays, capture health (deadline-bounded),
  Accessibility permission, port probe.
- `--test-pattern` serves a synthetic frame — exercise the whole HTTP/viewer
  stack without a desktop session (used in CI and Wine).
- Android: `adb shell dumpsys accessibility` and the host service audit log
  (`run-as cat files/audit.log` on debug builds) answer most input questions.
- Relay: `/resolve?peerId=…` shows whether a host registered and tunnelled;
  the relay prints tunnel connect/disconnect lines on stdout.
