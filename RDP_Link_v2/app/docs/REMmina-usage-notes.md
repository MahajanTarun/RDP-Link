# Remmina — Lessons for Office RDP (RDP Link upgrade)

> Reviewed from https://remmina.org/remmina-features/ and https://gitlab.com/Remmina/Remmina. This is a comparative analysis to inform the plan in `docs/PLAN-office-rdp.md`. No code changes made.

## Quick summary
Remmina is a mature, multi-protocol remote connection manager (GTK/Qt direction) supporting RDP, VNC, SSH, SFTP, SPICE, NX, XDMCP, WWW, EXEC, with a plugin architecture. It focuses on **connection management, protocol translation via FreeRDP/VNC/SPICE libs, tunneling/SSH, and UX polish** rather than being a self-hosting "RDP host server + P2P rendezvous" like AnyDesk/RustDesk.

## What Remmina does well (adopt ideas)
| Area | Notes | Apply to Office RDP? |
|---|---|---|
| **Plugin model** | Per-protocol plugins (RDP/VNC/SPICE/SSH/SFTP/etc.), clear feature toggles. | Yes — structure `core/transport/` as protocol/transport plugins (RDP-compat, native P2P/DTLS, VNC fallback). Keep FreeRDP for `legacyRdp` as a "plugin". |
| **Connection UX** | Groups, recent list, double-click connect, remember view mode per connection, tabs, search, tray, keystrokes, host key customization, fullscreen behavior. | Strongly — mirror launcher UX in `app/ui/app.py` (groups/tabs/recent/search), keep "thin launcher" feel. |
| **RDP feature set** | Username/domain/password, color depth/quality, share folder (drive), sound redirection, gateway/RD Gateway (transport/domain/user/pass), client name, startup program/path, load-balance info, printers/serial/parallel/smartcard, mic redirect, clipboard toggle, ignore cert, fast-path toggle, proxy, auto-reconnect, glyph cache, console attach (2003/R2). | Adopt for **legacyRdp** compat. For native transport, map to equivalent (drive→scoped file transfer, printers→IPP later, mic/cam→encrypted bidirectional). |
| **Tunneling** | SSH tunnel local port, parse `~/.ssh/config`, SSH log levels; SFTP plugin. | Keep current `deploy.py` SSH path for host setup; add optional SSH tunnel for legacyRdp when direct not available. |
| **Security options** | Per-connection toggles (ignore cert, disable password storing), gateway auth, SSH. | Match: TOFU + pinning for native, Keychain/libsecret/secrets as today. Add pairing cert pinning. |
| **Multi-view modes** | Scale quality, auto-scroll, fullscreen, view-only mode, grab keyboard. | Port to native viewer toolbar/overlays. |
| **Diagnostics/screenshots** | Screenshot folder/filename, prevent clipboard capture of screenshots, logs. | Extend `logutil.py` + `office-rdp doctor` with similar niceties. |

## What Remmina does *not* do (our opportunity)
| Gap | Notes | Action |
|---|---|---|
| **Self-hosting / P2P rendezvous** | Primarily a client/connection manager, not an AnyDesk/RustDesk-style host agent with Peer ID + global/local discovery + relay. | This is core to our plan (§5–§6). Keep in Rust core + rendezvous service. |
| **True host+guest in same install with NAT traversal/ICE** | No "share this PC" with ID/discovery/consent/unattended out of box as a unified P2P platform. | Implement per §4, §6, §9 (Android host+guest included). |
| **Encrypted A/V over custom P2P with virtual devices cross-OS** | Uses FreeRDP/VNC/SPICE backends; virtual cam/mic injection across all OSes + meeting use case less central. | Our differentiator. Generalize current `receiver/*.sh` to native encrypted transport. |
| **Multi-device/multi-directional N↔N** | Focuses on managing connections/tabs, not serving multiple simultaneous guests with token-based input. | Add room/input-token model (§11). |
| **Mobile host** | Desktop-focused GTK/Qt; no full Android host story. | Android host+guest in P0 as planned. |

## Concrete recommendations for this repo
1. **Adopt plugin boundaries**: Split `rdp_manager/` into `app/` (UI/launcher) + `core/transport/legacy_rdp/` (FreeRDP wrapper) + `core/transport/native/` (P2P/DTLS). Keep `legacyRdp` profiles compatible.
2. **UI parity**: Add groups, tabs, recent search, tray, keystrokes config, per-connection view mode. Keep Tk for launcher now, migrate later as per plan.
3. **RDP options mapping**: For legacy, expose gateway, share folder/drive, sound/mic, printers/serial/smartcard, console attach, fast-path, glyph cache, ignore-cert, auto-reconnect toggles in Profile Editor (extend `rdp_manager/ui/editor.py`).
4. **Tunneling reuse**: Add "SSH tunnel" option to legacy profiles (reuse `ssh` + parsed config) similar to Remmina; document in `docs/INSTALL.md`.
5. **Diagnostics alignment**: Add screenshot capture action + configurable folder/filename + "prevent clipboard" option; extend doctor output.
6. **Don't fork Remmina**: Better to learn from it and extend RDP Link's strengths (thin, watchdog, device bridges, mDNS/LAN UX) rather than rewriting to GTK plugin system wholesale. Rust core + thin shell matches our needs better for Android/Windows service/ICE.

## Impact on implementation file (`docs/PLAN-office-rdp.md`)
- **§9.2/architecture**: Add "transport plugin model" note (RDP-compat via FreeRDP plugin, Native P2P plugin) to align with Remmina's proven pattern.
- **§9.3 packaging**: No change needed, but mention plugin discovery/loading.
- **§12 P0/P1**: Consider adding "SSH tunnel for legacy RDP", "gateway/RD Gateway support for legacy", "printer/serial redirection parity" where easy (some already planned).
- **§14 UX**: Explicitly borrow groups/tabs/recent/search/tray/keystrokes from Remmina.
- **§18 gates**: Add legacyRdp parity checklist (gateway, drive, sound/mic, view modes).

## Verification
Remmina site loaded (features page) and GitLab README reviewed. Plugin list confirms RDP/VNC/SPICE/SSH/SFTP/NX/XDMCP/WWW/EXEC coverage and extensive RDP options matching our compatibility needs. 
