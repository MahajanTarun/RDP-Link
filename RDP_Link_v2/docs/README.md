# RDP Link documentation index

Start here, then dive into the topic you need.

| Doc | What it covers |
| --- | --- |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | **How the complete system works** — components, sequence diagrams for LAN/WAN/auth/input, Android internals, deployment topologies |
| [`DEVELOPMENT.md`](DEVELOPMENT.md) | Dev environment, building every platform, test gates, conventions, contributing |
| [`WAN.md`](WAN.md) | Relay operations — dynamic-IP model, `/resolve` + tunnel protocol, Oracle Cloud $0 deployment, resource sizing, free alternatives, telemetry monitor |
| [`HOST-AGENT.md`](HOST-AGENT.md) | Running `office-rdp` and the full HTTP API (`/session`, `/frame.jpg`, `/input`, clipboard, files, device bridge) |
| [`SETUP.md`](SETUP.md) | In-app setup assistant and OS permissions |
| [`SECURITY.md`](SECURITY.md) | Threat model, auth, throttling, signed relay traffic |
| [`PRIVACY.md`](PRIVACY.md) | What data touches which machine (no telemetry, relay sees proxied traffic) |
| [`TESTING.md`](TESTING.md) | Test bed, tools, pass criteria, reproduction commands |
| [`VALIDATION.md`](VALIDATION.md) | Working-status matrix and evidence per platform |
| [`DEVICES.md`](DEVICES.md) | Device bridge: audio, mic, camera, serial, ports, USB storage |
| [`UI-DESIGN.md`](UI-DESIGN.md) | Ant Design light tokens shared by all surfaces |
| [`ANDROID-HOST.md`](ANDROID-HOST.md) / [`ANDROID-RELEASE.md`](ANDROID-RELEASE.md) | Android host service and release process |
| [`RELEASING.md`](RELEASING.md) | Release checklist, build environment, checksums |
| [`MAC-SIGNING-NOTARIZATION.md`](MAC-SIGNING-NOTARIZATION.md) / [`MACOS-NOTARIZATION.md`](MACOS-NOTARIZATION.md) | macOS signing & notarization |
| [`WINDOWS-INSTALLER.md`](WINDOWS-INSTALLER.md) | NSIS installer build |
| [`FDROID.md`](FDROID.md) | F-Droid packaging notes |
| [`adr/`](adr/) | Architecture decision records |
