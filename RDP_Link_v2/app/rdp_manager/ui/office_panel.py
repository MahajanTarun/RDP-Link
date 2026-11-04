"""AnyDesk-style RDP Link panel.

One card for this device (id, addresses, password, sharing controls) and one
for connecting to a remote device. Kept independent of the RDP profile list so
the two features can evolve separately.
"""

import json
import os
import secrets
import shutil
import socket
import subprocess
import sys
import threading
import tkinter as tk
import urllib.parse
import urllib.request
from tkinter import messagebox, simpledialog, ttk

from ..viewer_launch import discard_password_file, viewer_argv

CONFIG_DIR = os.path.join(os.path.expanduser("~"), ".config", "office-rdp")
GUI_CONFIG = os.path.join(CONFIG_DIR, "gui.json")
DEFAULT_PORT = 8088


def find_office_binary():
    for name in ("office-rdp", "office-rdp.exe"):
        path = shutil.which(name)
        if path:
            return path
    here = os.path.dirname(os.path.abspath(__file__))
    app_root = os.path.dirname(os.path.dirname(here))
    repo_root = os.path.dirname(app_root)
    # Only probe the build tree for the platform we are running on: the repo
    # may also hold Windows/ Linux artifacts that cannot execute here.
    candidates = []
    if sys.platform == "win32":
        candidates.append(os.path.join(repo_root, "dist", "windows", "office-rdp.exe"))
    elif sys.platform == "darwin":
        candidates.append(os.path.join(
            repo_root, "dist", "macos", "RDP Link.app", "Contents", "MacOS", "office-rdp"))
    else:
        candidates.append(os.path.join(repo_root, "dist", "linux", "bin", "office-rdp-linux"))
    candidates += [
        os.path.join(repo_root, "office-rdp"),
        "/tmp/office-rdp",
    ]
    # Installed bundles keep the agent next to the launcher executable.
    if getattr(sys, "frozen", False):
        candidates.insert(0, os.path.join(os.path.dirname(sys.executable), "office-rdp.exe"))
        candidates.insert(0, os.path.join(os.path.dirname(sys.executable), "office-rdp"))
    for candidate in candidates:
        if os.path.exists(candidate):
            return candidate
    return None


def _load_config():
    try:
        with open(GUI_CONFIG) as fh:
            return json.load(fh)
    except Exception:
        return {}


def write_password_file(password):
    """Store the access password in a 0600 file and return its path."""
    os.makedirs(CONFIG_DIR, mode=0o700, exist_ok=True)
    try:
        os.chmod(CONFIG_DIR, 0o700)
    except OSError:
        pass
    path = os.path.join(CONFIG_DIR, "host-password")
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write(password + "\n")
    try:
        os.chmod(path, 0o600)
    except OSError:
        pass
    return path


def _save_config(data):
    os.makedirs(CONFIG_DIR, mode=0o700, exist_ok=True)
    try:
        # Passwords belong in the private password file, never in the general
        # launcher settings JSON. Migrate a legacy key out if one remains.
        safe_data = dict(data)
        safe_data.pop("password", None)
        temp = GUI_CONFIG + ".tmp"
        fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as fh:
            json.dump(safe_data, fh)
        os.replace(temp, GUI_CONFIG)
        try:
            os.chmod(GUI_CONFIG, 0o600)
        except OSError:
            pass
    except Exception:
        pass


class OfficePanel(ttk.Frame):
    def __init__(self, parent, status_callback=None):
        super().__init__(parent)
        self.status_callback = status_callback or (lambda *a, **k: None)
        self.config_data = _load_config()
        self.host_proc = None
        legacy_password = self.config_data.pop("password", None)
        password_file = os.path.join(CONFIG_DIR, "host-password")
        try:
            with open(password_file) as fh:
                saved_password = fh.read().strip()
        except OSError:
            saved_password = ""
        if not saved_password and legacy_password:
            saved_password = str(legacy_password)
            write_password_file(saved_password)
        if legacy_password is not None:
            # Remove a password left in an older plaintext gui.json.
            _save_config(self.config_data)
        try:
            saved_port = int(self.config_data.get("port", DEFAULT_PORT))
        except (TypeError, ValueError):
            saved_port = DEFAULT_PORT
        if not 1 <= saved_port <= 65535:
            saved_port = DEFAULT_PORT
        self.port = tk.IntVar(value=saved_port)
        self.peer_id = tk.StringVar(value="—")
        self.lan_addr = tk.StringVar(value="Not sharing")
        self.wan_addr = tk.StringVar(value="—")
        self.password = tk.StringVar(value=saved_password or self._random_password())
        self.display = tk.StringVar(value="0")
        self.rendezvous = tk.StringVar(value=self.config_data.get("rendezvous", ""))
        self.target = tk.StringVar(value="")
        self.state_text = tk.StringVar(value="Idle — not sharing this screen")
        self.recent = list(self.config_data.get("recent", []))
        self._style()
        self._build()
        self.refresh_identity()
        self.refresh_displays()

    # -- styling -------------------------------------------------------------
    def _style(self):
        style = ttk.Style(self)
        style.configure("Card.TFrame", background="#ffffff")
        style.configure("Card.TLabel", background="#ffffff", foreground="#1f1f1f")
        style.configure("CardMuted.TLabel", background="#ffffff", foreground="#595959",
                        font=("TkDefaultFont", 11))
        style.configure("CardTitle.TLabel", background="#ffffff", foreground="#1f1f1f",
                        font=("TkDefaultFont", 13, "bold"))
        style.configure("Peer.TLabel", background="#ffffff", foreground="#1f1f1f",
                        font=("TkFixedFont", 26, "bold"))
        style.configure("Primary.TButton", padding=(15, 6),
                        background="#1677ff", foreground="#ffffff",
                        bordercolor="#1677ff", relief="solid", borderwidth=1)
        style.map("Primary.TButton",
                  background=[("disabled", "#f5f5f5"), ("pressed", "#0958d9"),
                              ("active", "#4096ff")],
                  foreground=[("disabled", "#bfbfbf")],
                  bordercolor=[("disabled", "#d9d9d9")])
        style.configure("Ghost.TButton", padding=(11, 4), relief="solid",
                        borderwidth=1, background="#ffffff", foreground="#1f1f1f",
                        bordercolor="#d9d9d9")
        style.map("Ghost.TButton",
                  foreground=[("active", "#1677ff")],
                  bordercolor=[("active", "#4096ff")],
                  background=[("pressed", "#f5f5f5"), ("active", "#ffffff")])

    def _card(self, parent, title):
        outer = tk.Frame(parent, bg="#ffffff", highlightbackground="#f0f0f0",
                         highlightthickness=1)
        inner = ttk.Frame(outer, style="Card.TFrame", padding=(20, 16))
        inner.pack(fill="both", expand=True)
        ttk.Label(inner, text=title, style="CardTitle.TLabel").pack(anchor="w")
        return outer, inner

    # -- layout --------------------------------------------------------------
    def _build(self):
        self.columnconfigure(0, weight=1, uniform="col")
        self.columnconfigure(1, weight=1, uniform="col")
        self._build_this_device()
        self._build_remote()

    def _build_this_device(self):
        card, body = self._card(self, "This device")
        card.grid(row=0, column=0, sticky="nsew", padx=(0, 6))

        ttk.Label(body, text="RDP Link ID", style="CardMuted.TLabel").pack(anchor="w", pady=(8, 0))
        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x")
        ttk.Label(row, textvariable=self.peer_id, style="Peer.TLabel").pack(side="left")
        ttk.Button(row, text="Copy", style="Ghost.TButton",
                   command=lambda: self._copy(self.peer_id.get(), "Peer ID")).pack(side="right")

        ttk.Label(body, text="LAN", style="CardMuted.TLabel").pack(anchor="w", pady=(8, 0))
        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x")
        ttk.Label(row, textvariable=self.lan_addr, style="Card.TLabel").pack(side="left")
        ttk.Button(row, text="Copy", style="Ghost.TButton",
                   command=lambda: self._copy(self.lan_addr.get(), "LAN address")).pack(side="right")

        ttk.Label(body, text="WAN", style="CardMuted.TLabel").pack(anchor="w", pady=(6, 0))
        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x")
        ttk.Label(row, textvariable=self.wan_addr, style="Card.TLabel").pack(side="left")
        ttk.Button(row, text="Copy", style="Ghost.TButton",
                   command=lambda: self._copy(self.wan_addr.get(), "WAN address")).pack(side="right")

        tk.Frame(body, bg="#f0f0f0", height=1).pack(fill="x", pady=12)

        ttk.Label(body, text="Access password", style="CardMuted.TLabel").pack(anchor="w")
        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x", pady=(2, 0))
        ttk.Entry(row, textvariable=self.password, show="•").pack(side="left", fill="x", expand=True)
        ttk.Button(row, text="New", style="Ghost.TButton",
                   command=lambda: self.password.set(self._random_password())).pack(side="left", padx=(6, 0))

        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x", pady=(8, 0))
        ttk.Label(row, text="Display", style="CardMuted.TLabel").pack(side="left")
        self.display_box = ttk.Combobox(row, textvariable=self.display, width=3, state="readonly")
        self.display_box.pack(side="left", padx=(6, 12))
        ttk.Label(row, text="Port", style="CardMuted.TLabel").pack(side="left")
        ttk.Entry(row, textvariable=self.port, width=7).pack(side="left", padx=(6, 0))

        ttk.Label(body, text="Rendezvous (optional)", style="CardMuted.TLabel").pack(anchor="w", pady=(8, 0))
        ttk.Entry(body, textvariable=self.rendezvous).pack(fill="x", pady=(2, 0))

        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x", pady=(10, 0))
        self.start_btn = ttk.Button(row, text="Start sharing", style="Primary.TButton",
                                    command=self.start_host)
        self.start_btn.pack(side="left")
        self.stop_btn = ttk.Button(row, text="Stop", style="Ghost.TButton", command=self.stop_host)
        self.stop_btn.pack(side="left", padx=(6, 0))
        self.stop_btn.state(["disabled"])

        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x", pady=(8, 0))
        self.dot = tk.Canvas(row, width=10, height=10, highlightthickness=0, bg="#ffffff")
        self.dot.pack(side="left")
        self.dot_id = self.dot.create_oval(1, 1, 9, 9, fill="#94a3b8", outline="")
        ttk.Label(row, textvariable=self.state_text, style="CardMuted.TLabel").pack(side="left", padx=(6, 0))

    def _build_remote(self):
        card, body = self._card(self, "Connect to a device")
        card.grid(row=0, column=1, sticky="nsew", padx=(6, 0))

        ttk.Label(body, text="Address or peer ID", style="CardMuted.TLabel").pack(anchor="w", pady=(8, 0))
        ttk.Entry(body, textvariable=self.target).pack(fill="x", pady=(2, 0))
        ttk.Label(body, text="Password", style="CardMuted.TLabel").pack(anchor="w", pady=(8, 0))
        self.connect_password = tk.StringVar(value="")
        ttk.Entry(body, textvariable=self.connect_password, show="•").pack(fill="x", pady=(2, 0))
        ttk.Button(body, text="Connect", style="Primary.TButton",
                   command=self.connect).pack(anchor="w", pady=(10, 0))

        ttk.Label(body, text="Recent devices", style="CardMuted.TLabel").pack(anchor="w", pady=(12, 0))
        self.recent_box = tk.Listbox(body, height=4, activestyle="none",
                                     borderwidth=0, highlightthickness=1,
                                     highlightbackground="#d9d9d9",
                                     background="#ffffff", foreground="#1f1f1f",
                                     selectbackground="#e6f4ff",
                                     selectforeground="#1f1f1f",
                                     font=("TkDefaultFont", 12))
        self.recent_box.pack(fill="both", expand=True, pady=(2, 0))
        self.recent_box.bind("<Double-1>", lambda e: self.connect())
        self._refresh_recent()

        row = ttk.Frame(body, style="Card.TFrame")
        row.pack(fill="x", pady=(8, 0))
        ttk.Button(row, text="Pairing code", style="Ghost.TButton",
                   command=self.show_pairing_code).pack(side="left")
        ttk.Button(row, text="Wake on LAN…", style="Ghost.TButton",
                   command=self.wake_on_lan).pack(side="left", padx=(6, 0))
        ttk.Button(row, text="Unattended…", style="Ghost.TButton",
                   command=self.unattended_access).pack(side="left", padx=(6, 0))

    # -- identity / displays --------------------------------------------------
    def refresh_identity(self):
        binp = find_office_binary()
        if not binp:
            self.peer_id.set("office-rdp not found")
            return
        try:
            out = subprocess.check_output([binp, "--id"], text=True, timeout=5).strip()
            if out:
                self.peer_id.set(out.splitlines()[-1])
        except Exception:
            self.peer_id.set("unavailable")

    def refresh_displays(self):
        binp = find_office_binary()
        if not binp:
            return
        try:
            values = subprocess.check_output([binp, "--displays"], text=True, timeout=5).split()
        except Exception:
            values = ["0"]
        values = values or ["0"]
        self.display_box.configure(values=values)
        if self.display.get() not in values:
            self.display.set(values[0])

    # -- host ----------------------------------------------------------------
    def start_host(self):
        binp = find_office_binary()
        if not binp:
            messagebox.showerror("RDP Link", "office-rdp binary not found. Build it first.")
            return
        if self.host_proc is not None and self.host_proc.poll() is None:
            self._set_state("Already sharing", "#f59e0b")
            return
        password = self.password.get().strip()
        if not password:
            messagebox.showerror("RDP Link", "Set an access password before sharing.")
            return
        try:
            port = int(self.port.get())
        except (ValueError, tk.TclError):
            messagebox.showerror("RDP Link", "Port must be a number from 1 to 65535.")
            return
        if not 1 <= port <= 65535:
            messagebox.showerror("RDP Link", "Port must be between 1 and 65535.")
            return
        self.config_data.update({
            "port": port,
            "rendezvous": self.rendezvous.get().strip(),
        })
        _save_config(self.config_data)
        # The password goes into a 0600 file instead of the command line, so
        # it does not show up in the process list.
        password_file = write_password_file(password)
        args = [binp, "--host", "--port", str(port),
                "--display", str(self.display.get() or "0"),
                "--password-file", password_file]
        if self.rendezvous.get().strip():
            args += ["--rendezvous", self.rendezvous.get().strip()]
        try:
            # Log to a private file instead of an undrained pipe; a pipe that
            # nobody reads would eventually block the host process.
            log_path = os.path.join(CONFIG_DIR, "host.log")
            log_fd = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
            try:
                self.host_proc = subprocess.Popen(
                    args, stdout=log_fd, stderr=subprocess.STDOUT)
            finally:
                os.close(log_fd)
        except Exception as exc:
            messagebox.showerror("RDP Link", str(exc))
            return
        self.start_btn.state(["disabled"])
        self.stop_btn.state(["!disabled"])
        self._set_state("Starting…", "#f59e0b")
        threading.Thread(target=self._probe_host,
                         args=(port, password, self.host_proc),
                         daemon=True).start()

    def _probe_host(self, port, password, host_proc):
        import time

        for _ in range(20):
            time.sleep(0.4)
            if host_proc is None or host_proc.poll() is not None:
                self.after(0, lambda: self._set_state("Host exited", "#ef4444"))
                return
            try:
                request = urllib.request.Request(
                    f"http://127.0.0.1:{port}/info",
                    headers={"X-Office-Password": password})
                with urllib.request.urlopen(request, timeout=2) as response:
                    info = json.load(response)
                lan = f"{info.get('lanIp', '')}:{info.get('port', port)}"
                wan = f"{info.get('wanIp', '')}:{info.get('port', port)}"
                self.after(0, lambda: self._host_ready(lan, wan))
                return
            except Exception:
                continue
        self.after(0, lambda: self._set_state("Started (no local probe)", "#f59e0b"))

    def _host_ready(self, lan, wan):
        self.lan_addr.set(lan)
        self.wan_addr.set(wan)
        self._set_state("Sharing this screen", "#22c55e")
        self.status_callback(f"RDP Link sharing on {lan}")

    def stop_host(self):
        if self.host_proc is not None and self.host_proc.poll() is None:
            self.host_proc.terminate()
            try:
                self.host_proc.wait(timeout=5)
            except Exception:
                self.host_proc.kill()
        self.host_proc = None
        self.lan_addr.set("Not sharing")
        self.wan_addr.set("—")
        self.start_btn.state(["!disabled"])
        self.stop_btn.state(["disabled"])
        self._set_state("Idle — not sharing this screen", "#94a3b8")
        self.status_callback("RDP Link sharing stopped")

    def _set_state(self, text, color):
        self.state_text.set(text)
        self.dot.itemconfigure(self.dot_id, fill=color)

    # -- guest ---------------------------------------------------------------
    def connect(self):
        target = self.target.get().strip()
        if not target:
            selection = self.recent_box.curselection()
            if selection:
                target = self.recent_box.get(selection[0]).split("  —  ")[-1]
        if not target:
            messagebox.showerror("RDP Link", "Enter an address or peer ID.")
            return
        # Never fall back to this device's own sharing password: the remote
        # host has a different credential, and sending the wrong one only
        # produces a confusing 401.
        password = self.connect_password.get().strip()
        if not password:
            password = simpledialog.askstring(
                "RDP Link", "Access password on the remote device:",
                show="*", parent=self) or ""
            if not password:
                return
        if target.replace(" ", "").isdigit() and len(target.replace(" ", "")) == 9:
            target = self._resolve_peer(target)
            if not target:
                return
        if not target.startswith("http"):
            target = "http://" + target
        self._remember(target)
        self._open_viewer(target, password)

    def _resolve_peer(self, peer_id):
        rendezvous = self.rendezvous.get().strip()
        if not rendezvous:
            rendezvous = simpledialog.askstring(
                "RDP Link", "Rendezvous URL to resolve this peer ID:", parent=self
            )
            if not rendezvous:
                return None
            self.rendezvous.set(rendezvous)
        try:
            url = rendezvous.rstrip("/") + "/resolve?peerId=" + urllib.parse.quote(peer_id.replace(" ", ""))
            with urllib.request.urlopen(url, timeout=5) as response:
                info = json.load(response)
        except Exception as exc:
            messagebox.showerror("RDP Link", f"Could not resolve peer: {exc}")
            return None
        addr = info.get("addr") or info.get("lanIp") or ""
        relay = info.get("url") or ""

        def reachable(value):
            if not value:
                return False
            host, _, port = value.partition(":")
            try:
                port = int(port or 80)
            except ValueError:
                return False
            if host in ("", "0.0.0.0"):
                return False
            try:
                with socket.create_connection((host, port), timeout=0.6):
                    return True
            except OSError:
                return False

        # Same LAN is always faster; fall back to the relay for WAN and
        # changing public IPs.
        if reachable(addr):
            return addr
        if relay:
            return relay
        if addr:
            return addr
        messagebox.showerror("RDP Link", "Rendezvous returned no address for this peer.")
        return None

    def _open_viewer(self, address, password):
        argv = viewer_argv(address, password)
        try:
            subprocess.Popen(argv)
        except Exception as exc:
            discard_password_file(argv)
            messagebox.showerror("RDP Link", f"Could not open viewer: {exc}")
            return
        self.status_callback(f"Opening viewer for {address}")

    def _remember(self, address):
        if address in self.recent:
            self.recent.remove(address)
        self.recent.insert(0, address)
        self.recent = self.recent[:8]
        self.config_data["recent"] = self.recent
        _save_config(self.config_data)
        self._refresh_recent()

    def _refresh_recent(self):
        self.recent_box.delete(0, tk.END)
        for item in self.recent:
            self.recent_box.insert(tk.END, item)

    # -- extra actions -------------------------------------------------------
    def show_pairing_code(self):
        binp = find_office_binary()
        if not binp:
            return
        try:
            out = subprocess.check_output([binp, "--pair-code"], text=True, timeout=5).strip()
            messagebox.showinfo("RDP Link pairing code", out or "No code returned")
        except Exception as exc:
            messagebox.showerror("RDP Link", str(exc))

    def wake_on_lan(self):
        mac = simpledialog.askstring("RDP Link", "MAC address (aa:bb:cc:dd:ee:ff):", parent=self)
        if not mac:
            return
        binp = find_office_binary()
        if not binp:
            return
        try:
            subprocess.Popen([binp, "--wake", mac])
            self.status_callback(f"Wake packet sent to {mac}")
        except Exception as exc:
            messagebox.showerror("RDP Link", str(exc))

    def unattended_access(self):
        binp = find_office_binary()
        if not binp:
            return
        if not messagebox.askyesno(
            "RDP Link",
            "Install the host agent as a background service so this device can be "
            "reached without the app open?",
        ):
            return
        try:
            out = subprocess.check_output([binp, "--install-service"], text=True,
                                          stderr=subprocess.STDOUT, timeout=30)
            messagebox.showinfo("RDP Link", out or "Service installed.")
        except Exception as exc:
            messagebox.showerror("RDP Link", str(exc))

    # -- helpers -------------------------------------------------------------
    def _copy(self, value, label):
        if not value or value in ("—", "Not sharing"):
            return
        self.clipboard_clear()
        self.clipboard_append(value)
        self.status_callback(f"Copied {label}")

    def _random_password(self):
        return secrets.token_urlsafe(24)
