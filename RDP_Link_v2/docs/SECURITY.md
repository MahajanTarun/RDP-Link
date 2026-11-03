# Security model

## Assets

- Screen frames of the host.
- Input control of the host.
- Files exchanged through the host file endpoints.
- The host password and the peer identity seed.

## Trust boundaries

| Boundary | Protection |
| --- | --- |
| Guest → host HTTP | Per-session password on every frame/input/clipboard/file request |
| Guest → host WebRTC | Signalling messages authenticated by the rendezvous; media is DTLS-SRTP |
| Host → rendezvous | Plain HTTP by default; run behind TLS for WAN |
| LAN discovery | mDNS advertises the service; connecting still requires the password |
| Android input helper | BIND_ACCESSIBILITY_SERVICE permission; user must enable it manually |

## Hardening already in place

- Password comparison is constant-time on the Android host.
- Path traversal on file upload/download is blocked by `sanitizeName`.
- Recording names are sanitized and written under the user config directory.
- Input is disabled in `--read-only` mode.
- The Android foreground service is `mediaProjection`, non-exported, and stops
  when the projection is revoked.
- Android backups are disabled (`allowBackup=false`) so the password and seed
  are not copied off-device by default.
- The accessibility helper only performs gestures and text edits; it does not
  read screen content for transmission.

## Known limitations

- LAN traffic is plain HTTP unless you place the agent behind a TLS proxy.
  Treat the session password as the only LAN secret.
- The rendezvous server stores peer metadata in memory and has no built-in
  authentication; deploy it on a trusted network or behind an authenticating
  reverse proxy.
- The Android host does not yet sign signalling messages with the Ed25519
  identity; the desktop agent does.
- Session recording captures the raw H.264 stream without encryption at rest.

## Reporting

Please report vulnerabilities privately to the maintainers rather than opening a
public issue with exploit details.
