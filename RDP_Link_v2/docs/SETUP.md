# Setup & permissions

RDP Link aims for "install, open, use". This page lists what the installer and
the in-app **Setup & permissions** assistant do automatically, and which
consents are reserved for the user by each OS.

Open the assistant from **Setup** in the launcher header (macOS/Linux/Windows)
or **Setup & permissions** on Android.

---

## What is automated

| OS | Automation | Where |
| --- | --- | --- |
| **macOS** | Application Firewall allow for the agent | `.pkg` postinstall and the assistant (admin prompt) |
| | Host at login (LaunchAgent) | `.pkg` postinstall and the assistant "Install" |
| | Screen recording / Accessibility / Camera / Mic requests | Bundled `rdp-link-tcc` helper triggers the system prompt and opens the right Settings pane |
| | Status detection for every permission | assistant |
| **Windows** | Firewall rule `RDP Link` | NSIS installer and assistant |
| | Start at login (HKCU Run) | assistant "Install" |
| | Camera/mic privacy settings | assistant deep-links `ms-settings:privacy-*` |
| **Linux** | `v4l2loopback` + `usbip-host` load, uinput udev rule | `.deb` postinstall and assistant |
| | Firewall port 8088 | assistant (`pkexec ufw allow 8088/tcp`) |
| | Host user service | assistant writes `~/.config/systemd/user/rdp-link-host.service` |
| | Input group / uinput access | assistant (`pkexec` udev rule) |
| **Android** | Accessibility, notifications, battery optimisation status | **Setup & permissions** dialog with deep links |
| | Screen-capture consent | explained; Android requires per-session approval |

## What still needs one user tap (OS security)

- macOS: **Screen Recording**, **Accessibility**, **Camera**, **Microphone**,
  **Local Network**, and any **system extension** (camera/USB). These are TCC
  decisions; an app cannot pre-grant them. The assistant triggers the prompt and
  opens the exact pane. Managed Macs can pre-approve them with an MDM PPPC
  profile.
- Windows: camera/mic if the Privacy settings block desktop apps; USB/IP driver
  installation (admin + reboot) when used.
- Linux: Wayland screen-capture portal; Secure Boot MOK enrolment for
  `v4l2loopback`.
- Android: **MediaProjection** approval every sharing session and
  **Accessibility** enablement. Only device-owner/MDM or ADB can pre-enable
  accessibility; Play policy does not allow apps to do it silently.
- Browser guest: camera/mic need HTTPS (or localhost).

## Installer artifacts

| Platform | Artifact | Automation |
| --- | --- | --- |
| macOS | `RDPLink-macos.dmg` (drag-to-Applications) | assistant handles firewall/service |
| macOS | `RDPLink-installer.pkg` | postinstall: firewall + LaunchAgent; privacy via assistant |
| Windows | `RDPLink-setup.exe` | firewall rule, shortcuts, uninstaller; startup via assistant |
| Linux | `rdplink-linux-*.deb` | postinst: modules, uinput udev rule; firewall/service via assistant |
| Android | `RDPLink-android-release.apk` | runtime prompts + Setup dialog |

## Status model

The assistant reports four states:

- **Ready** — granted/installed and verified.
- **Action needed** — a one-time user action (prompt/deep link) or an
  installable service.
- **Blocked** — denied/restricted; the OS must be changed in Settings.
- **Optional** — hardware/driver features not required for basic use
  (virtual camera, USB redirection).

## Enterprise / zero-touch

- macOS: MDM **PPPC** payload for Screen Recording, Accessibility, Camera,
  Microphone and Local Network; system-extension policy for the camera
  extension.
- Windows: Intune/GPO for firewall and startup; SmartScreen avoided with EV or
  Azure Trusted Signing.
- Android: managed configurations/device-owner for accessibility and
  MediaProjection policy.
- Linux: configuration management for ufw, groups, modules and the user unit.

## Verifying the assistant

- macOS: **Setup** shows Screen recording / Accessibility / Camera / Microphone
  with **Ready** or **Action needed**; "Install" creates the LaunchAgent.
- Linux: the same dialog lists uinput, firewall, v4l2loopback, usbip-host and
  the user service.
- Windows: firewall and startup rows, plus privacy deep links.
- Android: **Setup & permissions** shows accessibility, notifications, battery
  and the screen-capture note.
