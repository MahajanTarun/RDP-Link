# RDP Link → Office RDP — Detailed Development Plan

> **Status:** PLAN ONLY — no code changes in this phase.  
> **Author:** Planning pass, 2026-11-01 (Rechecked 2026-11-01) · Repo: `MahajanTarun/RDP-Link` (v4.5.0)  
> **Goal:** Upgrade the current RDP launcher (FreeRDP + ffmpeg UDP bridges → xrdp host)
> into a full **LAN/WAN Host↔Guest remote-desktop platform** comparable to
> AnyDesk / RustDesk — single app, lightweight, open-source, multi-platform, true bidirectional/multi-device —
> while preserving the existing product's strengths and not regressing them.  
> **Validation:** Current-state cross-checked against all referenced files/lines (README, pyproject, ubuntu_rdp_manager.py, rdp_manager/*, receiver/*, docs/*, .github/workflows/ci.yml, install/build scripts). All constraints below reflect verified repo state.

---

## Table of contents

1. [Executive summary](#1-executive-summary)
2. [Current-state audit](#2-current-state-audit)
3. [Product definition — what "Office RDP" means](#3-product-definition--what-office-rdp-means)
4. [Architecture — target system](#4-architecture--target-system)
5. [Network & connectivity design (LAN/WAN, NAT traversal)](#5-network--connectivity-design-lanwan-nat-traversal)
6. [Identity, pairing & discovery (the AnyDesk-like ID)](#6-identity-pairing--discovery-the-anydesk-like-id)
7. [Transport & media pipeline (desktop + devices)](#7-transport--media-pipeline-desktop--devices)
8. [Security & privacy model](#8-security--privacy-model)
9. [Platform matrix & build strategy](#9-platform-matrix--build-strategy)
10. [Host agent & Guest viewer — single-binary design](#10-host-agent--guest-viewer--single-binary-design)
11. [Protocol & message model](#11-protocol--message-model)
12. [Feature catalogue (prioritised)](#12-feature-catalogue-prioritised)
13. [Data model & persistence](#13-data-model--persistence)
14. [UX / UI plan](#14-ux--ui-plan)
15. [Observability, logs & diagnostics](#15-observability-logs--diagnostics)
16. [Delivery plan — phases, milestones, dependencies](#16-delivery-plan--phases-milestones-dependencies)
17. [Risks, mitigations & open questions](#17-risks-mitigations--open-questions)
18. [Validation & acceptance gates (per-phase)](#18-validation--acceptance-gates-per-phase)
19. [Testing strategy](#19-testing-strategy)
20. [Performance budgets & SLAs](#20-performance-budgets--slas)
21. [Compatibility & migration](#21-compatibility--migration)
22. [Licensing, compliance & supply chain](#22-licensing-compliance--supply-chain)
23. [Cost estimate (infra)](#23-cost-estimate-infra)
24. [Repo layout after the upgrade (proposed)](#24-repo-layout-after-the-upgrade-proposed)
25. [Alternatives considered & why not](#25-alternatives-considered--why-not)
26. [Decision log (to fill during build)](#26-decision-log-to-fill-during-build)
27. [Appendix A — current file map](#appendix-a--current-file-map)
28. [Appendix B — key open-source building blocks](#appendix-b--key-open-source-building-blocks)
29. [Appendix C — quick glossary](#appendix-c--quick-glossary)

---

## 1. Executive summary

### What we have today (verified in repo)

- **Client:** Python + Tk GUI (`rdp_manager/`, ~3.3k LOC Python + 2 shell receivers),
  PyInstaller → `RDP Link.app` (macOS) / single-file `rdp-link` (Linux). Zero
  third-party Python runtime deps.
- **Engine:** FreeRDP (`sdl-freerdp` / `xfreerdp`) launched with `build_args()`
  (`rdp_manager/connection.py:75`) — flags for `/sound`, `/microphone`,
  `/clipboard`, `/drive`, `/kbd:layout:*`, `/cert:tofu`, `/from-stdin`.
- **Device bridges:** ffmpeg → UDP MPEG-TS on `7272` (camera) / `7273` (mic)
  (`rdp_manager/bridges.py:29-33`, `CAMERA_QUALITY`) plus UDP control
  responders on `7274`/`7275` checked by `probe_receiver()` with retry/backoff
  (`bridges.py:45`, `bridges.py:323`).
- **Host side:** two shell receivers (`receiver/camera-receiver.sh`,
  `receiver/mic-receiver.sh`) that install as systemd services over SSH
  (`rdp_manager/deploy.py:18` — `scp` + `ssh` using system binaries), use
  `v4l2loopback` (camera) and `module-pipe-source` (mic).
- **Profiles:** JSON store with legacy migration (`rdp_manager/profiles.py:65`
  `normalize()`), atomic save (`profiles.py:138`), OS keychain / `secret-tool` /
  `0600` file fallback + `hex:` scheme (`rdp_manager/secrets.py:80`).
- **Safety:** parent-death watchdog kills orphan camera/mic within ~1 s
  (`rdp_manager/watchdog.py:50`), FreeRDP itself is intentionally unwrapped.
- **CI:** `pyflakes` + `pytest` + CLI smoke + builds on `macos-latest` /
  `ubuntu-latest` (`.github/workflows/ci.yml:1`).

### What "Office RDP" must add

| Gap vs. brief | Concrete requirement |
|---|---|
| **No self-hosting of the desktop stream** — today we depend on host's `xrdp` (RDP) stack for screen/keys and only bridge A/V ourselves | Own capture→encode→stream path (or FreeRDP *server* side on hosts) so any OS can **host** without pre-installing `xrdp`/VNC |
| **No WAN / NAT traversal** — current host addr is a literal IP/mDNS the user types; bridges are raw UDP | Rendezvous + relay so `ID "483 902 471"` (or `alias.local`) just works across NATs / subnets, with direct P2P when reachable |
| **Windows/macOS/Android host or guest missing** — today: macOS+Linux guests, Linux hosts only | First-class host **and** guest on Linux, macOS, Windows, Android (guest-first on mobile) |
| **Insecure A/V path** — MPEG-TS over plain UDP (`docs/SECURITY.md:37`) | Encrypted, authenticated media; LAN-scoped firewall is not enough |
| **No conferencing primitives** — single viewer, no audio mixing, no multi-guest | Multi-viewer, selective input handoff, in-session chat/file drop, optional "meeting" layout |
| **Manual pairing** — IP + creds | One-tap unattended pairing + consent-based attended sessions |

### Design stance (principles)

1. **Keep the thin-launcher DNA** — the app must still feel instant; connect path
   never blocks.
2. **Reuse FreeRDP where it wins** (RDP encode path, codec negotiation,
   clipboard/drive channel patterns). Don't reimplement RDP from scratch unless
   forced by platform.
3. **Prefer proven OSS transport** — DTLS-SRTP (WebRTC) or SRT/QUIC for media;
   WireGuard/Noise-style handshake for signalling — not a bespoke crypto protocol.
4. **Single binary / single installer per OS** — same artifact is host, guest,
   or both (role toggled in-app). Parity with the current `guest`/`host`/`both`
   installer (`install.sh:21`).
5. **Offline-first LAN still works** — mDNS + direct UDP must succeed with **no
   internet**.

---

## 2. Current-state audit

### 2.1 What is solid (keep / extend)

- `profiles.py` — `normalize()` + atomic `save()` + `DEFAULTS`; migration
  pattern generalises to new `peerProfiles` (see §13).
- `secrets.py` — `hex:` scheme + multi-backend (`security`/`secret-tool`/file);
  directly reusable for pairing tokens + per-peer keys.
- `exe.py` — `find_exe()` + known prefixes; extend to locate capture backends.
- `connection.py` — `build_args()`/`shell_quote_cmd()` pattern and
  `_terminate()` escalation (`connection.py:278`); `HeadlessApp` + `_HeadlessRoot`
  for headless/CLI mode (`connection.py:294`/`connection.py:333`).
- `bridges.py` — health-monitor shape (`schedule_health` → `_monitor` →
  `_probe_async` off-Tk thread → `_apply_handshake`) is the right model for
  all link health.
- `watchdog.py` — parent-death guarantee must stay for any camera/mic path.
- `deploy.py` — visible-terminal `scp`/`ssh` install (no SSH lib) — keep as the
  zero-deps LAN provisioning path.
- `docs/` — `INSTALL.md`/`SECURITY.md`/`SAME-SCREEN.md`/`TROUBLESHOOTING.md`
  discipline — carry forward.

### 2.2 What must change

| Area | Current | Target |
|---|---|---|
| Desktop stream | Delegated to `xrdp` via FreeRDP client | In-app capture (platform backends) + encode (H.264/H.265/AV1/VP9) + own transport; FreeRDP retained as **one** host option on Linux, not the only one |
| A/V path | `ffmpeg` → clear `mpegts` UDP (`bridges.py:290`) | Encrypted RTP/SRTP; audio via platform capture + Opus; camera via virtual device only where needed |
| Host addressing | User-typed `host:port` (`profiles.py:43` default `192.168.1.10`) | `peerId` (numeric) + `alias.local` + raw addr fallback; rendezvous resolves |
| Connectivity | Assumes LAN reachability; firewall tip in `docs/SECURITY.md:44` | ICE (STUN/TURN), relay fallback, LAN mDNS fast-path, UPnP optional |
| Platform | Host = Linux only (`install.sh:112`) | Host+Guest everywhere (§9) |
| Multi-peer | `ConnectionController.processes` is 1:1 per profile | Room with N viewers / 1+ presenters; input token model |
| Auth | Per-profile password via `/from-stdin` | Pairing certs + per-session consent + optional org SSO |

### 2.3 Hard constraints inherited

- Python/Tk app is ~45 MB bundled; Android cannot run that — mobile needs a
  native shell (see §9).
- Camera/mic must never orphan — any new capture path inherits `watchdog.py`'s
  contract.
- CI already covers `pyflakes` + `pytest` + CLI smoke + both builds — extend,
  don't replace.

---

## 3. Product definition — what "Office RDP" means

### 3.1 Roles

- **Host** — machine whose desktop/devices are shared. Runs a background agent
  (even with no viewer).
- **Guest** — viewer/controller. Can observe, request/release input, chat,
  transfer files, use its own mic/cam inside the remote session (exactly the
  current Teams/Zoom-inside-RDP use case, generalised).
- **Both** — Office default. Every install is capable of both roles; the user
  toggles "Allow remote access" and sets policy.

### 3.2 Session types (map to the brief's "all kinds of usage")

1. **Remote control** — single guest drives host (AnyDesk default). Clipboard +
   drive redirect + audio, optional cam/mic injection. This is the current
   product, freed from `xrdp`.
2. **View-only / shadow** — coach mode, no input, optional curtain (host sees
   indicator but keeps control).
3. **Multi-guest** — e.g. 3 devs watching a host for pair-debug / deploy watch.
   One input holder at a time (token), others observe + chat.
4. **Meeting over RDP** — host shares desktop + host-mic/cam in one window; guests
   inject their A/V as remote devices (same receiver-virtual-device model, now
   cross-platform). Not trying to out-Zoom Zoom — but good enough for daily
   stand-ups, code reviews, and test witnessing.
5. **File / clipboard / print** — drag-drop file transfer (bidirectional),
   shared clipboard, optional printer pass-through.
6. **Unattended vs. attended** — unattended = paired device can connect when
   host is locked (with policy); attended = host must click Accept and can set
   permission scope.

### 3.3 Non-goals for v1 (explicit)

- Full WAN meeting SFU with 50 participants, breakout rooms, recording — out of
  scope. Cap at ~6 concurrent viewers per host.
- Remote printing via IPP bridge — defer.
- Wayland fractional-scale perfection on day one — X11 + basic Wayland first,
  then polish.

---

## 4. Architecture — target system

### 4.1 High-level view

```
                          ┌─────────────────────┐
                          │  Rendezvous / Relay  │
   Host A  ──DTLS/SRTP────►│  (self-hostable)    │◄────DTLS/SRTP──  Guest B
   Host B  ──(ICE)────────►│  STUN + TURN        │◄────(ICE)──────  Guest C
                          │  Registry (IDs)     │
                          └─────────────────────┘
                                    ▲
           LAN fast-path (direct):  mDNS + ICE host candidates (no relay)
```

Three deploy shapes:

- **Zero-infra LAN** — no rendezvous needed; mDNS (`.local`) + direct ICE.
  This is the default for office LAN and matches `mcitlptp810.local` usage today.
- **With org rendezvous** — lightweight Go/Rust service holding registry,
  STUN/TURN, and optional TURN relay. Teams run one per site or in cloud.
  Open-source; single binary + Docker.
- **Managed relay (fallback)** — public relay operated by the project for WAN
  when no org server is configured. Rate-limited; self-hostable.

### 4.2 Host process model

```
┌─────────────────────────────────────────────────────────┐
│  office-rdp agent (one process, or service + helper)    │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  ┌────────┐ │
│  │ capture  │─►│ encode   │─►│ transport │─►│ input  │ │
│  │ backends │  │(H.264/AV1│  │(WebRTC/QUIC│  │inject) │ │
│  └──────────┘  └──────────┘  └───────────┘  └────────┘ │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐             │
│  │ audio    │  │ camera   │  │ file/cb   │  policy     │
│  │ capture  │  │ vdev     │  │ channels  │  engine     │
│  └──────────┘  └──────────┘  └───────────┘             │
└─────────────────────────────────────────────────────────┘
   signals consent UI (attended) via desktop helper / tray
```

- **Linux:** agent as systemd `--user` + optional system helper for
  input-injection / display capture permissions. Capture via PipeWire portal
  (preferred), X11 `XDamage`/`XShm`, or DRM/KMS. Audio via PipeWire/Pulse.
- **macOS:** agent as LaunchAgent/daemon; capture via `ScreenCaptureKit`
  (macOS 12.3+), input via `CGEvent`, audio via `CoreAudio` tap / virtual device,
  permissions via `TCC` prompts.
- **Windows:** service + tray helper; capture via `Windows Graphics Capture`
  / DXGI Desktop Duplication, input via `SendInput`, audio via WASAPI loopback.
- **Android:** full host+guest support from v1 (API 29+ recommended; MediaProjection available API 21+). Host uses `MediaProjection` foreground service, `AccessibilityService` / `InputManager` for injection, system audio/camera/mic capture as permitted; guest uses native viewer. Both roles in single APK with role toggles and multi-session/multi-connection support.

### 4.3 Guest process model

- Native viewer window (SDL2 / platform toolkit) with overlay controls — **not**
  Tk for the live view (Tk stays for launcher/profile UI, bridged).
- Decode via platform HW (VideoToolbox / MediaCodec / D3D11VA / VAAPI / NVDEC)
  with SW fallback.
- Input forwarding with keymap normalisation (reuse `keyboard.py` model).
- Local preview of own cam/mic before injection.

### 4.4 Why not just "FreeRDP server on every host"?

- FreeRDP server (`freerdp-shadow-cli`/`xfreerdp-server`) could be a **Linux**
  host path, but Windows/macOS/Android don't have a usable FreeRDP server
  story and we would still need ICE/relay + A/V + pairing ourselves.
- **Decision:** keep FreeRDP **client** for the current RDP-to-xrdp profile
  type (compat profile), but the primary host path is a direct capture/encode
  pipeline shared across all OSes. This lets us ship Windows/macOS hosts without
  waiting on xrdp ports.

---

## 5. Network & connectivity design (LAN/WAN, NAT traversal)

### 5.1 Discovery & addressing (the `mcitlptp810.local` story)

Today the app stores `host=mcitlptp810.local` or `192.168.99.2` literally.
Keep all three forms, add one more:

| Addr form | Example | How it resolves |
|---|---|---|
| `peerId` | `483 902 471` | Rendezvous registry → ICE candidates |
| `alias.local` | `mcitlptp810.local` | mDNS (Avahi/Bonjour) → LAN IP |
| Hostname / mDNS | `mcitlptp810` | DNS/mDNS |
| Raw IP | `192.168.99.2:3389` | Direct (compat profile) |

The current `profiles.json` `host` field becomes `target` (string) plus a
discriminated `targetKind: "peerId" | "mdns" | "ip" | "legacyRdp"` so old
profiles deserialize unchanged.

### 5.2 ICE / STUN / TURN

- Use a WebRTC-style ICE stack (e.g. `pion/ice` in Go or `libjuice`/`libnice`
  in native) — gather host, srflx (STUN), relay (TURN) candidates, trickle ICE
  over the signalling channel.
- STUN: any public STUN + embedded STUN in rendezvous.
- TURN: `coturn` or built-in TURN in rendezvous. Relay is **metered**: direct
  host candidate wins when reachable; TURN only when both behind symmetric NAT.
- LAN optimisation: mDNS candidates advertised immediately; don't wait for
  STUN on `.local` targets.

### 5.3 Signalling channel

- WebSocket (TLS) to rendezvous when online; fallback to local mDNS + raw UDP
  hole-punch when offline.
- Signalling carries: SDP offer/answer (or equivalent for non-WebRTC transport),
  ICE candidates, pairing handshake, capability negotiation.
- No desktop pixels ever touch the signalling path.

### 5.4 Relay policy

- **Default:** relay through the org rendezvous if configured, else managed public
  relay with per-IP caps.
- **Privacy:** relay is pass-through (DTLS-encrypted); it cannot decrypt media.
- **Cost control:** byte caps + TURN `ALLOCATE` lifetime tuning; LAN peers never
  relay.

### 5.5 Firewall / ports (what changes for users)

- Today: `7272:7275/udp` on host (`receiver/*.sh` + `docs/SECURITY.md:44`).
  Target: **one** UDP port range per host (default `3478/udp` for STUN/TURN +
  dynamic ephemeral for media). LAN `.local` path needs no firewall change
  beyond existing office policy.
- Provide `office-rdp doctor --check` (successor to `receiver/*.sh --check`)
  that reports capture backend, firewall, STUN reachability, relay health.

---

## 6. Identity, pairing & discovery (the AnyDesk-like ID)

### 6.1 Peer identity

- On first launch the agent generates an **Ed25519 keypair** (device identity)
  stored in OS keychain (`Keychain` / `libsecret` / `DPAPI` / `Keystore`);
  file fallback `0600` JSON encrypted with OS-provided key — same backends as
  `secrets.py`.
- **Peer ID:** 9-digit numeric derived from public-key fingerprint
  (deterministic, collision-checked against registry). Spaced `XXX XXX XXX` for
  dictation. This is the AnyDesk-like number the brief asks for.
- **Alias:** user-chosen short name (`mcitlptp810`) published via mDNS + registry
  (unique per org when using rendezvous).

### 6.2 Pairing modes

1. **One-time code** — new guest shows a 6-digit code; host enters it (or scans
   QR on desktop/mobile) → both exchange public keys, store pairing cert.
2. **Attended consent** — guest enters host's Peer ID; host sees toast
   "Guest X wants to connect — Allow / Allow once / Deny"; permission scope
   chosen inline.
3. **Unattended** — after pairing, guest can connect when host is locked;
   requires host policy "Allow unattended" + OS unlock / pairing password.
   Host owner can revoke pairings at any time.
4. **Org pre-pair** — admin pushes pairings / policies from rendezvous (for
   fleet of office PCs). Phase 2.

### 6.3 Presence & directory

- Host advertises `online | away | busy | offline` + alias to rendezvous
  (heartbeat, not polling).
- LAN directory: mDNS `_office-rdp._tcp` service browsers in the app show
  nearby hosts without a server.
- No global public directory — Peer IDs are not enumerable (rate-limited
  lookup + proof-of-work or pairing code gating).

---

## 7. Transport & media pipeline (desktop + devices)

### 7.1 Desktop video

- **Capture:** platform backends (priority order):
  - Linux: PipeWire portal (`xdg-desktop-portal`) → X11 → DRM/KMS fallback.
  - macOS: `ScreenCaptureKit`.
  - Windows: `Windows Graphics Capture` → DXGI Duplication fallback.
  - Android (host, Phase 4): `MediaProjection`.
- **Encode:** H.264 baseline/high (universal HW), H.265/AV1 where
  negotiated (better bitrate). Temporal scalability for low-latency.
- **Packetise:** RTP over DTLS-SRTP (WebRTC stack) **or** SRT/QUIC — decide in
  Phase 0 spike (see §18). WebRTC is favoured for NAT/ICE maturity + Android
  browser interop.
- **Adaptation:** congestion control (GCC / BWE), resolution/quality ladder,
  pause-on-minimise, cursor-only updates option.

This replaces the current "delegate to `xrdp` and get pixels over RDP" arc
(`connection.py:82` `format_host()` / `build_args()`). Compat RDP profiles
remain for existing `xrdp` fleets.

### 7.2 Audio (both directions)

- **Host → Guest** (remote desktop's audio): host captures system mix via
  PipeWire/Pulse / WASAPI loopback / CoreAudio — guest plays locally.
  (Supersedes today's `/sound` flag — `profiles.py:24` `audio`.)
- **Guest → Host** (guest's mic inside remote session, e.g. Teams inside RDP):
  guest captures mic (Opus), host injects as virtual input:
  `MacMic`-style virtual source on Linux, virtual mic driver / WASAPI input on
  Windows, `CoreAudio` aggregate on macOS. Reuse `receiver/mic-receiver.sh`'s
  `module-pipe-source` pattern (`mic-receiver.sh:172`) as the Linux v1, then
  generalise.
- **Echo & mixing:** Opus + AEC on guest side; host side mixes with low latency
  (no double loopback).

### 7.3 Camera

- Host → Guest: not needed for remote control, but host can publish its cam to
  guests (meeting mode).
- Guest → Host (the current `camera bridge`): guest streams camera as host
  virtual webcam (`v4l2loopback` on Linux — `camera-receiver.sh:26` `MacCamera`,
  `exclusive_caps=1` requirement `camera-receiver.sh:84`), virtual camera driver
  on Windows/macOS. H.264 for camera too (today's `CAMERA_QUALITY` in
  `bridges.py:37` becomes encoder presets).
- **Encrypted:** no more `udp://host:7272` cleartext (`bridges.py:287`).
  Camera/mic ride the same DTLS-SRTP session as desktop, with separate SSRCs.

### 7.4 Clipboard, files, input

- Clipboard: text + image + file-list sync over reliable data channel; same
  toggle as today (`profiles.py:35` `clipboard`/`fileTransfer`).
- Files: drag-drop / file picker → chunked transfer over data channel with
  resume; progress UI. Replaces `/drive:MacFiles` (`connection.py:97`) with
  scoped per-transfer grants (no "entire `$HOME` exposed" — `docs/SECURITY.md:59`).
- Input: key events with layout mapping (keep `keyboard.py`'s `resolve_for_connect`
  model; extend to Wayland/Windows), pointer + scroll (keep `scroll.py`'s
  `apply_for_connect` / `mouse_scroll.py` logic), multi-monitor awareness.
- Privacy controls: per-session toggles for clipboard/files/input/camera/mic;
  host can revoke mid-session.

---

## 8. Security & privacy model

### 8.1 Threat model

Assume LAN is **not** trusted (sniffable open WiFi in office is common). Relay
operator is **not** trusted with plaintext. Attacker may spoof mDNS on LAN.

### 8.2 Guarantees

| Property | Mechanism |
|---|---|
| Media confidentiality & integrity | DTLS 1.3 (or Noise_IK) → SRTP; keys never touch relay; PFS |
| Peer authentication | Ed25519 device keys; pairing certs pin host key; TOFU only for first attended session, then pinned |
| mDNS spoof resistance | mDNS advertises ID + key fingerprint; guest verifies fingerprint out-of-band (first pairing code) or via rendezvous lookup; no auto-trust on `.local` alone |
| No orphan camera/mic | `watchdog.py` contract extended to all capture paths; `watchdog.run()` (`watchdog.py:69`) stays as the parent-death wrapper for any process touching hardware |
| Password handling | Keep `secrets.py` `hex:` + Keychain/libsecret/file pattern; pairing tokens same store |
| Least privilege on host | Agent is `user`-level; system helper (if any) is minimal and capability-scoped; no permanent root daemon for desktop share on Linux |

### 8.3 What changes from `docs/SECURITY.md`

- "Bridges are unencrypted MPEG-TS over UDP" (`SECURITY.md:37`) → encrypted SRTP;
  that warning and the `ufw from <client-ip>` mitigation (`SECURITY.md:44`) become
  historical.
- "No telemetry, no network calls beyond your host + bridges + handshake"
  (`SECURITY.md:64`) → add rendezvous/STUN/TURN endpoints (documented, opt-in
  self-host; offline LAN still zero-calls).
- `FIREWALL.md` / `docs/SECURITY.md` must document new port(s) + "scoped TURN" guidance.

### 8.4 Hardening checklist (Phase 1)

- [ ] DTLS/SRTP on all media, data channel over DTLS.
- [ ] Host shows persistent "🔴 Streaming to X" overlay + tray icon while any
  viewer is connected.
- [ ] Guest's camera/mic indicators and "Stop streaming" match current
  `device_chips()` (`bridges.py:215`) + `stop_device_streaming()` (`ui/app.py:350`) parity.
- [ ] Pairing revocation + session audit log (who connected, when, permissions).
- [ ] `/drive` whole-home exposure removed; scoped file transfer only.

---

## 9. Platform matrix & build strategy

### 9.1 Coverage

| OS | Host (share desktop) | Guest (view/control) | Notes |
|---|---|---|---|
| **Linux (Ubuntu 22.04+/24.04+, Fedora, Arch)** | ✅ v1 | ✅ v1 | X11 first, Wayland portal next; VAAPI/NVENC |
| **macOS 11+ (Intel + Apple Silicon)** | ✅ v1 | ✅ v1 | ScreenCaptureKit ≥12.3, TCC prompts; universal2 binary |
| **Windows 10+ (x64, arm64 where feasible)** | ✅ v1 | ✅ v1 | WGC + DXGI; MSIX + portable zip |
| **Android 10+ (API 29+)** | ✅ v1 (host+guest) | ✅ v1 (guest+host) | Single APK (universal + splits). Foreground service (MediaProjection), AccessibilityService/Input APIs for injection, virtual A/V, multi-session/multi-connection, hardware decode/encode where available |

Today is macOS+Linux guest / Linux host — this is the biggest expansion.

### 9.2 Implementation language decision

The current app is Python+Tk+shell. Keeping that while adding cross-platform
capture, HW encode, ICE, and Android is not viable long-term. Options:

**Recommended: Rust core + thin per-OS shell**

- Core crate(s): capture/encode/transport/signalling/protocol/policy.
- Per-OS adapters: small Swift / Kotlin / C# (or Rust FFI) shims for
  capture/input/permissions/tray.
- **Transport plugin model (learn from Remmina):** structure transports as plugins
  — `transport/legacy_rdp` (FreeRDP wrapper for `legacyRdp` profiles), `transport/native_p2p`
  (DTLS-SRTP/ICE for new Office transport), with a common trait for negotiation/features.
- Launcher/profile UI: keep Python/Tk for desktop v1 (low churn), but plan a
  migration to a shared UI (Tauri / Flutter / native) in Phase 3. Android guest/host
  in Kotlin + Rust core via JNI.
- **UX borrowings (Remmina):** groups, tabs, recent/search, tray, keystrokes,
  per-connection view modes, fullscreen, scale quality, view-only — adapt to our launcher.

Rationale: Rust gives us memory safety, small binaries, strong WebRTC/QUIC
ecosystem (`webrtc-rs`, `quinn`, `libjuice` bindings), and Android NDK support.
Go is the alternative (strong `pion/webrtc` + `coturn`-like relay) but Windows
service + macOS TCC ergonomics are weaker and binary size is larger.

**Spike task (Phase 0):** build a 2-day prototype of "capture → H.264 → RTP"
in both Rust+`webrtc-rs` and Go+`pion/webrtc` to pick before committing. Also
prototype `legacy_rdp` plugin wrapper to ensure FreeRDP parity toggles remain.

### 9.3 Packaging (single app, minimal install)

| Platform | Artifact(s) | Install Method | Notes |
|---|---|---|---|
| macOS | `OfficeRDP-<ver>-universal.dmg` containing `Office RDP.app`, `office-rdp` CLI, and `office-rdp-agent` LaunchAgent helper | Drag to `/Applications`; optional `brew install --cask officerdp` | Universal2 (x86_64 + arm64), notarized, TCC permissions declared, LaunchAgent auto-start for host |
| Linux | `.deb` (APT), `.rpm` (DNF/YUM), `OfficeRDP-<ver>-x86_64.AppImage`, `office-rdp_<ver>_amd64.tar.gz` (portable), AUR `officerdp` | `sudo dpkg -i *.deb` or `sudo rpm -i *.rpm` or run AppImage (chmod +x) | Single binary with `office-rdp`, `office-rdp-agent` (systemd --user), `office-rdp-cli`. Works on X11/Wayland. Supports headless. |
| Windows | `OfficeRDP-Setup-<ver>.exe` (NSIS installer), `OfficeRDP-<ver>-portable.zip`, `OfficeRDP-<ver>.msix` (optional) | Run installer (per-user or system), extract portable zip, or install MSIX | Registers background agent as user service/helper, WGC/DXGI capture, works for both host+guest. No admin required for normal use. |
| Android | `OfficeRDP-<ver>.apk` (universal or split ABIs), `OfficeRDP-<ver>.aab` (Play Store), F-Droid build variant | Sideload APK, install from F-Droid, or distribute AAB | Single app for host and guest. Foreground service for host (MediaProjection), AccessibilityService for input injection, full A/V, multi-session, multi-device. Supports Android 10+. |

All three desktop artifacts contain host agent + guest viewer; role is a
toggle ("Enable incoming connections"). Matches `install.sh`'s `guest`/`host`/`both`
ergonomics (`install.sh:21`) but now truly single-binary per OS.

---

## 10. Host agent & Guest viewer — single-binary design

### 10.1 Binary & service shape

- Desktop: one executable `office-rdp` that can run as:
  - `office-rdp` (launcher GUI — current `ubuntu_rdp_manager.py` entry point),
  - `office-rdp --agent` (headless host daemon, auto-started at login),
  - `office-rdp --connect <id|alias>` (headless guest, like `rdp-link --connect`
    `ubuntu_rdp_manager.py:34`).
- Background agent: `LaunchAgent` / `systemd --user` / Windows service (helper)
  — not root on Unix.
- Tray/menu-bar icon shows host state (online/idle/in-session) and quick
  "Copy my ID" / "Pause incoming".

### 10.2 Capability negotiation per connection

On signalling, peers exchange a `Hello` with: version, OS, HW codecs,
max viewers, policy (who may request input/clipboard/files). Host is
authoritative; guest requests, host grants. Guest cannot escalate.

### 10.3 Compat shims (don't break today's fleet)

- Keep `rdp_manager/connection.py:build_args()` + `deploy.py:setup_command()`
  + `receiver/*.sh` **as-is** behind a profile flag `targetKind == "legacyRdp"`.
  Old profiles keep working to `xrdp` hosts via FreeRDP as today; new profiles
  use the native transport. The app shows both in one list.
- `camera-receiver.sh --install` / `mic-receiver.sh --install` remain valid for
  legacy RDP hosts; native mode does not need them.

---

## 11. Protocol & message model

Versioned binary framing over DTLS data channel + JSON signalling.

```
Signalling (WebSocket/TLS or mDNS):
  Hello { peerId, alias, version, caps{capture, codecs[], maxViewers}, policy }
  Offer / Answer { sdpOrEquivalent, iceCandidates[] }
  PairRequest { code|peerId, requestedPerms }
  PairGrant   { cert, perms, expiry }
  Presence    { status }

Media (SRTP):
  Video  SSRC 1..N  (desktop + optional cam tiles)
  Audio  SSRC      (Opus, host→guest + guest→host as separate SSRCs)

Data channel (reliable, ordered):
  InputEvent   { key|pointer|scroll, seq }
  Clipboard   { mime, bytes }          // gated by policy
  FileChunk   { transferId, offset, bytes, eof }
  ChatMessage { text }                 // optional, per-session
  Control     { requestInput | releaseInput | toggle{cam,mic,clip,files} }
  HealthPing  { rttProbe }             // successor to RDPCTL1 PING/PONG
```

All data-channel messages are authenticated (DTLS) + host-authorised.
Clipboard/file transfers are per-message consent when policy is "ask".

---

## 12. Feature catalogue (prioritised)

### P0 — must ship with first Office RDP release (Phase 1)

- **Single install, all roles:** Host+Guest on Linux/macOS/Windows and **Android (single APK)**. True bidirectional and multi-device/multi-connection (N guests ↔ N hosts).
- Peer ID + alias/mDNS discovery + LAN zero-infra works offline (no internet required).
- Remote control with input-token per session + DTLS-SRTP video+audio.
- Bidirectional A/V: guest mic/cam injection into host session and host A/V to guests (meeting use case).
- Clipboard sync (text/image) + bidirectional file transfer (scoped, per-transfer consent; no whole-home exposure).
- Capture v1 on all platforms: X11/Wayland/PipeWire, ScreenCaptureKit, WGC/DXGI, **Android MediaProjection**.
- Consent + unattended pairing; per-session permission toggles; revocation + session audit.
- Rendezvous self-host (one binary + Docker) with STUN/TURN; LAN direct/mDNS preferred.
- `legacyRdp` compat — existing `xrdp` fleet still works via FreeRDP (with Remmina-inspired toggles: gateway/RD Gateway, drive/share folder, sound/mic, printers/serial/smartcard, console attach, fast-path, glyph cache, ignore-cert, auto-reconnect). SSH tunnel option for legacy RDP.

### P1 — v1.1 (Phase 2)

- Multi-guest (up to 6 viewers, 1 input holder / token passing).
- Printer pass-through (IPP), shared folder mapping (opt-in).
- Org policy push from rendezvous (device groups, allowed peers, session recording policy).
- Wayland polish (fractional scale, portal picker UX).
- Windows arm64, Linux AppImage signing, macOS notarisation hardening.
- TURN bandwidth quotas + audit log export.

### P2 — later

- Session recording (host-side, consent-gated).
- Android host (MediaProjection + a11y input).
- Wake-on-LAN integration for office PCs.

---

## 13. Data model & persistence

### 13.1 Profile store evolution

Current `profiles.json` (`paths.py:39` `PROFILES_FILE`) holds flat RDP profiles
(`profiles.py:22` `DEFAULTS`, `profiles.py:42` `default_profiles()`). New model:

```
profiles.json  (legacy RDP — kept as-is, migrated in place)
peers.json     (new — paired peers + known IDs + aliases + last-seen)
settings.json  (new — global: hostEnabled, alias, rendezvousUrl, policy)
```

`peers.json` entry:
```json
{
  "peerId": "483 902 471",
  "alias": "mcitlptp810",
  "displayName": "Tarun — MolCon Wired",
  "targetKind": "peerId",
  "rawTarget": "483 902 471",
  "pairing": { "state": "paired|pending|blocked", "certFp": "…" },
  "caps": { "codecs": ["h264"], "maxViewers": 4 },
  "perms": { "input": "ask", "clipboard": true, "files": "ask" },
  "lastSeen": "2026-11-01T…"
}
```

Migration: keep `ProfileStore` (`profiles.py:116`) for legacy RDP entries;
add `PeerStore` with identical atomic-save / `normalize()` discipline
(`profiles.py:138`). Both appear in one unified list in the launcher.

### 13.2 Secrets

Reuse `secrets.py` (`secrets.py:80` `store_set` + `hex:` + multi-backend) for:
- device Ed25519 private key,
- per-peer pairing certs,
- unattended access password (hashed with Argon2, never raw).

---

## 14. UX / UI plan

### 14.1 Launcher (evolves current `ui/app.py`)

Keep the current table (`ui/app.py:110` columns `name|target|routing|state`) but:

- Row = union of legacy RDP profiles + paired peers (badged `RDP` vs `Office`).
- New header: "My ID: `483 902 471`  ·  `mcitlptp810.local`  [Copy]" — one click.
- Status: `● Connected (guest has input)` / `○ Idle` / `⇄ Connecting…` + RTT.
- Device chips stay (`bridges.py:215` `device_chips()` parity: 🎥 🎤) plus
  `🔴 Live` when hosting.
- Right-click: Connect / View-only / Request input / Files / Stop A/V /
  Show log / Pairing / Block / Copy ID — superset of today's menu (`ui/app.py:132`).
- Add groups, tabs, recent list with search, tray icon, double-click config/connect
  (Remmina-inspired). Keep "thin launcher" feel.

### 14.2 Session window

- Frameless viewer with auto-hiding toolbar: input toggle, quality (Auto/HD/Low),
  display picker (multi-monitor), fullscreen, chat drawer, file-drop zone,
  camera/mic toggles, "Stop streaming" (parity with `ui/app.py:350`). Add scale
  quality, view-only, grab keyboard (host key), keystrokes, screenshot (configurable
  folder/filename, prevent clipboard capture). Borrow from Remmina UX.
- Host consent toast (attended): who is asking + permission scope + Allow/Deny
  with "Remember".

### 14.3 Android guest

- Native viewer (Kotlin + `SurfaceView`/`TextureView` for HW decode).
- Touch → pointer/key mapping, on-screen kb toggle, pinch zoom, file picker
  bridge, mic/cam inject toggles.

### 14.4 Minimalism rule

Zero mandatory setup wizard. Install → alias prefilled from hostname →
toggle "Allow incoming" → done. Pairing code is shown inline, not a modal maze.
This honours "On-The-Go" in the brief.

---

## 15. Observability, logs & diagnostics

### 15.1 What stays, what evolves

- Keep `logutil.py` (`logutil.py:35` `current_log_path()` + `logutil.cleanup()`
  rotation of 10) as the session log for the launcher; add structured JSON lines
  for transport/ICE events alongside FreeRDP/bridge stderr.
- `receiver/*.sh --check` diagnostics (`camera-receiver.sh:104` `check()`,
  `mic-receiver.sh:73` `check()`) become `office-rdp doctor --check`:
  capture backend, display server, GPU codecs, firewall ports, STUN reachability,
  relay health, last session RTT/loss, TCC grants.

### 15.2 Metrics (local + optional push)

- Per-session: RTT, jitter, packet loss, encode/decode ms, bitrate, dropped
  frames, codec, path (direct vs relay), bytes relayed.
- Shown in-app (overlay + session log), not silently collected. Opt-in anonymised
  health pings to rendezvous only.

---

## 16. Delivery plan — phases, milestones, dependencies

> All durations are engineering-time estimates for a 2-person core team; scale
> with headcount. No phase starts coding until its **gate** ( §18 ) is green.

### Phase 0 — Spike & scaffolding (2 weeks)

| Task | Owner | Verifiable output |
|---|---|---|
| Transport spike: WebRTC (pion/webrtc or webrtc-rs) vs SRT vs QUIC for "capture→encode→RTP" on Linux→Linux | Eng | Benchmark doc + choice (latency, NAT, binary size, Android interop) with numbers |
| Capture spike: one backend per OS (PipeWire portal, ScreenCaptureKit, WGC/DXGI) — just pixels to window, no crypto | Eng | Demo app per OS streaming to local viewer |
| Repo surgery: introduce `core/` (Rust) + `app/` (Python/Tk shim) + `rendezvous/` (Go/Rust) without breaking current build; CI stays green | Eng | PR: new layout, CI matrix extended, `pyinstaller` still produces working `RDP Link` |
| Spec freeze: protocol v1, pairing flow, ID format, mDNS service name, port plan | Both | This file §11 + §6 locked; no drift without ADR |

**Depends on:** nothing. **Blocks:** everything.

### Phase 1 — Office RDP v1 (P0) (10–12 weeks after Phase 0 gates)

1. **Core transport + signalling** (wks 1–4)
   - DTLS-SRTP + ICE (or chosen equivalent) with trickle.
   - WS signalling to rendezvous + mDNS LAN signalling.
   - Rendezvous v1: registry, presence, STUN, TURN relay (single binary).
   - Unit tests for ICE failover (direct → relay) with mocked NATs.
2. **Host agent** (wks 2–6, overlaps 1)
   - Capture backends + H.264 encode + RTP out; tray/menu-bar agent.
   - Audio capture (host mix) + virtual mic/cam injection paths per OS.
   - Permission flows (TCC prompts, portal dialogs) — tested, not silent.
3. **Guest viewer** (wks 4–7)
   - SDL2 / native viewer window, HW decode, input forward (keep `keyboard.py`/
     `scroll.py` mapping model), clipboard+file channels, quality ladder.
   - Android guest (Kotlin shell + core via JNI) streams and controls.
4. **Pairing, IDs & LAN directory** (wks 5–7)
   - Ed25519 identity, 9-digit Peer ID, alias, mDNS `_office-rdp._tcp`.
   - Code/QR pairing + consent toast + unattended policy + revocation.
5. **Packaging & polish** (wks 8–10)
   - `.deb/.rpm/.AppImage`/`.app`/`.exe`/`.apk` from CI; `install.sh` successor
     (`office-rdp-install.sh guest|host|both`).
   - `doctor --check`, unified launcher list (legacy RDP + Office peers),
     headless `office-rdp --connect <id>`.

**Milestone gate:** office dog-food — 5 hosts (mix Linux/macOS/Windows) and 10
guests (incl. 2 Android) on office LAN + 2 homes over WAN/relay, 1 week no
major regression vs. current RDP Link for daily work. See §18 gates.

**Depends on:** Phase 0. **Blocks:** Phase 2.

### Phase 2 — Multi-guest & org (P1) (6–8 weeks)

- SFU-lite or mesh for ≤6 viewers (relay-assisted if needed), token-passing input.
- Org rendezvous: device groups, policy push, audit log export, quotas.
- Printer/IPP, folder mapping (scoped), Wayland fractional-scale polish.
- Notarisation / signing hardening, Store-readiness for Android.

### Phase 3 — Hardening & extras (ongoing)

- UI migration off Tk (if warranted by metrics) — Tauri/Flutter/native.
- Fuzzing + third-party audit of crypto/ICE path.

---

## 17. Risks, mitigations & open questions

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | Wayland capture fragmentation (portal vs compositor) | High | Med | PipeWire portal first; X11 compat as fallback; per-distro QA matrix |
| R2 | macOS ScreenCaptureKit prompt fatigue / TCC | Med | Med | Request only on first host-enable; explain in UI; keep `permissions.py`-style cache |
| R3 | Windows service + input injection (UAC/UIPI) | Med | High | Helper process at user session level for input; service only for keep-alive; test on Win 10/11 + domain-joined |
| R4 | Relay cost at scale | Med | Med | Caps, prefer direct, self-host default for office; public relay is fallback, not primary |
| R5 | Codec mismatch (HW encode missing on old HW) | Med | Low | H.264 baseline SW fallback (`openh264`); negotiate down |
| R6 | NAT traversal failure on strict corp firewall | Med | High | TURN over TLS/443 as last resort; document firewall allowlist; LAN still works |
| R7 | Security regression vs current thin launcher | Med | High | No cleartext media ever (unlike `bridges.py:287` MPEG-TS); audit + "🔴 Live" indicator; watchdog for all cam/mic |
| R8 | Scope creep (meeting vs RDP) | High | High | Cap viewers at 6; no breakout/record in v1; reuse OS meeting apps inside session where possible |
| R9 | Android host constraints (foreground service, AccessibilityService, battery) | Med | Med | Design for foreground service + notification, request permissions explicitly, test on OEMs; already planned for P0 with clear UX and battery/Doze guidance |
| R10 | Python→Rust migration churn | Med | Med | Keep Python launcher in Phase 1; core in Rust behind FFI; no big-bang rewrite |

### Open questions (must be answered in Phase 0)

1. **Transport choice confirmation** — WebRTC vs raw DTLS-SRTP vs QUIC/SRT.
   Criteria: ICE maturity, Android/browser interop, binary size, TURN fallback.
2. **Exact rendezvous hosting story** — does the org run one per site or one global?
   Data-residency requirements? (Determines relay routing.)
3. **Unattended access policy** — org default? Need SSO (OIDC) in v1 or can it wait to Phase 2?
4. **Compliance** — any requirement for session audit export / retention in the office?
5. **Branding** — keep `RDP Link` name or rename to `Office RDP` / `Office Link`? Keep `com.eminentailabs.rdplink` bundle id or migrate?

---

## 18. Validation & acceptance gates (per-phase)

### Gate 0 (before Phase 1 code)

- [ ] Transport spike doc with latency / loss / CPU / binary-size numbers for both candidates; decision recorded in `docs/adr/`.
- [ ] Capture spike demos on Linux, macOS, Windows recorded (video artifacts attached to PR).
- [ ] Protocol v1 spec (§11) reviewed by both engineers; no open TODO.
- [ ] CI still green on new layout; `python ubuntu_rdp_manager.py --version`
      and `--list` smoke still pass (`.github/workflows/ci.yml:28` parity).

### Gate 1 (Office RDP v1 release)

- [ ] `pytest` + `pyflakes` green (`ci.yml:25`); new core crate tests green.
- [ ] Multi-device/multi-directional dog-food: 5 hosts × 10 guests (including Android host+guest), LAN + WAN/relay, 1 week, zero orphan cam/mic
      (verified via `watchdog.WATCHDOG_KILLED` exit + manual kill-app test). Test guest→multiple hosts and host→multiple guests with input-token handoff.
- [ ] `office-rdp doctor --check` correctly diagnoses: missing capture backend,
      closed TURN port, revoked TCC grant (each tested).
- [ ] LAN-only mode verified: unplug WAN, mDNS `alias.local` still connects.
- [ ] Security: no cleartext media on wire (pcap check), pairing cert pins host,
      revocation works, tray indicator visible, tray "Stop" kills A/V within 1 s.
- [ ] Packaging: fresh install (no prior state) on each OS yields working host
      and guest; `legacyRdp` profile to `xrdp` host still works (parity: gateway,
      drive, sound/mic, view modes). SSH tunnel for legacy tested.
- [ ] Perf: see §20 (e.g. <120 ms end-to-end on office LAN at 1080p30).
- [ ] Docs: `docs/INSTALL.md` + `docs/SECURITY.md` updated for new ports/auth/
      self-host rendezvous; `docs/TROUBLESHOOTING.md` covers new failure modes.

### Gate 2 (Phase 2)

- [ ] 6-viewer session stable for 30 min, token handoff <500 ms, no relay
      saturation under quota.
- [ ] Org policy push tested on 20-device fleet; audit log export validated.

---

## 19. Testing strategy

### 19.1 Unit / integration (in CI)

- Keep existing Python suite (`tests/` — `test_profiles`, `test_connection`,
  `test_keyboard`, `test_scroll`, `test_secrets`, `test_bridges`, `test_watchdog`,
  `test_deploy`, `test_permissions`, `test_bridge_manager`, `test_connect_flow`).
- Add: `test_peers` (PeerStore normalise/migrate), `test_handshake` (pairing
  cert pin/verify, replay rejection), `test_ice_fallback` (mock candidates),
  `test_transport` (RTP reorder/loss/FEC as in `test_bridges.py:handshake` style
  with real loopback).
- Rust core: `cargo test` with mocked capture (frame generator) + mocked net
  (`turmoil`-style) for congestion/relay tests.

### 19.2 Platform QA matrix

| OS | Host capture | Guest render | HW codec | Pairing | LAN mDNS | WAN/relay |
|---|---|---|---|---|---|---|
| Ubuntu 24.04 (X11) | · | · | VAAPI | · | · | · |
| Ubuntu 24.04 (Wayland) | · | · | VAAPI | · | · | · |
| Fedora 40 | · | · | | · | · | · |
| macOS 14 Intel | · | · | VT | · | · | · |
| macOS 14 AS | · | · | VT | · | · | · |
| Windows 11 x64 | · | · | D3D11 | · | · | · |
| Android 14 (guest) | — | · | MC | · | · | · |

Each cell = manual pass + `doctor --check` green + 60 s session with input +
clipboard + file-drop.

### 19.3 Security testing

- Static: `cargo audit` / `cargo deny`, `pip-audit`, `govulncheck` for Go bits.
- Dynamic: fuzz data-channel parser + signalling JSON; pcap-verify no cleartext
  media; mDNS-spoof test (rogue `_office-rdp._tcp` must not auto-trust).
- Kill-app test: `kill -9` agent → cam/mic processes exit `99` within 1 s
  (`watchdog.py:50` contract).

### 19.4 Performance testing

- Automated: `office-rdp bench --duration 60 --resolution 1080p --fps 30`
  captures p50/p95 latency, bitrate, CPU, drops on loopback + over emulated
  100 ms / 1% loss (via `tc netem` or `com.apple.networkextension` shim).
- Tracked in CI as a non-blocking benchmark job; regressions flagged.

---

## 20. Performance budgets & SLAs (LAN, 1080p30, H.264)

| Metric | Budget (p50 LAN) | Budget (p95 WAN/relay) | How measured |
|---|---|---|---|
| End-to-end latency (key→pixel) | <120 ms | <220 ms | `bench` + high-speed camera spot check |
| Frame drops | <1% | <3% | RTP stats |
| Host CPU (1080p30 encode) | <25% one core (HW) | <40% one core (SW fallback) | `bench` CPU sampler |
| App install size | <80 MB desktop, <30 MB Android | — | Artifact size in CI |
| Time-to-first-frame after Accept | <2 s | <4 s | Instrumented session start |
| Relay bytes per 1 h session | 0 on LAN | metered; quota enforced | Rendezvous counters |

These are budgets, not guarantees — tune quality ladder to stay within them.

---

## 21. Compatibility & migration

### 21.1 Existing installs (v4.5.0)

- `legacyRdp` profiles (`host`, `port`, `username`, `audio/mic/camera` routes)
  keep working exactly as today via FreeRDP — no re-pairing, no new deps,
  `secrets.py` store untouched (`rdp_manager/__init__.py:16` `SECRET_SERVICE`
  stays).
- New flow is additive: users see a banner "Enable Office RDP streaming
  (no xrdp needed) → Show my ID" and can opt in.

### 21.2 IDs & naming

- `mcitlptp810.local` / `192.168.99.2` strings in old profiles keep resolving;
  they gain an "Upgrade to Peer ID?" helper but are never auto-rewritten
  (avoids surprising firewall/policy).

### 21.3 Receiver scripts

- `receiver/camera-receiver.sh` / `mic-receiver.sh` + `deploy.py` SSH path stay
  for `legacyRdp` hosts. Native mode does not use them — document both clearly
  in `docs/INSTALL.md` so office ops know which fleet needs which.

---

## 22. Licensing, compliance & supply chain

- Core: MIT or Apache-2.0 (compatible with current `LICENSE` MIT) — decide in
  Phase 0; either works. Avoid GPL in core so Windows/macOS bundling stays clean.
- Notable deps to vet:
  - `openh264` (BSD) — Cisco binary blob patent dance; document.
  - FFmpeg (LGPL/GPL) — prefer platform HW + `openh264` to avoid GPL taint;
    if FFmpeg stays, build LGPL-only.
  - `pion/webrtc` (MIT), `coturn` (BSD), `webrtc-rs` (MIT/Apache-2), `quinn`
    (MIT/Apache-2) — all permissive.
- Supply chain: `cargo audit` + `pip-audit` + Dependabot in CI; SBOM
  (`cargo auditable` or `syft`) per release.

---

## 23. Cost estimate (infra)

| Item | Self-host (office) | Managed relay fallback |
|---|---|---|
| Rendezvous + STUN | 1 small VM (1 vCPU/1 GB) — ~€5/mo | hosted free tier, metered |
| TURN relay bandwidth | office LAN = 0; WAN tests ~few GB/mo | ~€0.08/GB (Hetzner/OVH ballpark) |
| Signing/notarisation | Apple Developer €99/yr, MS cert ~€80/yr | same |
| CI runners | GitHub free tier + 1 Linux runner | — |

No per-seat SaaS cost if self-hosted — aligns with "open-source, flexible".

---

## 24. Repo layout after the upgrade (proposed)

```
office-rdp/                          # repo root (rename from UbuntuRDPManagerApp)
├── app/                             # launcher/profile UI (Python/Tk, current rdp_manager/)
│   ├── app.py, editor.py, ...       #   evolves in place; no big-bang rewrite
│   └── peers.py                     #   new: PeerStore (mirrors profiles.py)
├── core/                            # Rust crate(s): capture/encode/transport/signal
│   ├── crates/capture/              #   per-OS backends (pipewire/sckit/wgc/…)
│   ├── crates/transport/            #   ICE/DTLS-SRTP/QUIC (chosen in Phase 0)
│   ├── crates/protocol/             #   framing, Hello/Offer/Pair, data channels
│   └── crates/agent/                #   host agent loop + guest viewer glue
├── rendezvous/                      # Go or Rust: registry/presence/STUN/TURN
│   ├── cmd/rendezvous/
│   └── Dockerfile
├── android/                         # Kotlin guest app (JNI to core)
├── packaging/                        # .deb/.rpm/AppImage/NSIS/Brewfile
├── receiver/                        # kept for legacyRdp compat (current *.sh)
├── docs/
│   ├── PLAN-office-rdp.md           # ← this file
│   ├── adr/                         # architecture decision records (new)
│   ├── INSTALL.md, SECURITY.md, …   # updated
│   └── SAME-SCREEN.md               # kept (legacyRdp console listener docs)
├── tests/                           # Python + Rust tests
├── tools/
├── .github/workflows/ci.yml         # extended for Rust/Go/Android builds
├── install.sh                       # successor: office-rdp-install.sh
└── Cargo.toml / go.mod / pyproject.toml
```

Package name transition: keep `rdp-link` CLI alias + `com.eminentailabs.rdplink`
bundle ID for compat; new primary is `office-rdp` / `com.eminentailabs.officerdp`.
Old paths auto-migrate like `paths.py:45` `migrate_legacy()` already does.

---

## 25. Alternatives considered & why not

| Alternative | Why not (for this brief) |
|---|---|
| **Just expose xrdp everywhere** (run xrdp on every host OS) | No viable xrdp on Windows/macOS; still need NAT/relay + A/V + pairing; single point of failure |
| **VNC everywhere (TigerVNC/x11vnc)** | Worse perf/codec story than RDP/WebRTC; still no NAT/relay/A-V; `docs/SAME-SCREEN.md:70` shows VNC is already a niche shim for console attach, not a platform |
| **Fork RustDesk verbatim** | RustDesk's core is solid (Rust + `scrap` capture + `hbb_common` relay) but its UI/protocol/policy are opinionated; we need office-meeting semantics + `legacyRdp` compat + mDNS `.local` parity. Better to reuse patterns (and optionally `scrap`/`hbb_common` crates) than fork the product |
| **Pure WebRTC in browser** (no native agent) | Browser cannot inject input / virtual cam/mic at OS level nor do unattended host — needs native agent anyway |
| **Electron/Tauri only (JS core)** | Heavier than needed; capture/encode/ICE want Rust/Go; browser sandbox hurts permissions |
| **Go-only core** | Viable (strong `pion/webrtc`), but Rust wins on binary size, Android NDK, and Windows service ergonomics — spike will confirm |

---

## 26. Decision log (to fill during build)

| Date | Decision | Rationale | ADR |
|---|---|---|---|
| 2026-11-01 | Plan created (this doc) | Planning-only phase per brief | — |
| *Phase 0* | Transport: WebRTC vs SRT vs QUIC | Spike numbers | `adr/001-transport.md` |
| *Phase 0* | Core language: Rust vs Go | Spike + size/perf | `adr/002-core-lang.md` |
| *Phase 0* | Rendezvous stack | Pair with transport choice | `adr/003-rendezvous.md` |
| *Phase 1* | SFU vs mesh for multi-guest | Load test | `adr/004-multiparty.md` |

---

## Appendix A — current file map

Verified 2026-11-01 (`wc -l` total 3313 lines):

```
rdp_manager/__init__.py        16  version, APP_NAME, BUNDLE_ID, SECRET_SERVICE
rdp_manager/paths.py          104  SUPPORT_DIR, PROFILES_FILE, migrate_legacy(), resource_dir()
rdp_manager/exe.py             30  find_exe(), find_freerdp(), find_ffmpeg()
rdp_manager/profiles.py       160  ROUTES, DEFAULTS, normalize(), ProfileStore (atomic save)
rdp_manager/secrets.py        152  hex: scheme, Keychain/libsecret/file, install_askpass()
rdp_manager/keyboard.py       188  warmup(), resolve_for_connect() → /kbd:layout:*
rdp_manager/scroll.py         331  warmup(), apply_for_connect() (SDL config)
rdp_manager/permissions.py    132  probe_device(), reset_permission_cache() (macOS TCC)
rdp_manager/bridges.py        389  CAMERA_PORT 7272, MIC_PORT 7273, CTL 7274/75, BridgeManager
rdp_manager/connection.py     341  build_args(), shell_quote_cmd(), ConnectionController, HeadlessApp
rdp_manager/deploy.py          86  setup_command() scp+ssh, open_terminal()
rdp_manager/watchdog.py       100  spawn() wrapper, run() parent-death loop (WATCHDOG_KILLED=99)
rdp_manager/logutil.py         35  current_log_path(), open_log(), cleanup() (keep 10)
rdp_manager/mouse.py           18
rdp_manager/mouse_scroll.py    11
rdp_manager/trackpad.py        11
rdp_manager/ui/app.py         374  UbuntuRDPApp — table, menu, device_chips, stop_device_streaming
rdp_manager/ui/editor.py      360  ProfileEditor — routing tabs, device pickers, host setup
rdp_manager/ui/dialogs.py       53  PasswordPrompt
receiver/camera-receiver.sh   230  v4l2loopback MacCamera, responder, systemd --system
receiver/mic-receiver.sh      191  module-pipe-source MacMic, responder, systemd --user
ubuntu_rdp_manager.py           ~80  entry: GUI vs --list/--connect/--bridge-watchdog
install.sh                    144  guest/host/both/--uninstall, Brewfile/apt/dnf/pacman paths
build-app.sh                   67  PyInstaller windowed .app + plist + re-sign
build-linux.sh                 33  PyInstaller onefile → dist/rdp-link
docs/INSTALL.md               144  client+host install, firewall, speaker audio (pulseaudio-module-xrdp)
docs/SECURITY.md               67  camera/mic guarantee, secrets, bridge warning (plain UDP)
docs/SAME-SCREEN.md           122  3389 vs 3390 (new session vs console / x11vnc bridge)
docs/DEVELOPMENT.md            87  principles, layout, ports, tests, building, roadmap
.github/workflows/ci.yml       62  test (macos/ubuntu) + build matrix
```

---

## Appendix B — key open-source building blocks

| Need | Candidate | License | Notes |
|---|---|---|---|
| Screen capture (Linux) | PipeWire portal, `scrap` (RustDesk), X11 `XDamage` | MIT/Apache-2 | `scrap` already abstracts Linux/macOS/Windows |
| Screen capture (macOS) | `ScreenCaptureKit`, `scrap` | Apple / MIT | SCK ≥12.3 preferred for permissions |
| Screen capture (Win) | WGC, DXGI Duplication, `scrap` | MIT/Apache-2 | WGC needs Win10 1903+ |
| Encode | `openh264` (SW H.264), platform HW (VT/D3D11VA/VAAPI/NVENC) | BSD / platform | Avoid GPL FFmpeg if possible |
| Transport | `pion/webrtc` (Go), `webrtc-rs` (Rust), `libjuice`, `quinn` (QUIC), `srt-rs` | MIT/Apache-2/BSD | Decide in Phase 0 spike |
| STUN/TURN | `coturn`, `pion/turn`, built-in in rendezvous | BSD/MIT | Self-host single binary |
| mDNS | `mdns-sd` (Rust), `avahi`/`bonjour` bindings | MIT/Apache-2 | `_office-rdp._tcp` service |
| RDP (compat) | `FreeRDP` (`sdl-freerdp`/`xfreerdp`) | Apache-2.0 | Keep for `legacyRdp` profiles |
| Virtual cam/mic (Linux) | `v4l2loopback`, `module-pipe-source` | GPL-2 / LGPL | Already used in `receiver/*.sh` |
| Installer | `cargo-bundle`, `cargo-deb`, `NSIS`, `AppImageKit` | MIT/Apache-2 | — |

---

## Appendix C — quick glossary

- **Host / Guest** — the shared machine vs the viewer (one install does both).
- **Peer ID** — 9-digit `XXX XXX XXX` derived from device key fingerprint (AnyDesk-style).
- **Alias** — human `hostname.local` (mDNS) for LAN.
- **Rendezvous** — registry + STUN/TURN + presence server (self-hostable).
- **ICE** — Interactive Connectivity Establishment (NAT traversal via STUN/TURN candidates).
- **DTLS-SRTP** — Datagram TLS keying SRTP media (WebRTC's security model).
- **SFU** — Selective Forwarding Unit (relay that forwards, not mixes, media for multi-guest).
- **PipeWire portal** — Wayland-safe screen capture via `xdg-desktop-portal`.
- **TOFU** — Trust On First Use (`/cert:tofu` today — `connection.py:99`).
- **LegacyRdp** — profile type that still goes via FreeRDP → `xrdp` on Linux (keeps current fleet working).

---

*End of plan. No code was changed in this phase. Next step: review this doc,
resolve §17 open questions, then green-light Phase 0 spikes.*
