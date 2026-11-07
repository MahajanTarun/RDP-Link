# WAN access, relays and dynamic IPs

> **Public relay (provided as-is, best effort).** A convenience relay is
> currently available at `https://relay.130-210-56-200.sslip.io`. It is
> operated by the project maintainer on a best-effort basis and is **subject
> to change, rate limiting, or removal at any time without notice**. No uptime,
> availability, or data-durability guarantees are provided. **Use it at your own
> risk.** For production, privacy-sensitive, or high-availability use, run
> your own relay (see §11).

RDP Link has two ways to reach a host:

| Path | When to use | How |
| --- | --- | --- |
| **LAN** | Both devices on the same network | Guest connects to `host:8088` (or the mDNS name) |
| **Relay (WAN)** | Different networks, CGNAT, changing public IP | Host dials out to a rendezvous server; guests connect to the relay by peer ID |

The relay solves the dynamic-IP problem: **the host never needs a public IP,
port forwarding or a DNS name.** It keeps an outbound WebSocket to the
rendezvous server, and guests reach it at
`https://<relay>/t/<peer-id>/…` which is proxied over that connection.

---

## 1. Run a relay server

```sh
# Plain HTTP (LAN/testing only)
office-rdp-rendezvous --listen :8080 --public-url http://192.168.1.14:8080

# With an admin token: the full registry (including the LAN addresses) is
# only handed out to callers that know the token.
RDPLINK_ADMIN_TOKEN=$(openssl rand -hex 16) \
  office-rdp-rendezvous --listen :8080 --public-url http://192.168.1.14:8080

# TLS (required for WAN)
office-rdp-rendezvous --listen :443 \
  --tls-cert /etc/letsencrypt/live/relay.example.com/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/relay.example.com/privkey.pem \
  --public-url https://relay.example.com
```

`--public-url` is the address guests will use; it is what `/resolve` returns.
Terminate TLS at the relay (or behind a reverse proxy that sets
`X-Forwarded-Proto: https`).

## 2. Start the host with the relay

```sh
office-rdp --host --port 8088 --password <secret> \
  --rendezvous https://relay.example.com
```

The host:

1. registers its peer ID (every 30 s),
2. opens the outbound tunnel (`/tunnel?peerId=…`),
3. reconnects automatically with backoff if the network drops or the IP changes.

Both requests are **signed with the device's Ed25519 key**. The peer ID is
derived from that key, so the relay can verify that the host owns the ID it
claims: nobody else can register somebody else's peer ID, hijack a tunnel or
change a peer's policy. The signature covers the method, the request URI, a
timestamp (five minute window) and a hash of the body.

For a self-signed relay during testing:

```sh
office-rdp ... --rendezvous https://192.168.1.14:8443 --relay-insecure
# or trust a private CA:
office-rdp ... --rendezvous https://relay.local --relay-ca /path/ca.pem
```

## 3. Connect a guest

**Desktop launcher:** enter the 9-digit RDP Link ID, set the rendezvous URL in
the same panel (or leave the last one), and press Connect. The launcher:

- resolves the ID with `/resolve`,
- probes the returned LAN address for ~0.6 s,
- uses the LAN address when reachable, otherwise the relay URL.

**Browser / Android:** either paste the relay URL
`https://relay.example.com/t/033976933/` (the trailing slash matters), or set
the rendezvous URL in Android's **Advanced settings** and enter the 9-digit ID;
the app resolves it to the relay URL automatically. The Android host starts the
same outbound relay tunnel as the desktop agent (through the gomobile-bound Go
relay client) when a rendezvous URL is configured.

## 4. What the relay does and does not do

| Property | Behaviour |
| --- | --- |
| NAT traversal | Outbound host tunnel; no inbound ports |
| Dynamic IP | Irrelevant: guests use the relay's stable address |
| Reconnect | Host reconnects with exponential backoff; viewers retry |
| Transport | WebSocket (`ws://` or `wss://`) carrying base64 HTTP requests |
| Request size | 32 MiB per request/response over the relay |
| Concurrency | Up to 6 in-flight requests per host |
| Auth | Host password still enforced end-to-end by the host; the relay only accepts signed requests from the host |
| Registry | `/peers` needs a signed request (`office-rdp --list-peers <relay>`) and only reveals the LAN addresses when the admin token is presented; `/resolve` is open but rate limited |
| Encryption | TLS between host↔relay and guest↔relay; the relay terminates TLS |
| Multiple hosts | One tunnel per peer ID; a second connection is rejected |

**Trust model:** the relay sees request/response bodies because it terminates
TLS. Run it yourself, or use one you trust. For end-to-end encryption without
trusting the relay, use an overlay network (Tailscale/WireGuard); the HTTP
relay is deliberately simple.

**Stream cleanup:** when a guest closes an audio or camera stream, the relay
sends a cancel frame to the host, which stops the capture immediately and
releases the device. An abandoned stream can no longer stall the tunnel.

## 5. Testing a WAN-style path locally

```sh
# terminal 1 — relay
office-rdp-rendezvous --listen :18080 --public-url http://192.168.1.14:18080

# terminal 2 — host
office-rdp --host --test-pattern --port 18530 --password relay1 \
  --rendezvous http://192.168.1.14:18080

# terminal 3 — resolve + fetch through the relay
curl -s 'http://192.168.1.14:18080/resolve?peerId=033976933'
curl -s -o f.jpg -w '%{http_code} %{size_download}\n' \
  -H "X-Office-Password: relay1" \
  'http://192.168.1.14:18080/t/033976933/frame.jpg'

# the browser flow: exchange the password for a session cookie
curl -s -c jar.txt -X POST -H "X-Office-Password: relay1" \
  -H 'Content-Type: application/json' -d '{"password":"relay1"}' \
  'http://192.168.1.14:18080/t/033976933/session'
curl -s -b jar.txt -o f.jpg 'http://192.168.1.14:18080/t/033976933/frame.jpg'
```

TLS variant: add `--tls-cert/--tls-key` to the relay, `--relay-insecure` (or
`--relay-ca`) to the host, and use `https://` URLs with `curl -k` while
testing.

Validated: relay + TLS, resolution, `/ui`, `/frame.jpg`, and the full viewer
(including the Android app) through the tunnel.

## 6. Moving devices between networks

1. Start the relay with a stable public address (VPS + Let's Encrypt).
2. On each host, pass `--rendezvous https://relay…`.
3. Keep the 9-digit ID in the guest's recent list; no IP is stored.
4. When a device changes networks, the host reconnects on its own and the guest
   resolves the same ID to the relay.

No port forwarding, DDNS or static IP is needed.

---

## 7. How WAN works with dynamic IPs (detailed overview)

### 7.1 The problem

On the internet most devices have **dynamic public IPs** that change over time,
and many are behind CGNAT with **no inbound ports** at all. A guest cannot
reliably connect to `host:8088` because:

- the host's public IP changes,
- the host may not have a public IP,
- port forwarding is not possible.

### 7.2 The solution: a stable relay + a stable peer ID

```
        Dynamic-IP world                 Stable relay
┌─────────────┐                    ┌──────────────────┐
│  Host (Mac) │ ──outbound WS────► │ relay.example.com│ ◄── Guest (anywhere)
│  (changes)  │   peerId=033…      │  /t/033976933/   │    uses peer ID
└─────────────┘                    └──────────────────┘
```

- **Peer ID** — derived from the device's Ed25519 key. It is stable for the
  lifetime of that device, even across IP changes and networks.
- **Relay (rendezvous server)** — the only piece with a stable public address.
  The host keeps an **outbound** WebSocket tunnel open to it.
- **Guest** — connects to the relay using the peer ID, not an IP.

### 7.3 Step-by-step flow

1. **Host starts sharing with a relay:**
   ```sh
   office-rdp --host --port 8088 --password secret \
     --rendezvous https://relay.example.com
   ```
   - Generates/loads its Ed25519 key → derives peer ID `033 976 933`.
   - Registers the peer ID with the relay (request signed with the private key).
   - Opens an outbound WebSocket tunnel `/tunnel?peerId=033976933`.
   - Reconnects with exponential backoff if the network drops or the IP changes.

2. **Guest connects:**
   - Enters the 9-digit peer ID (`033 976 933`) and the relay URL.
   - Calls `GET /resolve?peerId=033976933` on the relay.
   - Relay returns `{"relay":true,"url":"https://relay.example.com/t/033976933/"}`.
   - Guest uses that URL for frames, input, clipboard, etc.
   - The relay proxies every request over the host's outbound tunnel.

3. **LAN preference (automatic):**
   - The guest also receives the host's LAN address from `/resolve`.
   - It probes that address for ~0.6 s; if reachable, it uses LAN directly.
   - Otherwise it falls back to the relay URL automatically.

### 7.4 Why the dynamic IP does not matter

| Problem | How RDP Link solves it |
|---|---|
| Host's public IP changes | Host reconnects to the relay; peer ID stays the same |
| Host behind CGNAT / no inbound ports | Outbound tunnel only; no port forwarding needed |
| Guest doesn't know host's IP | Guest uses peer ID + relay URL, never an IP |
| Host moves between networks | Reconnects with backoff; viewers retry |

### 7.5 Security notes

- Peer ID ownership is verified via Ed25519 signatures — nobody can hijack
  another device's peer ID.
- The host password is still enforced end-to-end by the host; the relay only
  proxies bytes.
- The relay terminates TLS, so it sees request bodies. Run your own relay or
  one you trust. For full end-to-end encryption without trusting the relay,
  use an overlay network (Tailscale/WireGuard).

### 7.6 Which relay is used "as of now"

There is **no shared/public relay bundled with RDP Link**. You self-host the
relay server (`office-rdp-rendezvous`) on any machine with a stable address
(VPS, home server, office machine, etc.).

During validation of this release we used:

- a **local relay** on `http://192.168.1.14:18080` for LAN-style WAN tests, and
- a **real public hop** via a Cloudflare quick tunnel
  (`https://…trycloudflare.com`) to exercise a true internet path.

In production, point `--rendezvous` at your own stable relay URL (ideally with
TLS). No third-party relay service is required or used.

---

## 8. Testing with a public hop: `cloudflared` quick tunnel

A **Cloudflare quick tunnel** is a temporary public HTTPS URL that proxies
traffic to a service running on your local machine. It requires no domain name,
no DNS setup and no Cloudflare account — you just run one command.

### 8.1 Why we use it

During validation we needed to prove the WAN path works over the **real
internet**, not just a LAN relay. Instead of buying a domain or configuring TLS
on a VPS, we used a quick tunnel to expose the local relay/host to the public
internet for a few minutes.

### 8.2 How to use it

```sh
# Expose a local service (e.g. the relay or host) to the public internet
cloudflared tunnel --url http://127.0.0.1:8080
```

Cloudflare prints a URL like:

```
https://random-words-1234.trycloudflare.com
```

That URL is live immediately and proxies HTTPS traffic to your local service.

### 8.3 Example: testing the Mac host over the internet

```sh
# terminal 1 — host on port 8099
office-rdp --host --port 8099 --password secret

# terminal 2 — public tunnel
cloudflared tunnel --url http://127.0.0.1:8099
# → https://abc-def-ghi.trycloudflare.com

# terminal 3 — guest from anywhere
curl -s -H "X-Office-Password: secret" \
  https://abc-def-ghi.trycloudflare.com/info
curl -s -o frame.jpg -H "X-Office-Password: secret" \
  https://abc-def-ghi.trycloudflare.com/frame.jpg
```

### 8.4 Important caveats

- Quick tunnels are **temporary** — the URL changes every run and expires when
  the process stops. They are for **testing only**, not production.
- The host password and origin checks still apply; the tunnel only adds a
  public HTTPS endpoint.
- For production, use a real relay on a VPS with your own domain and TLS
  (see sections 1–6 above).

### 8.5 Summary

| Tool | Purpose | Production? |
|---|---|---|
| `office-rdp-rendezvous` | Self-hosted relay server | ✅ Yes |
| `cloudflared tunnel --url …` | Temporary public HTTPS proxy for testing | ❌ No (testing only) |

---

## 9. Resource requirements for running the relay (VPS)

The relay (`office-rdp-rendezvous`) is a single, small Go binary. It is
**lightweight** — the cost driver is **network bandwidth**, not CPU or RAM.

### 9.1 Minimum VPS specs

| Resource | Minimum | Recommended | Notes |
|---|---|---|---|
| **CPU** | 1 vCPU (shared) | 1–2 vCPU | Relay work is I/O-bound, not compute-heavy |
| **RAM** | 256 MB | 512 MB – 1 GB | ~20–50 MB base; grows with concurrent tunnels |
| **Disk** | 1 GB | 5–10 GB | Binary + logs; no database required |
| **Network** | 1 TB/month transfer | 2–5 TB/month | **This is the real cost driver** |
| **OS** | Linux (any) | Ubuntu 22.04/24.04 | Static binary; no dependencies |

### 9.2 Bandwidth reality check

Each remote session streams JPEG frames. Rough estimate:

- 1 session @ 10 fps, 1080p ≈ 1–2 Mbps ≈ 300–600 GB/month
- 5 concurrent sessions ≈ 5–10 Mbps ≈ 1.5–3 TB/month

If you expect many public users, bandwidth — not CPU/RAM — is what you pay for.

### 9.3 What the relay does NOT need

- No database
- No Redis / message queue
- No persistent storage beyond logs
- No GPU

---

## 10. Free / open-source alternatives to a paid VPS

If you don't want to pay for a VPS, here are realistic options:

### 10.1 Free-tier cloud (run the relay for $0)

| Provider | Free tier | Notes |
|---|---|---|
| **Oracle Cloud (Always Free)** | 2 AMD VMs (1/8 OCPU, 1 GB RAM) or 4 ARM cores + 24 GB RAM | Best free option; ARM instances are powerful and free forever |
| **Google Cloud (Free Tier)** | 1 e2-micro VM (0.25 vCPU, 1 GB RAM) in us-central1/us-west1/us-east1 | Enough for a small relay; 1 GB egress/month free |
| **AWS (Free Tier, 12 months)** | t2.micro / t3.micro (1 vCPU, 1 GB RAM) | 15 GB egress/month free for 12 months |
| **Azure (Free, 12 months)** | B1s VM (1 vCPU, 1 GB RAM) | 15 GB egress/month free |
| **Fly.io** | 3 shared-CPU VMs, 256 MB RAM each, 160 GB egress/month | Good for small relays; auto-sleep on free tier |
| **Render / Railway** | Free web service tier | May sleep after inactivity; check bandwidth limits |

> ⚠️ Free tiers often have **bandwidth caps** (1 GB–160 GB/month). For heavy
> screen-sharing traffic, you may hit limits quickly. Monitor usage.

### 10.2 Self-host at home (no VPS cost)

- Run the relay on a **Raspberry Pi**, old laptop, or any always-on machine
  at home.
- Use **port forwarding** on your router (port 443 → relay) + a free
  **DDNS** (DuckDNS, No-IP) for a stable hostname.
- Or use **Cloudflare Tunnel** (free, stable) to expose the relay without
  port forwarding (see §8).

### 10.3 Overlay networks (avoid the relay entirely)

If you don't want to run a relay at all, use a VPN/overlay network so both
devices are on the same virtual LAN:

| Tool | Cost | Notes |
|---|---|---|
| **Tailscale** | Free for personal use (up to 100 devices) | Easiest; devices get stable IPs; RDP Link then uses LAN mode |
| **Headscale** | Free (self-hosted Tailscale control server) | Full control; run on a tiny VPS or Raspberry Pi |
| **WireGuard** | Free / open source | Manual key management; very low overhead |
| **ZeroTier** | Free tier (up to 25 devices) | Similar to Tailscale |
| **Cloudflare WARP / Tunnel** | Free | Can expose services without public IP |

With any of these, RDP Link's **LAN mode** works directly — no relay needed.

### 10.4 Free public tunnel services (testing / light use)

| Service | Free? | Stable URL? | Notes |
|---|---|---|---|
| **Cloudflare Tunnel (quick)** | ✅ Free | ❌ Temporary | Testing only (see §8) |
| **Cloudflare Tunnel (named)** | ✅ Free | ✅ Yes (needs a domain on Cloudflare) | Best free option for a stable public relay |
| **ngrok (free tier)** | ✅ Free | ❌ Random URL | 1 tunnel, rate limits, sessions expire |
| **localtunnel** | ✅ Free / open source | ❌ Random URL | `npx localtunnel --port 8080` |
| **serveo** | ✅ Free | ❌ Random URL | `ssh -R 80:localhost:8080 serveo.net` |
| **bore** | ✅ Free / open source (Rust) | ❌ Random URL | `bore local 8080 --to bore.pub` |

### 10.5 Recommendation

| Scenario | Recommendation |
|---|---|
| **Personal / small team** | Tailscale (free) + LAN mode — no relay, no cost |
| **Public service, low traffic** | Oracle Cloud Always Free (ARM) + self-hosted relay |
| **Public service, moderate traffic** | Cloudflare Tunnel (free, stable) pointing to a home relay |
| **Public service, high traffic** | Paid VPS with enough bandwidth, or ask users to self-host their own relay |
| **Testing** | `cloudflared tunnel --url …` (temporary) |

### 10.6 Open-source license

`office-rdp-rendezvous` is part of RDP Link and is released under the same
open-source license as the project. You are free to self-host, modify, and
redistribute it. See `LICENSE` and `THIRD_PARTY_NOTICES.md`.

---

## 11. Deploying the relay on Oracle Cloud (Always Free)

This is a step-by-step guide for running `office-rdp-rendezvous` on an
Oracle Cloud Always Free VM.

### 11.1 Create the VM

1. Log in to the Oracle Cloud Console.
2. Go to **Compute → Instances → Create Instance**.
3. Choose an **Always Free** shape:
   - **AMD:** `VM.Standard.E2.1.Micro` (1/8 OCPU, 1 GB RAM) — simplest.
   - **ARM:** `VM.Standard.A1.Flex` (up to 4 OCPUs, 24 GB RAM free) — more
     powerful; pick Ubuntu 22.04/24.04 (aarch64 image).
4. Add your SSH public key.
5. Under **Networking**, ensure a **public IPv4 address** is assigned.
6. Create the instance.

### 11.2 Open firewall ports

Oracle Cloud has **two** firewalls you must configure:

**A. Security List (VCN level)**

1. Go to **Networking → Virtual Cloud Networks → your VCN → Security Lists**.
2. Add **Ingress Rules**:
   - Source `0.0.0.0/0`, Protocol TCP, Port `22` (SSH)
   - Source `0.0.0.0/0`, Protocol TCP, Port `80` (HTTP, for Let's Encrypt)
   - Source `0.0.0.0/0`, Protocol TCP, Port `443` (HTTPS relay)

**B. Instance OS firewall (Ubuntu)**

SSH into the VM and run:

```sh
sudo apt update && sudo apt install -y ufw
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

### 11.3 Install the relay binary

Pick the binary matching your VM's architecture:

```sh
# AMD64 (E2.1.Micro)
wget https://github.com/.../releases/download/v0.2.1/office-rdp-linux-amd64 \
  -O /usr/local/bin/office-rdp-rendezvous
chmod +x /usr/local/bin/office-rdp-rendezvous

# ARM64 (A1.Flex)
wget https://github.com/.../releases/download/v0.2.1/office-rdp-linux-arm64 \
  -O /usr/local/bin/office-rdp-rendezvous
chmod +x /usr/local/bin/office-rdp-rendezvous
```

(Or build from source: `go build -o office-rdp-rendezvous ./cmd/rendezvous`.)

Verify:

```sh
office-rdp-rendezvous --help
```

### 11.4 Get a domain name (free options)

You need a hostname for TLS. Free options:

- **DuckDNS** (duckdns.org) — free subdomain, supports Let's Encrypt.
- **No-IP** (noip.com) — free hostname with periodic confirmation.
- **Cloudflare** — if you own a domain, free DNS + proxy.
- **Oracle's own** — not free; skip.

Point your domain's A record to the VM's public IP.

### 11.5 Set up TLS (Let's Encrypt)

The easiest way is a reverse proxy with automatic TLS. Use **Caddy**:

```sh
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | \
  sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | \
  sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy
```

Create `/etc/caddy/Caddyfile`:

```
relay.example.com {
    reverse_proxy localhost:8080
}
```

```sh
sudo systemctl restart caddy
```

Caddy automatically obtains and renews the Let's Encrypt certificate.

### 11.6 Run the relay as a systemd service

Create `/etc/systemd/system/office-rdp-rendezvous.service`:

```ini
[Unit]
Description=RDP Link rendezvous/relay server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/office-rdp-rendezvous \
  --listen :8080 \
  --public-url https://relay.example.com
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now office-rdp-rendezvous
sudo systemctl status office-rdp-rendezvous
```

### 11.7 Verify

```sh
curl -s https://relay.example.com/
# (404 is fine — the relay is responding)

# From your local machine:
office-rdp --host --port 8088 --password secret \
  --rendezvous https://relay.example.com
```

Guests then connect with the 9-digit peer ID and
`https://relay.example.com`.

### 11.8 Alternative: skip TLS on the relay, use Cloudflare Tunnel

If you don't want to manage certificates, run the relay on localhost and
expose it with a **Cloudflare named tunnel** (free, stable URL):

```sh
# On the VM
cloudflared tunnel --url http://localhost:8080
```

Or set up a named tunnel with your own domain on Cloudflare for a permanent
URL. See `docs/WAN.md` §8.

### 11.9 Cost summary (Oracle Always Free)

| Item | Cost |
|---|---|
| VM (E2.1.Micro or A1.Flex) | $0 (Always Free) |
| Public IP | $0 |
| Bandwidth (first 10 TB/month) | $0 |
| Domain (DuckDNS/No-IP) | $0 |
| TLS (Let's Encrypt via Caddy) | $0 |
| **Total** | **$0** |

> ⚠️ Always Free resources are reclaimed if idle for extended periods on some
> shapes. Keep the relay running (systemd ensures restart). Monitor usage in
> the Oracle Console to avoid unexpected charges.

---

## 12. Lightweight telemetry monitor (Oracle relay)

A tiny, password-protected web dashboard runs on the relay host to track
server health, services, and hardware resources without heavy overhead.

- **URL:** `https://monitor.130-210-56-200.sslip.io`
- **Auth:** HTTP Basic Auth (username `admin`, password set by the server
  operator — not stored in this repository).
- **Resource use:** ~12 MB RAM, negligible CPU; refreshes every 5 s.
- **Service name:** `relay-monitor` (systemd), running on `127.0.0.1:9100`
  behind Caddy.
- **Metrics shown:** CPU %, memory, disk, network rates, load average, uptime,
  process count, top processes by memory, all systemd services, key service
  statuses, and relay health.

### 12.1 What it monitors

- **Hardware:** CPU, memory, swap, disk, network RX/TX, load average, uptime.
- **Services:** every `systemd` service (filterable), plus key services
  (relay, caddy, ssh, etc.) with active/inactive status.
- **Relay:** HTTPS health check against the local relay.

### 12.2 Security

- Access is gated by Caddy `basicauth` (bcrypt-hashed password).
- The dashboard binds to `127.0.0.1` only; it is never exposed directly.
- TLS is provided by Caddy (Let's Encrypt).
- Change the password by regenerating the hash with
  `caddy hash-password` and updating `/etc/caddy/Caddyfile`, then
  `systemctl reload caddy`.

### 12.3 Resource footprint

| Component | RAM | CPU |
|---|---|---|
| `relay-monitor` (Python) | ~12 MB | < 1% |
| Caddy (TLS + auth) | ~11 MB | < 1% |
| **Total overhead** | ~23 MB | negligible |

This is intentionally minimal — no database, no agent, no external
dependencies beyond Python 3 and Caddy.
