# Troubleshooting

The app never leaves you guessing: bridge problems surface in the status
line (bottom bar), details land in the session log
(**right-click a profile → Show session log**).

## Status messages

| Message | Meaning | Fix |
|---|---|---|
| `Camera bridge: receiver detected on HOST ✓` | Handshake OK; data receiver listening | Nothing — it's working |
| `no receiver response on HOST` | Control port unanswered | Run **Set up receivers on host…** (context menu) |
| `receiver script running but its data receiver is down` | Responder alive, ffmpeg dead | On host: `systemctl status ubuntu-rdp-camera-receiver`; check its journal |
| `Camera stream stopped (exit N)` | Local ffmpeg died | See session log; usually macOS camera permission |

## Symptom → fix matrix

### Session "extends" instead of showing the same screen
Port 3389 creates a new virtual session; the same-screen console listener
lives on 3390 in typical setups. Edit the profile's port — full guide:
**[SAME-SCREEN.md](SAME-SCREEN.md)**.

### No remote sound
`pulseaudio-module-xrdp` / `pipewire-module-xrdp` missing on the host.
Install and re-login to the RDP session.

### No mic in the remote session
- Host on xrdp **0.9.x** → native RDP mic redirection won't work; use the
  **Microphone → This computer** route (ffmpeg bridge). Works everywhere.
- Host on xrdp **0.10+** → you can also switch the route to
  *Native RDP microphone*, then pick the redirected input in Teams.
- Teams may need to be told to pick **MacMic** manually (device settings).

### Camera doesn't show in Teams on the host
Work through these, in order:
1. **Client side**: profile → Edit → *Test…* produces a photo? If not, fix
   local permission (System Settings → Privacy & Security → Camera) or pick
   the right device (the picker lists **video devices only**).
2. **Handshake**: connect; status says *receiver detected*? If not, run
   **Set up receivers on host…**.
3. **Host side**: `ubuntu-rdp-camera-receiver --check` — it reports module
   state (`exclusive_caps` must be `1`), the loopback node, receiver
   process, responder and firewall.
4. **Firewall**: `sudo ufw status` must allow `7272:7275/udp` — the
   installer adds this; if you manage rules manually:
   `sudo ufw allow 7272:7275/udp`.
5. In Teams/Zoom, select the **MacCamera** device.

### "Input/output error" opening the camera (macOS)
Almost always permission or a wrong device index. The camera picker shows
video devices only; use *Refresh* and pick your camera (e.g.
`0 — MacBook Pro Camera`), then *Test…*. Capture parameters are fixed at
1280x720@30 uyvy422 — the modes this camera family supports.

### Saved password doesn't connect
Toggle **stdin pass** handling per profile by editing the profile: v4 pipes
the password via `/from-stdin`. If a particular FreeRDP build ignores it,
the app falls back to `FREERDP_ASKPASS`. Use *Copy FreeRDP Command* (context
menu) to reproduce a connect manually — the copied command contains a
`YOUR_PASSWORD` placeholder, never the real password.

### Non-ASCII password failed after upgrade
Fixed in v4: passwords are stored hex-wrapped (`hex:` prefix) because newer
macOS `security -w` hex-prints non-ASCII values on read. Re-save the
password once from the editor to migrate.

### App feels slow to connect
It shouldn't — the connect path is free of blocking probes. If you routed
camera/mic locally, the bridges spawn in parallel with FreeRDP; the
permission prompt (first use only) is the OS's, not ours.

## CLI diagnostics

```sh
rdp-link --list                       # profiles + state
rdp-link --connect "MolCon Wired"     # headless connect
tail -f "$HOME/Library/Application Support/RDP Link/logs/"session-*.log
```

## Receiver service management (on the host)

```sh
systemctl status ubuntu-rdp-camera-receiver     # camera (system service)
journalctl -u ubuntu-rdp-camera-receiver -e
systemctl --user status ubuntu-rdp-mic-receiver # mic (user service)
journalctl --user -u ubuntu-rdp-mic-receiver -e
```
