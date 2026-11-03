# Host agent (RDP Link)

Run:
```
office-rdp --host --port 8088 --password <secret>
```

It captures the screen (requires OS screen-recording permission) and serves:
- `GET /info` — peer ID, alias, LAN/WAN IPs, port
- `GET /frame.png?password=...` — latest frame
- `POST /input?password=...` — input events (currently logged; injection is next)
- `GET /` — built-in HTML viewer

Guest viewer:
```
cd app && python3 office_viewer.py http://<host-ip>:8088 --password <secret>
```

Notes:
- LAN and WAN IPs are printed at startup.
- macOS requires Screen Recording permission for the terminal/app.
- Password protects frame/input endpoints.

## WebRTC guest (experimental)
Run host with rendezvous:
```
office-rdp --host --port 8088 --password secret --rendezvous http://<rendezvous>:8080
```
Guest opens `http://<rendezvous>:8080/client`, enters host peer ID and password.
