"""Add / Edit profile dialog (devices, sharing, password, host setup)."""

import os
import re
import subprocess
import sys
import threading
import tkinter as tk
from tkinter import ttk

from .. import paths
from ..bridges import list_cameras, probe_receiver, CAMERA_CTL_PORT
from ..deploy import deploy_receivers
from ..profiles import ROUTES, ROUTE_LABELS
from .dialogs import _pw_show_toggle

PORT_RE = re.compile(r"^\d{1,5}$")


class ProfileEditor(tk.Toplevel):
    """on_save(profile, password_or_None, connect_now)"""

    def __init__(self, parent, profile, is_new, on_save, status_cb=None):
        super().__init__(parent)
        self.on_save = on_save
        self.status_cb = status_cb
        self.title("Add RDP Connection" if is_new else "Edit RDP Connection")
        self.resizable(False, False)
        self.transient(parent)
        self.grab_set()

        self.profile = dict(profile)
        self.is_new = is_new

        self._pw_var = tk.StringVar()
        self._pw_show = tk.BooleanVar()
        self._save_pw_var = tk.BooleanVar(value=profile.get("savePassword", False))
        self._camtest_var = tk.StringVar(value="")

        outer = ttk.Frame(self, padding=20)
        outer.pack(fill="both", expand=True)

        ttk.Label(outer, text=self.title(),
                  font=("TkDefaultFont", 16, "bold")).pack(anchor="w", pady=(0, 14))

        # ---- Connection ------------------------------------------------
        conn = ttk.LabelFrame(outer, text=" Connection ", padding=12)
        conn.pack(fill="x")
        for i in range(2):
            conn.columnconfigure(i, weight=1)
        self._vars = {}
        fields = [("Name", "name"), ("Host / IP", "host"),
                  ("Port", "port"), ("Username", "username")]
        for i, (label, key) in enumerate(fields):
            r, c = divmod(i, 2)
            ttk.Label(conn, text=label).grid(row=r * 2, column=c, sticky="w",
                                             pady=(0 if r == 0 else 6, 2),
                                             padx=(0 if c == 0 else 12, 0))
            var = tk.StringVar(value=str(profile.get(key, "")))
            self._vars[key] = var
            ttk.Entry(conn, textvariable=var, width=26).grid(
                row=r * 2 + 1, column=c, sticky="ew",
                padx=(0 if c == 0 else 12, 0))
        self._smart_var = tk.BooleanVar(value=profile.get("smartSizing", True))
        ttk.Checkbutton(conn, text="Smart sizing",
                        variable=self._smart_var).grid(
            row=4, column=0, columnspan=2, sticky="w", pady=(10, 0))

        # ---- Devices & sharing ------------------------------------------
        dev = ttk.LabelFrame(outer, text=" Devices & sharing ", padding=12)
        dev.pack(fill="x", pady=(12, 0))
        dev.columnconfigure(1, weight=1)

        self._route_vars = {}
        routes = [("Speaker (remote sound)", "audio"),
                  ("Microphone", "mic"),
                  ("Camera", "camera")]
        for i, (label, key) in enumerate(routes):
            ttk.Label(dev, text=label).grid(row=i, column=0, sticky="w",
                                            pady=(0 if i == 0 else 6, 2))
            allowed = ROUTES[key]
            current = profile.get(key, "host" if key != "camera" else "remote")
            if current not in allowed:
                current = allowed[0]
            values = [ROUTE_LABELS[v] for v in allowed]
            var = tk.StringVar(value=ROUTE_LABELS[current])
            self._route_vars[key] = (var, key)
            ttk.Combobox(dev, textvariable=var, values=values,
                         state="readonly", width=30).grid(
                row=i, column=1, sticky="w", pady=(0 if i == 0 else 6, 2))

        camrow = ttk.Frame(dev)
        camrow.grid(row=3, column=0, columnspan=2, sticky="w", pady=(8, 0))
        ttk.Label(camrow, text="Camera device:").pack(side="left")
        self._camdev_var = tk.StringVar(value=str(profile.get("camDevice", "0")))
        self._camdev_box = ttk.Combobox(camrow, textvariable=self._camdev_var,
                                        values=["0"], width=30)
        self._camdev_box.pack(side="left", padx=(6, 6))
        ttk.Button(camrow, text="Refresh", width=8,
                   command=self._refresh_cam_devices).pack(side="left")
        ttk.Button(camrow, text="Test…", width=8,
                   command=self._test_camera).pack(side="left", padx=(6, 0))

        self._setup_btn = ttk.Button(dev, text="Set up receivers on host…",
                                     command=self._setup_host)
        self._setup_btn.grid(row=4, column=0, columnspan=2, sticky="w",
                             pady=(6, 0))
        ttk.Label(dev, textvariable=self._camtest_var, foreground="#888"
                  ).grid(row=5, column=0, columnspan=2, sticky="w", pady=(4, 0))

        ttk.Label(dev, foreground="#888", wraplength=500, justify="left",
                  text="Routing a device to this computer: speaker needs "
                       "pipewire-module-xrdp on the host; mic/camera stream over "
                       "ffmpeg — one click above installs the receiver services "
                       "on the host (SSH + sudo password may be asked once)."
                  ).grid(row=6, column=0, columnspan=2, sticky="w", pady=(8, 0))

        # ---- Clipboard & files ------------------------------------------
        cf = ttk.LabelFrame(outer, text=" Clipboard & files ", padding=12)
        cf.pack(fill="x", pady=(12, 0))
        self._clip_var = tk.BooleanVar(value=profile.get("clipboard", True))
        self._files_var = tk.BooleanVar(value=profile.get("fileTransfer", True))
        ttk.Checkbutton(cf, text="Share clipboard (copy / paste)",
                        variable=self._clip_var).pack(anchor="w")
        ttk.Checkbutton(cf, text="Share computer files (\\tsclient\\MacFiles, "
                                 "enables file copy/paste)",
                        variable=self._files_var).pack(anchor="w", pady=(4, 0))

        # ---- Keyboard ------------------------------------------------------
        kb = ttk.LabelFrame(outer, text=" Keyboard ", padding=12)
        kb.pack(fill="x", pady=(12, 0))
        from ..keyboard import choices
        kbvalues = choices()
        current = str(profile.get("keyboard", "auto"))
        display = ("Auto-detect (current Mac layout)" if current == "auto"
                   else next((lab for v, lab in kbvalues if v == current), None))
        if display is None:            # stale/unknown stored value
            current, display = "auto", "Auto-detect (current Mac layout)"
        self._kb_var = tk.StringVar(value=display)
        labels = [lab for _, lab in kbvalues]
        ttk.Combobox(kb, textvariable=self._kb_var, state="readonly",
                     width=34,
                     values=["Auto-detect (current Mac layout)"] + labels
                     ).grid(row=0, column=0, sticky="w")
        ttk.Label(kb, foreground="#888",
                  text="Auto-detect sends your currently selected keyboard "
                       "layout (US, German, …) to the remote session at "
                       "login. Applies at session start."
                  ).grid(row=1, column=0, sticky="w", pady=(6, 0))

        # ---- Password ------------------------------------------------------
        pw = ttk.LabelFrame(outer, text=" Password ", padding=12)
        pw.pack(fill="x", pady=(12, 0))
        ttk.Checkbutton(pw, text="Save password (Keychain on macOS)",
                        variable=self._save_pw_var).pack(anchor="w")
        pwrow = ttk.Frame(pw)
        pwrow.pack(anchor="w", pady=(6, 0))
        ttk.Label(pwrow, text="Password").pack(side="left")
        self._pw_entry = tk.Entry(pwrow, textvariable=self._pw_var,
                                  show="●", width=30)
        self._pw_entry.pack(side="left", padx=(8, 4))
        ttk.Checkbutton(pwrow, text="👁", variable=self._pw_show,
                        command=lambda: _pw_show_toggle(self._pw_entry,
                                                        self._pw_show)
                        ).pack(side="left")
        ttk.Label(pw, foreground="#888",
                  text="Saved passwords connect directly — the app pipes the "
                       "password to FreeRDP via stdin; leave blank to keep the "
                       "current one.").pack(anchor="w", pady=(6, 0))

        # ---- buttons --------------------------------------------------------
        btns = ttk.Frame(outer)
        btns.pack(fill="x", pady=(16, 0))
        ttk.Button(btns, text="Cancel", command=self.destroy).pack(side="right")
        self._save_btn = ttk.Button(btns, text="Save", command=self._save)
        self._save_btn.pack(side="right", padx=(0, 8))
        self._connect_btn = ttk.Button(btns, text="Save & Connect",
                                       command=self._save_connect)
        self._connect_btn.pack(side="right", padx=(0, 8))

        self.bind("<Return>", lambda e: self._save())
        self.bind("<Escape>", lambda e: self.destroy())
        self._validate()
        for key in ("name", "host", "port", "username"):
            self._vars[key].trace_add("write", lambda *_: self._validate())
        self.update_idletasks()
        self._center_on(parent)
        self._refresh_cam_devices()

    def _center_on(self, parent):
        px, py = parent.winfo_rootx(), parent.winfo_rooty()
        pw, ph = parent.winfo_width(), parent.winfo_height()
        w, h = self.winfo_width(), self.winfo_height()
        self.geometry(f"+{max(0, px + (pw - w) // 2)}+{max(0, py + (ph - h) // 2)}")

    def _validate(self):
        ok = all(self._vars[k].get().strip() for k in ("name", "host", "username"))
        port = self._vars["port"].get().strip()
        ok = ok and (PORT_RE.match(port) is not None and 0 < int(port) < 65536)
        state = ["!disabled"] if ok else ["disabled"]
        self._save_btn.state(state)
        self._connect_btn.state(state)

    def _collect(self):
        for key in ("name", "host", "username"):
            self.profile[key] = self._vars[key].get().strip()
        port = self._vars["port"].get().strip()
        self.profile["port"] = port if (PORT_RE.match(port)
                                        and 0 < int(port) < 65536) else "3389"
        self.profile["smartSizing"] = bool(self._smart_var.get())
        for var, key in self._route_vars.values():
            label = var.get()
            for val, lab in ROUTE_LABELS.items():
                if lab == label:
                    self.profile[key] = val
                    break
        self.profile["camDevice"] = self._camdev_var.get().split("—")[0].strip() or "0"
        self.profile["clipboard"] = bool(self._clip_var.get())
        self.profile["fileTransfer"] = bool(self._files_var.get())
        self.profile["savePassword"] = bool(self._save_pw_var.get())
        current_label = self._kb_var.get()
        if current_label.startswith("Auto-detect"):
            self.profile["keyboard"] = "auto"
        else:
            from ..keyboard import choices
            value = next((v for v, lab in choices()
                          if lab == current_label), "auto")
            self.profile["keyboard"] = value
        return self._pw_var.get() or None

    def _save(self):
        password = self._collect()
        self.on_save(self.profile, password, False)
        self.destroy()

    def _save_connect(self):
        password = self._collect()
        self.on_save(self.profile, password, True)
        self.destroy()

    # -- host setup (zero-setup receiver install) ---------------------------

    def _setup_host(self):
        host = self._vars["host"].get().strip()
        if not host:
            self._camtest_var.set("Enter a host first")
            return
        self._collect()   # persist current fields into self.profile
        self._camtest_var.set(f"Opening terminal to install receivers on {host}…")
        if deploy_receivers(self.profile):
            if self.status_cb:
                self.status_cb(f"Receiver install started on {host} — "
                               f"follow the terminal window")
            self.after(1500, self._check_receiver)
        else:
            self._camtest_var.set("Couldn't open a terminal — copy the setup "
                                  "command from the README")

    def _check_receiver(self):
        host = self._vars["host"].get().strip()
        if not host:
            return
        from ..bridges import MIC_CTL_PORT
        wants_cam = self.profile.get("camera") == "host"
        wants_mic = self.profile.get("mic") == "host"
        def work():
            alive = []
            if wants_mic and probe_receiver(host, MIC_CTL_PORT):
                alive.append("mic")
            if wants_cam and probe_receiver(host, CAMERA_CTL_PORT):
                alive.append("camera")
            self.after(0, lambda: self._camtest_var.set(
                f"✓ Receivers responding on host: {', '.join(alive)}"
                if alive else
                "No receiver response yet — they appear after the install "
                "finishes in the terminal"))
        threading.Thread(target=work, daemon=True).start()

    # -- camera helpers -----------------------------------------------------

    def _refresh_cam_devices(self):
        def work():
            devs = list_cameras()
            self.after(0, lambda: self._camdev_box.config(values=devs or ["0"]))
        threading.Thread(target=work, daemon=True).start()

    def _test_camera(self):
        dev = self._camdev_var.get().split("—")[0].strip() or "0"
        self._camtest_var.set("Testing camera…")
        def work():
            import tempfile
            out = os.path.join(tempfile.gettempdir(), "rdplink-camera-test.jpg")
            from ..exe import find_ffmpeg
            ff = find_ffmpeg()
            ok, err = False, "ffmpeg not found"
            if ff:
                if sys.platform == "darwin":
                    inp = ["-f", "avfoundation", "-framerate", "30",
                           "-video_size", "1280x720", "-pixel_format",
                           "uyvy422", "-i", dev]
                else:
                    in_dev = dev if dev.startswith("/") else f"/dev/{dev}"
                    inp = ["-f", "v4l2", "-i", in_dev]
                try:
                    r = subprocess.run(
                        [ff, "-hide_banner", "-loglevel", "error"] + inp +
                        ["-frames:v", "1", "-y", out],
                        capture_output=True, text=True, timeout=20)
                    ok = r.returncode == 0 and os.path.exists(out) \
                         and os.path.getsize(out) > 0
                    err = (r.stderr or "").strip().splitlines()
                    err = err[-1] if err else f"exit {r.returncode}"
                except subprocess.TimeoutExpired:
                    err = "timed out"
                except OSError as e:
                    err = str(e)
            def done():
                if ok:
                    self._camtest_var.set(f"✓ Camera {dev} works — opening test image")
                    paths.open_path_in_gui(out)
                else:
                    self._camtest_var.set(f"✗ Camera {dev} failed: {err}")
            self.after(0, done)
        threading.Thread(target=work, daemon=True).start()
