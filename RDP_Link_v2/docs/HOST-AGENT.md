# Host agent (RDP Link)

Run:
```
office-rdp --host --port 8088 --password-file ~/.config/office-rdp/host-password
```

`--password <secret>` also works, but a file (or the `RDPLINK_PASSWORD`
environment variable) keeps the password out of the process list.

It captures the screen (requires OS screen-recording permission) and serves:
- `POST /session` — exchanges the access password for an `HttpOnly` session
  cookie
- `GET /info` — peer ID, alias, LAN/WAN IPs, port (needs credentials)
- `GET /frame.jpg`, `GET /frame.png` — the latest frame, with `ETag`/`304`
- `POST /input` — input events
- `GET /clipboard`, `POST /clipboard` — clipboard sync
- `GET /files`, `POST /upload`, `GET /download` — file transfer
- `GET /capabilities`, `/audio`, `/mic`, `/camera.mjpg`, `/serial`,
  `/usb/*`, `/port/<port>/` — the device bridge
- `GET /` — the built-in HTML viewer, `GET /ui` — the control panel

Every endpoint needs a credential: the `X-Office-Password` header, the
`rdplink_sid` cookie or a `?token=` session token.

Guest viewer:
```
cd app && python3 office_viewer.py http://<host-ip>:8088 --password <secret>
```

Notes:
- LAN and WAN IPs are printed at startup.
- macOS requires Screen Recording permission for the terminal/app.
- The password protects the frame, input, clipboard and file endpoints.

## Relay guest (WAN)
Run the host with a rendezvous server:
```
office-rdp --host --port 8088 --password-file <file> --rendezvous https://relay.example.com
```
The host signs its registration and its tunnel with the device Ed25519 key,
so the relay can verify that it owns the peer ID it claims. Guests resolve the
peer ID and connect through `https://relay.example.com/t/<peer-id>/`.

See [`WAN.md`](WAN.md) for the relay and [`SECURITY.md`](SECURITY.md) for the
authentication model.
