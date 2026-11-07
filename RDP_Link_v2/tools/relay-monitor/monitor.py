#!/usr/bin/env python3
"""Lightweight telemetry dashboard for the relay VPS.

Serves a small HTML page + JSON API on 127.0.0.1:9100. Intended to sit
behind a reverse proxy (Caddy) that provides TLS and basic auth.
"""

import http.server
import json
import os
import re
import shutil
import socket
import ssl
import subprocess
import threading
import time
import urllib.request

HOST = "127.0.0.1"
PORT = 9100
REFRESH = 2.0

state = {}
lock = threading.Lock()
prev_cpu = None
prev_net = None
prev_proc_cpu = {}
prev_time = None


def read_file(path):
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return ""


def cpu_times():
    line = read_file("/proc/stat").splitlines()[0] if read_file("/proc/stat") else ""
    parts = line.split()
    if not parts or parts[0] != "cpu":
        return None
    vals = [int(x) for x in parts[1:]]
    idle = vals[3] + (vals[4] if len(vals) > 4 else 0)
    total = sum(vals)
    return total, idle


def mem_info():
    info = {}
    for line in read_file("/proc/meminfo").splitlines():
        k, v = line.split(":", 1)
        info[k] = int(v.split()[0])  # kB
    total = info.get("MemTotal", 0)
    avail = info.get("MemAvailable", info.get("MemFree", 0))
    used = total - avail
    swap_total = info.get("SwapTotal", 0)
    swap_free = info.get("SwapFree", 0)
    return {
        "total_mb": round(total / 1024, 1),
        "used_mb": round(used / 1024, 1),
        "available_mb": round(avail / 1024, 1),
        "percent": round(used / total * 100, 1) if total else 0,
        "swap_total_mb": round(swap_total / 1024, 1),
        "swap_used_mb": round((swap_total - swap_free) / 1024, 1),
    }


def disk_info():
    u = shutil.disk_usage("/")
    return {
        "total_gb": round(u.total / 1e9, 1),
        "used_gb": round(u.used / 1e9, 1),
        "free_gb": round(u.free / 1e9, 1),
        "percent": round(u.used / u.total * 100, 1) if u.total else 0,
    }


def net_bytes():
    rx = tx = 0
    for line in read_file("/proc/net/dev").splitlines()[2:]:
        parts = line.split()
        if len(parts) < 10:
            continue
        if parts[0].startswith(("lo:",)):
            continue
        rx += int(parts[1])
        tx += int(parts[9])
    return rx, tx


def proc_list():
    procs = []
    for name in os.listdir("/proc"):
        if not name.isdigit():
            continue
        pid = int(name)
        stat = read_file(f"/proc/{pid}/stat")
        if not stat:
            continue
        # comm may contain spaces in parens; parse carefully
        m = re.match(r"(\d+)\s+\((.*)\)\s+(.*)", stat)
        if not m:
            continue
        pid_s, comm, rest = m.groups()
        fields = rest.split()
        try:
            utime = int(fields[11])
            stime = int(fields[12])
            rss_pages = int(fields[21])
        except (IndexError, ValueError):
            continue
        procs.append((pid, comm, utime + stime, rss_pages))
    return procs


def services():
    try:
        out = subprocess.check_output(
            ["systemctl", "list-units", "--type=service", "--all",
             "--no-pager", "--no-legend"],
            text=True, stderr=subprocess.DEVNULL, timeout=10,
        )
    except Exception:
        return []
    rows = []
    for line in out.splitlines():
        parts = line.split(None, 4)
        if len(parts) < 4:
            continue
        name, load, active, sub = parts[0], parts[1], parts[2], parts[3]
        desc = parts[4] if len(parts) > 4 else ""
        rows.append({
            "name": name,
            "load": load,
            "active": active,
            "sub": sub,
            "desc": desc,
        })
    return rows


def key_services():
    names = ["office-rdp-rendezvous", "caddy", "ssh", "systemd-journald",
             "systemd-networkd", "systemd-resolved", "cron"]
    result = []
    for n in names:
        try:
            st = subprocess.check_output(
                ["systemctl", "is-active", n],
                text=True, stderr=subprocess.DEVNULL, timeout=5,
            ).strip()
        except Exception:
            st = "unknown"
        result.append({"name": n, "status": st})
    return result


def relay_health():
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    try:
        req = urllib.request.Request("https://127.0.0.1:443/", method="GET")
        with urllib.request.urlopen(req, timeout=3, context=ctx) as r:
            code = r.getcode()
        return {"ok": True, "http_code": code}
    except Exception as e:
        return {"ok": False, "error": str(e)}


def update():
    global prev_cpu, prev_net, prev_proc_cpu, prev_time
    now = time.time()
    # CPU
    ct = cpu_times()
    cpu_pct = 0.0
    if ct and prev_cpu:
        dt = ct[0] - prev_cpu[0]
        di = ct[1] - prev_cpu[1]
        if dt > 0:
            cpu_pct = round((1 - di / dt) * 100, 1)
    prev_cpu = ct

    # Network rate
    rx, tx = net_bytes()
    rx_rate = tx_rate = 0.0
    if prev_net and prev_time:
        dt = now - prev_time
        if dt > 0:
            rx_rate = round((rx - prev_net[0]) / dt / 1024, 1)
            tx_rate = round((tx - prev_net[1]) / dt / 1024, 1)
    prev_net = (rx, tx)
    prev_time = now

    # Top processes by RSS memory
    procs = proc_list()
    top_mem = sorted(procs, key=lambda p: p[3], reverse=True)[:8]
    top_mem = [{"pid": p[0], "name": p[1], "rss_mb": round(p[3] * 4096 / 1e6, 1)}
               for p in top_mem]

    # Services (cached-ish: recompute every cycle, cheap enough)
    svc = services()

    with lock:
        state.clear()
        state.update({
            "timestamp": time.strftime("%Y-%m-%d %H:%M:%S"),
            "hostname": socket.gethostname(),
            "uptime": read_file("/proc/uptime").split()[0] if read_file("/proc/uptime") else "?",
            "loadavg": os.getloadavg(),
            "cpu_percent": cpu_pct,
            "memory": mem_info(),
            "disk": disk_info(),
            "network": {
                "rx_total_mb": round(rx / 1e6, 1),
                "tx_total_mb": round(tx / 1e6, 1),
                "rx_rate_kbps": rx_rate,
                "tx_rate_kbps": tx_rate,
            },
            "process_count": len(procs),
            "top_memory": top_mem,
            "services": svc,
            "key_services": key_services(),
            "relay": relay_health(),
        })


def loop():
    while True:
        try:
            update()
        except Exception as e:
            with lock:
                state["error"] = str(e)
        time.sleep(REFRESH)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api":
            with lock:
                data = dict(state)
            body = json.dumps(data).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path == "/":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(HTML.encode())
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *args):
        pass


HTML = r"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Relay Monitor</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; background: #f5f5f5; color: #1f1f1f; }
  header { background: #fff; padding: 16px 24px; border-bottom: 1px solid #e0e0e0; }
  h1 { margin: 0; font-size: 20px; }
  .sub { color: #595959; font-size: 13px; margin-top: 4px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; padding: 16px 24px; }
  .card { background: #fff; border-radius: 8px; padding: 14px; box-shadow: 0 1px 3px rgba(0,0,0,.06); }
  .card h3 { margin: 0 0 8px; font-size: 13px; color: #595959; text-transform: uppercase; }
  .val { font-size: 24px; font-weight: 700; }
  .bar { height: 6px; background: #eee; border-radius: 3px; margin-top: 8px; overflow: hidden; }
  .bar > div { height: 100%; background: #1677ff; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { padding: 6px 8px; border-bottom: 1px solid #f0f0f0; text-align: left; }
  th { color: #595959; font-weight: 600; }
  .ok { color: #389e0d; font-weight: 600; }
  .bad { color: #cf1322; font-weight: 600; }
  section { padding: 0 24px 16px; }
  h2 { font-size: 16px; margin: 16px 0 8px; }
  .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
</style>
</head>
<body>
<header>
  <h1>Relay Monitor</h1>
  <div class="sub" id="meta">Loading…</div>
</header>
<div class="grid" id="cards"></div>
<section>
  <h2>Key Services</h2>
  <table id="keysrv"><thead><tr><th>Service</th><th>Status</th></tr></thead><tbody></tbody></table>
</section>
<section>
  <h2>Relay Health</h2>
  <div id="relay">Checking…</div>
</section>
<section>
  <h2>Top Processes (by memory)</h2>
  <table id="procs"><thead><tr><th>PID</th><th>Name</th><th>RSS (MB)</th></tr></thead><tbody></tbody></table>
</section>
<section>
  <h2>All Services</h2>
  <input id="filter" placeholder="Filter…" style="padding:6px 10px;border:1px solid #d9d9d9;border-radius:6px;width:260px;margin-bottom:8px;">
  <table id="svcs"><thead><tr><th>Name</th><th>Load</th><th>Active</th><th>Sub</th><th>Description</th></tr></thead><tbody></tbody></table>
</section>
<script>
async function refresh() {
  try {
    const r = await fetch('/api', { cache: 'no-store' });
    const d = await r.json();
    document.getElementById('meta').textContent =
      d.hostname + ' · up ' + d.uptime + 's · load ' + d.loadavg.map(x=>x.toFixed(2)).join(', ') +
      ' · ' + d.timestamp;
    const cards = [
      ['CPU', d.cpu_percent + '%', d.cpu_percent],
      ['Memory', d.memory.used_mb + ' / ' + d.memory.total_mb + ' MB', d.memory.percent],
      ['Disk (/)', d.disk.used_gb + ' / ' + d.disk.total_gb + ' GB', d.disk.percent],
      ['Network RX', d.network.rx_rate_kbps + ' KB/s', null],
      ['Network TX', d.network.tx_rate_kbps + ' KB/s', null],
      ['Processes', d.process_count, null],
    ];
    const c = document.getElementById('cards');
    c.innerHTML = cards.map(([t,v,p]) =>
      `<div class="card"><h3>${t}</h3><div class="val">${v}</div>` +
      (p !== null ? `<div class="bar"><div style="width:${Math.min(p,100)}%"></div></div>` : '') +
      `</div>`).join('');
    const ks = document.getElementById('keysrv').querySelector('tbody');
    ks.innerHTML = d.key_services.map(s =>
      `<tr><td>${s.name}</td><td class="${s.status==='active'?'ok':'bad'}">${s.status}</td></tr>`).join('');
    const relay = document.getElementById('relay');
    relay.innerHTML = d.relay.ok
      ? `<span class="ok">OK</span> (HTTP ${d.relay.http_code})`
      : `<span class="bad">FAIL</span> ${d.relay.error || ''}`;
    const pt = document.getElementById('procs').querySelector('tbody');
    pt.innerHTML = d.top_memory.map(p =>
      `<tr><td>${p.pid}</td><td>${p.name}</td><td>${p.rss_mb}</td></tr>`).join('');
    const filter = document.getElementById('filter').value.toLowerCase();
    const st = document.getElementById('svcs').querySelector('tbody');
    st.innerHTML = d.services
      .filter(s => !filter || s.name.toLowerCase().includes(filter) || (s.desc||'').toLowerCase().includes(filter))
      .map(s => `<tr><td>${s.name}</td><td>${s.load}</td><td class="${s.active==='active'?'ok':''}">${s.active}</td><td>${s.sub}</td><td>${s.desc}</td></tr>`).join('');
  } catch (e) { console.error(e); }
}
document.getElementById('filter').addEventListener('input', refresh);
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>"""

if __name__ == "__main__":
    threading.Thread(target=loop, daemon=True).start()
    # seed initial state
    time.sleep(0.5)
    http.server.ThreadingHTTPServer.allow_reuse_address = True
    httpd = http.server.ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"monitor listening on {HOST}:{PORT}", flush=True)
    httpd.serve_forever()
