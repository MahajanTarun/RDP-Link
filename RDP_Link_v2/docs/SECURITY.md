# Security model

## Assets

- Screen frames of the host.
- Input control of the host.
- Files exchanged through the host file endpoints.
- The host password, the session tokens and the device identity key.

## Trust boundaries

| Boundary | Protection |
| --- | --- |
| Guest → host HTTP | Session cookie or `X-Office-Password` on every frame/input/clipboard/file request |
| Guest → host WebSocket | Same credentials, plus an `Origin` check that rejects cross-site upgrades |
| Guest → host HTTP (cross-site) | `Origin` check on state-changing requests, `SameSite=Strict` session cookie |
| Host ↔ rendezvous | Ed25519 signature over method, URI, timestamp and body hash; the peer ID is derived from the signing key |
| Host → guest (relay) | The relay forwards requests only over the host's authenticated tunnel; the guest still needs the host password |
| LAN discovery | mDNS advertises the service; connecting still requires the password |
| Android input helper | BIND_ACCESSIBILITY_SERVICE permission; user must enable it manually |

## Host authentication

The access password is never accepted as a URL parameter. Three credentials
work:

| Credential | Used by |
| --- | --- |
| `X-Office-Password: <password>` | Desktop viewer, CLI tools, the control panel |
| `rdplink_sid` cookie (HttpOnly, SameSite=Strict) | Browsers, after `POST /session` |
| `?token=<media token>` | Media players such as `ffplay` that cannot send headers |

`POST /session` exchanges the password for a session token that is valid for
12 hours. The token is opaque, random and can be revoked by restarting the
host.

`POST /media-token` (authenticated with the password or the session cookie)
returns a separate token that is valid for two minutes and only opens the
read-only media paths. A full session token is never accepted in a URL, so a
media URL that leaks through a process list or a player log cannot drive
input, read the clipboard or manage devices.

The password is also kept out of the process list: the launcher writes it to a
`0600` file and starts the agent with `--password-file`.

## Rendezvous authentication

Every request that changes or reads registry state is signed with the device's
Ed25519 key (`core/peerauth`):

- the peer ID in the request **must** equal `SHA-256(public key)` truncated to
  nine digits, so a signature can only ever claim the ID that belongs to that
  key;
- the signature covers the method, the request URI, a timestamp, a unique
  nonce and a hash of the body, so it cannot be replayed for another endpoint
  or payload;
- timestamps older than five minutes are rejected, and a nonce that was
  already used inside that window is refused, so a captured request cannot be
  replayed even to the same endpoint;
- the first key seen for a peer ID is pinned, so a later registration with a
  different key is refused.

This applies to `/register`, `/tunnel`, `/policy` and `/peers`. `/resolve` stays
open — a guest has to resolve an ID it already knows — but it is rate limited
to one lookup per two seconds per address.

`/peers` returns the peer IDs and aliases to a signed caller that has
registered itself. The LAN addresses are only returned when the request
carries the relay's admin token (`--admin-token`, `RDPLINK_ADMIN_TOKEN`), so
an unprivileged caller cannot map the network. `/pair/start` and
`/pair/verify` need a signature too, and only the two parties in a pairing may
complete it.

The nine-digit peer ID is a lookup handle, not a secret. It is short for
AnyDesk-style usability, so it is guessable in principle; knowing it is not
enough to register, take over a tunnel or read another peer's policy, because
those operations require the matching Ed25519 key. `/resolve` is rate limited
to one lookup per two seconds per address, which keeps the ID space from being
enumerated quickly.

The in-memory tables are bounded, so a caller cannot exhaust the server:
10,000 pinned keys (least recently used evicted), 1,024 pending pairings,
10,000 peer records (entries older than a day are swept), 50,000 replay
nonces (expired entries swept) and 4,096 resolve-throttle entries.

## Hardening already in place

- Password comparison is constant-time on every platform.
- `Origin` is checked inside the shared auth path, so the device bridge
  endpoints and `/session` are covered as well as the screen and input routes.
- Through the relay, the host's session cookie is narrowed to
  `Path=/t/<peerId>/`, so two devices on the same relay never share or
  overwrite each other's cookie.
- Failed logins are throttled per address, and the throttle table is bounded
  (4096 entries) so it cannot be used to exhaust memory.
- `X-Forwarded-For` is only believed when the request carries the per-process
  secret of the in-process relay client, so a local process cannot spoof an
  address and bypass the limiter.
- WebSocket upgrades and state-changing requests check the `Origin` header, so
  a web page on another site cannot drive the host.
- Path traversal on file upload/download is blocked by `sanitizeName`.
- Downloads are served as `Content-Disposition: attachment`.
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
- The rendezvous server keeps the registry in memory; restart it to forget the
  peers. Run it behind TLS for WAN use.
- A relay guest can see the host's LAN address through `/resolve`; the address
  is only returned for peers that are online.
- Session recording captures the raw H.264 stream without encryption at rest.
- The host clipboard is read on demand (cached for 750 ms) instead of polled in
  the background, which keeps helper processes from running when nobody is
  connected.
- The password is a shared secret between the host and the guest. There is no
  per-user identity on the host side yet.

## Reporting

Please report vulnerabilities privately to the maintainers rather than opening a
public issue with exploit details.
