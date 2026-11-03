# Testing strategy & cross-OS tooling (Office RDP v2)

This document collects the tools and methods used to test Office RDP across
components and operating systems. It is the living reference for Phase 0/1
validation gates (see docs/PLAN-office-rdp.md §18-19).

## 1. Test layers

| Layer | Tooling | Location | Runs in CI |
|---|---|---|---|
| Go unit/integration | `go test ./...` | `core/`, `rendezvous/` | yes (ubuntu, macos) |
| Python unit | `pytest` | `app/tests/` | yes |
| Static (Go) | `go vet`, `golangci-lint` | all | yes |
| Static (Python) | `pyflakes` | `app/` | yes |
| CLI smoke | `office-rdp --version/--list/--id` | `cmd/` | yes |
| Rendezvous smoke | `curl` against local server | manual | optional |
| Cross-OS VM matrix | see §3 | manual/CI self-hosted | partial |
| Network emulation | `tc netem`, Clumsy, Network Link Conditioner | manual | no |
| Security | `gosec`, `govulncheck`, `pip-audit`, pcap | manual | partial |
| Fuzzing | `go test -fuzz`, Hypothesis (Python) | manual | scheduled |
| Performance | `office-rdp bench` (planned) | manual | non-blocking |

## 2. Local dev commands

```sh
# Go
go test ./...
go vet ./...
go build ./...

# Python
cd app && python -m pytest
python -m pyflakes rdp_manager tests ubuntu_rdp_manager.py

# Rendezvous smoke
go run ./cmd/rendezvous --listen :8099 &
curl -s localhost:8099/health
curl -s -XPOST localhost:8099/register -H 'content-type: application/json' \
  -d '{"peerId":"123","alias":"host"}'
curl -s 'localhost:8099/resolve?peerId=123'
```

## 3. Cross-OS testing methods & VM tools

### 3.1 macOS (host machine = darwin/arm64)

- Native testing on the dev machine (Apple Silicon) covers macOS guest/host.
- **UTM** (https://mac.getutm.app) — QEMU-based VMs on macOS; runs Ubuntu
  x86_64/arm64 and Windows 11 (ARM) guests. Good for Linux/Windows smoke.
- **Tart** (https://tart.run) — OCI VMs for macOS (macOS + Linux guests) with
  CI-friendly CLI; ideal for macOS guest images in GitHub Actions (cirruslabs
  tart runner).
- **Lima** (https://lima-vm.io) — Linux VMs on macOS with file sharing; useful
  for Linux host-agent testing without full desktop.
- **Docker Desktop for Mac** — containerised rendezvous + Linux CLI tests;
  cannot test GUI/capture.
- **Multipass** — quick Ubuntu VMs (`multipass launch`); good for receiver
  install scripts.

### 3.2 Linux

- Native on Ubuntu 22.04/24.04/Fedora (bare metal or VM).
- **KVM/QEMU + virt-manager** — primary Linux VM stack; use `qemu-system-x86_64`
  with KVM acceleration. Test X11 vs Wayland sessions by selecting GNOME on
  Xorg vs Wayland at login.
- **GNOME Boxes** — simpler front-end for QEMU VMs.
- **Docker** — rendezvous, CLI, unit tests; use `--network host` for mDNS
  experiments (mDNS across containers is limited).
- **LXD/LXC** — system containers for lightweight multi-distro testing.

### 3.3 Windows

- **Windows Sandbox** (built into Windows 10/11 Pro) — ephemeral desktop for
  installer smoke; enable via `optionalfeatures`.
- **Hyper-V** / **VMware Workstation** / **VirtualBox** — full Windows 10/11
  VMs for capture/input/TCC-equivalent (UAC) testing.
- **GitHub Actions windows-latest** — CI build/test of Go/Python; no GUI
  capture in headless runners.
- **UTM on macOS** can run Windows 11 ARM for quick smoke.

### 3.4 Android

- **Android Studio Emulator** (AVD) — primary guest/host emulation; use API 29+
  images. Hardware acceleration via HAXM/Hypervisor.framework.
- **scrcpy** — mirror/control a physical Android device from desktop.
- **Firebase Test Lab** — remote physical devices for compatibility.
- **Maestro** / **Appium** — UI automation for the Android guest.
- Real-device matrix: at least one Pixel (stock), one Samsung (One UI), one
  Xiaomi (MIUI) for permission/battery quirks.


- **Xcode Simulator** for guest UI only (no host).
- Physical device via TestFlight for real capture/permission flows.

## 4. Network & NAT simulation

- **Linux tc netem**: `sudo tc qdisc add dev eth0 root netem delay 100ms loss 1%`
  (and `tc qdisc del` to clear).
- **macOS Network Link Conditioner** (Hardware IO Tools) — GUI profiles.
- **Clumsy** (Windows) — drop/delay/reorder packets.
- **Toxiproxy** — programmable proxy for latency/timeout injection in
  rendezvous tests.
- **Docker networks** with custom MTU/latency via `tc` in containers.

## 5. Capture & media verification

- **Wireshark/tcpdump** — verify no cleartext media; inspect SRTP/DTLS.
- **ffprobe/ffmpeg** — validate encoded streams when debugging bridges.
- **OBS / platform screen recorders** — reference capture for comparison.
- **GPU codec checks**: `vainfo` (Linux VAAPI), `ffmpeg -hwaccels`.

## 6. Security testing

- `govulncheck ./...`, `gosec ./...` (Go).
- `pip-audit` (Python).
- `cargo audit` if Rust core added later.
- mDNS spoof test: run a rogue `_office-rdp._tcp` advertiser and confirm the
  guest does not auto-trust (requires manual verification + pairing code).
- Kill-app test: `kill -9` on agent → camera/mic processes exit within 1 s
  (watchdog contract).

## 7. CI matrix (planned .github/workflows)

- Go: ubuntu-latest, macos-latest (build + test + vet).
- Python: ubuntu-latest, macos-latest (pytest + pyflakes).
- Rendezvous: docker build smoke.
- Android: gradle build on ubuntu (when android/ added).
- (Future) Windows runner for Go build.

## 8. Acceptance evidence to collect

For each gate, attach: test logs, `doctor --check` output, pcap snippets,
and (for capture) short screen recordings. Store under `docs/evidence/`
(not committed; referenced in PRs).
