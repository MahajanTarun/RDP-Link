# Security notes

## Passwords
- macOS: stored in the login **Keychain** under service
  `com.eminentailabs.UbuntuRDPManager`, keyed by profile ID.
- Linux: stored via `secret-tool` (libsecret) when available; otherwise a
  `0600` JSON file in the config dir (documented plaintext fallback — check
  `docs/INSTALL.md`).
- v4 stores values as `hex:` + hexdump. This works around a macOS 26 quirk
  where `security -w` hex-prints non-ASCII passwords on read (which used to
  corrupt them). Reads transparently decode the prefix; raw values written
  by v3 still read fine.
- FreeRDP receives the password via **stdin** (`/from-stdin`) or the
  `FREERDP_ASKPASS` helper — it never appears in the process argument list,
  which any local user can see via `ps`.
- The askpass helper is written `0700` and only decodes the Keychain value.

## Device streams
- The camera/mic bridges are **unencrypted MPEG-TS over UDP** (7272/7273),
  sent only while a connection with those routes is active. Everything is
  terminated on disconnect (SIGTERM → SIGKILL escalation).
- The receiver handshake (7274/7275) is a 20-line Python responder that
  answers pings only.
- Anyone on the LAN can *see* the stream if they can sniff it, and could
  send frames to a receiver that doesn't filter sources. **Mitigate on
  untrusted networks** by scoping the firewall to your client:
  ```sh
  sudo ufw delete allow 7272:7275/udp
  sudo ufw allow from <client-ip> to any port 7272:7275 proto udp
  ```
- HTTPS/SRT-wrapped streams are a planned improvement (tracked in
  docs/DEVELOPMENT.md) — until then, treat the bridge as LAN-grade privacy,
  not internet-grade.

## Certificates
`/cert:tofu` (trust on first use) is on by default, mirroring Windows RDP
behaviour: the first-seen host certificate is remembered; changes are
rejected. Turn it off per profile if your PKI dictates.

## Files & clipboard
`/drive:MacFiles,$HOME` exposes your **entire home directory** to the remote
session when "Share computer files" is on. Scope it down by editing
`rdp_manager/connection.py::build_args` if you only need a folder.

## What we don't do
- No telemetry, no network calls beyond your RDP host, the UDP bridges and
  the receiver handshake.
- No root: the client app never elevates; the host installer uses sudo only
  in the terminal window you control.
