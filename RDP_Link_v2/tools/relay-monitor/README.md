# relay-monitor — lightweight server telemetry dashboard

A ~12 MB Python 3 web dashboard that shows CPU, memory, disk, network,
processes, systemd service status, and relay health for the machine that runs
the RDP Link rendezvous/relay — or any small VPS.

## Deploy

```sh
sudo install -d /opt/relay-monitor
sudo install -m 0755 monitor.py /opt/relay-monitor/
sudo install -m 0644 relay-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now relay-monitor    # serves 127.0.0.1:9100
```

Then reverse-proxy it behind Caddy with TLS and basic auth, for example:

```
monitor.example.com {
    basicauth {
        admin <bcrypt hash from `caddy hash-password`>
    }
    reverse_proxy 127.0.0.1:9100
}
```

The dashboard listens on loopback only; TLS and authentication belong to the
reverse proxy. No database, no dependencies beyond Python 3.8.

Live instance used in this project: `https://monitor.130-210-56-200.sslip.io`
(see `docs/WAN.md` §12).
