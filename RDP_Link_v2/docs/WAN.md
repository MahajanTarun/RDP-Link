# WAN access, relays and dynamic IPs

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
2. opens the outbound tunnel (`/tunnel?peerId=…&token=…`),
3. reconnects automatically with backoff if the network drops or the IP changes.

The tunnel token is derived from the peer ID and the access password
(`sha256("rdp-link-tunnel:<peerID>:<password>")`). Changing the password
requires the relay to forget the peer (restart it, since bindings are in
memory).

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
the app resolves it to the relay URL automatically.

## 4. What the relay does and does not do

| Property | Behaviour |
| --- | --- |
| NAT traversal | Outbound host tunnel; no inbound ports |
| Dynamic IP | Irrelevant: guests use the relay's stable address |
| Reconnect | Host reconnects with exponential backoff; viewers retry |
| Transport | WebSocket (`ws://` or `wss://`) carrying base64 HTTP requests |
| Request size | 32 MiB per request/response over the relay |
| Concurrency | Up to 6 in-flight requests per host |
| Auth | Host password still enforced end-to-end by the host |
| Encryption | TLS between host↔relay and guest↔relay; the relay terminates TLS |
| Multiple hosts | One tunnel per peer ID; a second connection is rejected |

**Trust model:** the relay sees request/response bodies because it terminates
TLS. Run it yourself, or use one you trust. For end-to-end encryption without
trusting the relay, use an overlay network (Tailscale/WireGuard) or the WebRTC
media path; the HTTP relay is deliberately simple.

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
  'http://192.168.1.14:18080/t/033976933/frame.jpg?password=relay1'
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
