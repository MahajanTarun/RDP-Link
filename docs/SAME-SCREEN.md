# Same screen vs. new session: port 3390 vs 3389

**TL;DR** — your xrdp server can answer on two different ports and they
behave completely differently:

| Port | What you get |
|---|---|
| **3389** (default) | A **new virtual session**: xrdp spawns a fresh Xorg/Xvnc desktop for your login. The physical monitor keeps showing whatever it shows; the RDP window is a separate, parallel desktop. |
| **3390** (your setup) | The **same screen**: a secondary listener configured to reach the *physical console* — you see exactly what's on the monitor attached to the Ubuntu box, and what you do there is mirrored both ways. |

If an RDP connection "extends" or duplicates your screen instead of showing
the machine's own display, you connected to **3389** when you wanted the
**3390-style console listener**.

## Why this happens

`xrdp` is a *broker*: on port 3389 it starts a **new backend X session per
login** (typically `Xorg` via `xorgxrdp`). That's the behaviour most
documented and most people want — a clean remote workspace.

"Same screen" requires a backend that *attaches to the console display*
instead of creating one. The common recipe is a second xrdp listener whose
session is a **VNC bridge to the console** (`x11vnc -display :0`), served on
a second port. Your machine already has this wired up on 3390.

## Decision table

| You want… | Use |
|---|---|
| The monitor's exact picture (control the physical box) | 3390 / console listener |
| A clean, independent workspace (safe to run alongside console logins) | 3389 |
| GPU/Teams session on the go, mirroring what your desk setup shows | 3390 |
| Testing, or two people working at once (different users) | 3389 |

## Symptoms you're on the "wrong" one

- Screen **extends** / feels like a second display rather than the machine's
  own screen → you're on 3389
- The physical monitor and your RDP window show **identical, live-synced**
  content → you're on the console listener (3390)
- Apps logged in at the console "disappear" from view after connecting on
  3389 → that's expected: they're on the console, your RDP session is
  elsewhere. Move back to 3390 to see them.

## Using the same-screen listener from a different network

The console listener may be firewalled to a specific subnet (e.g. only your
wired one). To reach it from another network — WiFi included — allow it on
the host:

```sh
sudo ufw allow from <your-subnet>/24 to any port 3390 proto tcp
```

Then in the app: edit the profile → set **Port** to `3390`. Everything else
(device routing, saved password) is per-profile and unaffected.

> Check whether the profile's *host* is the same machine first! In our case
> `MolCon Wired` = `192.168.99.2:3390` (wired link) and
> `MolCon Wifi LAN` = `mcitlptp810.local` → `192.168.1.12` — different
> addresses, need to be the same box.

## Setting up a same-screen listener from scratch

(Try this only if `--check`/your admin confirms no console listener exists
yet. On our MolCon box it already exists — don't duplicate it.)

```sh
# 1. Console VNC bridge (runs on the host, mirrors the physical display)
sudo apt install -y x11vnc
sudo tee /etc/systemd/system/urdm-console-vnc.service >/dev/null <<'EOF'
[Unit]
Description=Console VNC bridge for RDP same-screen sessions
After=graphical.target

[Service]
ExecStart=/usr/bin/x11vnc -display :0 -localhost -rfbport 5900 -nopw -shared -forever
Restart=on-failure

[Install]
WantedBy=graphical.target
EOF
sudo systemctl enable --now urdm-console-vnc.service

# 2. Second xrdp listener on 3390 pointing at that VNC port
sudo cp /etc/xrdp/xrdp.ini /etc/xrdp/xrdp-console.ini
sudo sed -i 's/^port=3389/port=3390/' /etc/xrdp/xrdp-console.ini
sudo sed -i 's/^security_layer=.*/security_layer=negotiate/' /etc/xrdp/xrdp-console.ini
sudo tee /etc/systemd/system/xrdp-console.service >/dev/null <<EOF
[Unit]
Description=xrdp console listener (same screen)
After=network.target urdm-console-vnc.service

[Service]
ExecStart=/usr/sbin/xrdp --config /etc/xrdp/xrdp-console.ini --nodaemon
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload && sudo systemctl enable --now xrdp-console.service

# 3. Firewall (only where you need it)
sudo ufw allow from <your-subnet>/24 to any port 3390 proto tcp
```

Verify: `sudo ss -ltnp | grep -E '3389|3390'` — you should see xrdp on both.

## Security notes

- A console listener grants **the same trust as sitting at the keyboard** —
  including an unlocked desktop. Scope it with ufw (`from <subnet>`) and
  never expose it beyond your LAN/VPN.
- The VNC bridge above binds `-localhost` only; xrdp is the only thing
  talking to it, and RDP credentials protect the external side.
- Port 3389 sessions are per-login and isolated — lower risk if the machine
  is physically shared.

## Related

- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — general symptom → fix matrix
- [SECURITY.md](SECURITY.md) — stream and secret security
