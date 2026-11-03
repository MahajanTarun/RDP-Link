"""Main application window — a thin, fast launcher around FreeRDP."""

import os
import shutil
import subprocess
import tkinter as tk
import uuid
from tkinter import ttk, messagebox, simpledialog

from .. import APP_NAME, secrets, logutil
from ..connection import ConnectionController, shell_quote_cmd
from ..deploy import copy_setup_command, deploy_receivers
from ..profiles import ProfileStore, normalize
from .dialogs import PasswordPrompt
from .editor import ProfileEditor
from .office_panel import OfficePanel

class UbuntuRDPApp:
    def __init__(self):
        self.store = ProfileStore()
        self.controller = ConnectionController(self)
        logutil.cleanup()

        self.root = tk.Tk()
        self.root.title(APP_NAME)
        self.root.geometry("1000x720")
        self.root.minsize(880, 640)

        self._status_var = tk.StringVar(value="Ready")
        self._office_var = tk.StringVar(value="Office ID: run office-rdp --id")
        self._office_proc = None
        self._style_ui()
        self._build_ui()
        self.refresh_office_id()
        self.refresh_display_list()
        self.refresh_list()
        self.root.protocol("WM_DELETE_WINDOW", self._on_close)
        # Warm the keyboard-layout and scroll-direction caches in the
        # background so the connect path never blocks on 'defaults read',
        # 'setxkbmap' or 'gsettings'.
        from ..keyboard import warmup
        warmup()
        from ..scroll import warmup as warmup_scroll
        warmup_scroll()

    # -- styling -------------------------------------------------------------

    def _style_ui(self):
        style = ttk.Style(self.root)
        try:
            style.theme_use("clam")
        except tk.TclError:
            pass
        style.configure("Treeview", rowheight=36, font=("TkDefaultFont", 13),
                        fieldbackground="#ffffff", borderwidth=0)
        style.configure("Treeview.Heading", font=("TkDefaultFont", 11, "bold"),
                        padding=(6, 6))
        style.configure("TButton", padding=(12, 6))
        style.configure("Accent.TButton", background="#0a84ff",
                        foreground="#ffffff")
        style.map("Accent.TButton",
                  background=[("disabled", "#c9c9c9"), ("pressed", "#0066cc"),
                              ("active", "#3395ff")])
        style.configure("Danger.TButton", background="#e74c3c",
                        foreground="#ffffff")
        style.map("Danger.TButton",
                  background=[("disabled", "#c9c9c9"), ("pressed", "#b93a2d"),
                              ("active", "#f06a5d")])

    # -- UI -------------------------------------------------------------------

    def _build_ui(self):
        outer = ttk.Frame(self.root, padding=(18, 16))
        outer.pack(fill="both", expand=True)

        header = ttk.Frame(outer)
        header.pack(fill="x")
        title_box = ttk.Frame(header)
        title_box.pack(side="left")
        ttk.Label(title_box, text="🔗  RDP Link",
                  font=("TkDefaultFont", 22, "bold")).pack(anchor="w")
        ttk.Label(title_box,
                  text="Lightweight remote access",
                  foreground="#777").pack(anchor="w")
        ttk.Button(header, text="＋  Add", command=self.add_profile,
                   style="Accent.TButton").pack(side="right")
        self._office_panel = OfficePanel(outer, status_callback=self.set_status)
        self._office_panel.pack(fill="x", pady=(12, 0))
        ttk.Separator(outer).pack(fill="x", pady=(12, 10))

        # Reserve the bottom bar BEFORE packing the list: the footer is
        # anchored to the window's bottom edge, so the status bar and buttons
        # stay visible at any window size — the list shrinks instead of
        # pushing them off-screen.
        footer = ttk.Frame(outer)
        footer.pack(side="bottom", fill="x")
        ttk.Separator(outer).pack(side="bottom", fill="x", pady=(10, 10))
        self._dot_canvas = tk.Canvas(footer, width=10, height=10,
                                     highlightthickness=0)
        self._dot_canvas.pack(side="left")
        self._dot = self._dot_canvas.create_oval(1, 1, 9, 9, fill="#2ecc71",
                                                 outline="")
        ttk.Label(footer, textvariable=self._status_var,
                  foreground="#555").pack(side="left", padx=(6, 12))

        self._edit_btn = ttk.Button(footer, text="Edit…",
                                    command=self.edit_profile)
        self._edit_btn.pack(side="right")
        self._disconnect_all_btn = ttk.Button(footer, text="Disconnect All",
                                              style="Danger.TButton",
                                              command=self.disconnect_all)
        self._disconnect_all_btn.pack(side="right", padx=(0, 8))
        self._connect_btn = ttk.Button(footer, text="Connect",
                                       style="Accent.TButton",
                                       command=self.toggle_selected)
        self._connect_btn.pack(side="right", padx=(0, 8))

        cols = ("name", "target", "routing", "state")
        self.tree = ttk.Treeview(outer, columns=cols, show="headings",
                                 selectmode="browse")
        self.tree.heading("name", text="Name", anchor="w")
        self.tree.heading("target", text="Target", anchor="w")
        self.tree.heading("routing", text="Device routing", anchor="w")
        self.tree.heading("state", text="Status", anchor="w")
        self.tree.column("name", width=190, anchor="w")
        self.tree.column("target", width=230, anchor="w")
        self.tree.column("routing", width=230, anchor="w")
        self.tree.column("state", width=110, anchor="w", stretch=False)
        self.tree.tag_configure("connected", background="#e8f7ee",
                                foreground="#1a7f37")
        self.tree.pack(fill="both", expand=True)
        self.tree.bind("<Double-1>", self._on_double_click)
        self.tree.bind("<Return>", lambda e: self.toggle_selected())
        self.tree.bind("<Delete>", lambda e: self.delete_profile())
        self.tree.bind("<BackSpace>", lambda e: self.delete_profile())
        self.tree.bind("<Button-2>", self._on_context)
        self.tree.bind("<Button-3>", self._on_context)
        self.tree.bind("<<TreeviewSelect>>", lambda e: self._update_buttons())

        self._menu = tk.Menu(self.root, tearoff=0)
        self._menu.add_command(label="Connect / Disconnect",
                               command=self.toggle_selected)
        self._menu.add_command(label="Edit…", command=self.edit_profile)
        self._menu.add_command(label="Copy FreeRDP Command",
                               command=self.copy_launch_command)
        self._menu.add_command(label="Set up receivers on host…",
                               command=self.setup_host_receivers)
        self._menu.add_command(label="Stop camera & mic now",
                               command=self.stop_device_streaming)
        self._menu.add_command(label="Show session log",
                               command=logutil.open_log)
        self._menu.add_command(label="Check Permissions…",
                               command=self.check_permissions)
        self._menu.add_command(label="Office RDP: Connect to host…",
                               command=self.connect_office_host)
        self._menu.add_command(label="Office RDP: Connect by Peer ID…",
                               command=self.connect_office_peer_id)
        self._menu.add_command(label="Office RDP: Browse hosts…",
                               command=self.browse_office_hosts)
        self._menu.add_command(label="Office RDP: Open Web Guest…",
                               command=self.open_web_guest)
        self._menu.add_command(label="Office RDP: Generate pairing code",
                               command=self.generate_pairing_code)
        self._menu.add_command(label="Office RDP: Wake on LAN…",
                               command=self.wake_on_lan)
        self._menu.add_separator()
        self._menu.add_command(label="Delete", command=self.delete_profile)

    # -- routing summary -------------------------------------------------------

    def _routing_summary(self, p):
        parts = []
        label = {"audio": "spk", "mic": "mic", "camera": "cam"}
        route_disp = {"host": "local", "native": "RDP", "remote": "PC"}
        for key in ("audio", "mic", "camera"):
            parts.append(f"{label[key]}:{route_disp.get(p.get(key), '?')}")
        if p.get("clipboard"):
            parts.append("clip")
        if p.get("fileTransfer"):
            parts.append("files")
        return "  ·  ".join(parts)

    def _update_buttons(self):
        sel = self._selected()
        self._edit_btn.state(["!disabled"] if sel else ["disabled"])
        if sel:
            self._connect_btn.config(
                text="Disconnect" if self.controller.is_active(sel["id"])
                else "Connect")
            self._connect_btn.state(["!disabled"])
        else:
            self._connect_btn.state(["disabled"])
        self._disconnect_all_btn.state(
            ["!disabled"] if self.controller.any_active() else ["disabled"])

    def _selected(self):
        items = self.tree.selection()
        if not items:
            return None
        return self.store.find(items[0])

    def _on_double_click(self, event):
        profile = self._selected()
        if profile:
            self._attempt_toggle(profile)

    def _on_context(self, event):
        row = self.tree.identify_row(event.y)
        if row:
            self.tree.selection_set(row)
            self._menu.tk_popup(event.x_root, event.y_root)

    # -- data / UI sync ---------------------------------------------------------

    def refresh_list(self):
        selected = self.tree.selection()
        self.tree.delete(*self.tree.get_children())
        for p in self.store.profiles:
            active = self.controller.is_active(p["id"])
            pw = "🔑 " if p.get("savePassword") and secrets.store_has(p["id"]) else ""
            state = "● Connected" if active else ""
            chips = self.controller.bridges.device_chips(p["id"])
            if chips:
                state = f"{state} {chips}".strip()
            self.tree.insert("", "end", iid=p["id"],
                             tags=("connected",) if active else (),
                             values=(
                f'{pw}{p["name"]}',
                f'{p["username"]}  •  {p["host"]}:{p["port"]}',
                self._routing_summary(p),
                state,
            ))
        if selected and selected[0] in self.tree.get_children():
            self.tree.selection_set(selected[0])
        self._update_buttons()

    def set_status(self, text, error=False, warn=False):
        self._status_var.set(text)
        color = ("#e74c3c" if error
                 else "#f39c12" if warn
                 else "#2ecc71")
        self._dot_canvas.itemconfig(self._dot, fill=color)

    # -- actions -----------------------------------------------------------------

    def _attempt_toggle(self, profile):
        if profile.get("savePassword") and not secrets.store_has(profile["id"]):
            PasswordPrompt(self.root, profile,
                           lambda pw: self._save_pw_and_connect(profile, pw))
        else:
            self.controller.toggle(profile)

    def _save_pw_and_connect(self, profile, password):
        if secrets.store_set(profile["id"], password):
            profile["savePassword"] = True
            self.store.save()
            self.refresh_list()
            self.controller.connect(profile)
        else:
            messagebox.showerror(APP_NAME,
                                 "Couldn't save the password.")

    def toggle_selected(self):
        profile = self._selected()
        if profile:
            self._attempt_toggle(profile)

    def add_profile(self):
        ProfileEditor(self.root, {
            "id": str(uuid.uuid4()), "name": "", "host": "",
            "port": "3389", "username": "",
        }, True, self._on_editor_save, status_cb=self.set_status)

    def edit_profile(self):
        profile = self._selected()
        if profile:
            ProfileEditor(self.root, profile, False, self._on_editor_save,
                          status_cb=self.set_status)

    def _on_editor_save(self, saved, password, connect_now):
        saved = normalize(saved)
        existed = self.store.find(saved["id"]) is not None
        self.store.upsert(saved)

        if password is not None:
            if not secrets.store_set(saved["id"], password):
                messagebox.showerror(APP_NAME, "Couldn't save the password.")
        if not saved["savePassword"] and existed:
            secrets.store_delete(saved["id"])

        self.refresh_list()
        if saved["id"] in self.tree.get_children():
            self.tree.selection_set(saved["id"])
            self.tree.see(saved["id"])

        if connect_now:
            self.controller.connect(saved)

    def delete_profile(self):
        profile = self._selected()
        if not profile:
            return
        if not messagebox.askyesno(APP_NAME, f'Delete "{profile["name"]}"?'):
            return
        self.controller.disconnect(profile)
        self.controller.processes.pop(profile["id"], None)
        self.controller.bridges.stop(profile["id"])
        secrets.store_delete(profile["id"])
        self.store.remove(profile["id"])
        self.refresh_list()
        self.set_status(f'Deleted {profile["name"]}')

    def report_permission_denied(self, profile, problems):
        names = " and ".join(n for n, _ in problems)
        panes = {"Camera": "Privacy_Camera", "Microphone": "Privacy_Microphone"}
        pane = panes.get(problems[0][0], "Privacy_Microphone")
        if messagebox.askyesno(
                APP_NAME,
                f"{names} access is denied for {APP_NAME}, so the "
                f"{names.lower()} bridge for \"{profile['name']}\" can't "
                f"start.\n\nOpen System Settings → Privacy & Security to "
                f"allow it?",
                icon="warning"):
            subprocess.Popen(["open",
                              "x-apple.systempreferences:com.apple."
                              f"preference.security?{pane}"],
                             stdout=subprocess.DEVNULL,
                             stderr=subprocess.DEVNULL)

    def check_permissions(self):
        """Explicit re-check: forget cached status, then probe for real."""
        profile = self._selected()
        if profile:
            from ..permissions import reset_permission_cache
            reset_permission_cache()
            self.controller.request_permissions(profile)

    def copy_launch_command(self):
        profile = self._selected()
        if not profile:
            return
        from ..exe import find_freerdp
        exe = find_freerdp() or "sdl-freerdp"
        stdin_pass = (profile.get("savePassword")
                      and secrets.store_has(profile["id"])
                      and profile.get("stdinPass", True))
        cmd = shell_quote_cmd(exe, profile, stdin_pass=stdin_pass)
        self.root.clipboard_clear()
        self.root.clipboard_append(cmd)
        self.set_status("FreeRDP command copied (password placeholder included"
                        if stdin_pass else "FreeRDP command copied to clipboard")

    def setup_host_receivers(self):
        profile = self._selected()
        if not profile:
            return
        self.set_status(f"Opening terminal to install receivers on "
                        f"{profile['host']}…")
        if not deploy_receivers(profile):
            cmd = copy_setup_command(profile)
            self.root.clipboard_clear()
            self.root.clipboard_append(cmd)
            self.set_status("No terminal found — setup command copied instead",
                            warn=True)

    def disconnect_all(self):
        self.controller.disconnect_all()
        self.set_status("Disconnecting all…")

    def stop_device_streaming(self):
        """Instant privacy off-switch: kill camera/mic bridges but keep the
        session open."""
        profile = self._selected()
        if not profile:
            return
        pid = profile["id"]
        was_cam = self.controller.bridges.camera_procs.get(pid) is not None
        was_mic = self.controller.bridges.mic_procs.get(pid) is not None
        self.controller.bridges.stop(pid)
        self.refresh_list()
        if was_cam or was_mic:
            self.set_status(f"Stopped {'camera' if was_cam else ''}"
                            f"{' & ' if was_cam and was_mic else ''}"
                            f"{'microphone' if was_mic else ''} streaming — "
                            f"session stays open (reconnect to re-enable)")
        else:
            self.set_status("No device streaming active for this profile")

    def refresh_office_id(self):
        self._office_panel.refresh_identity()

    def refresh_display_list(self):
        self._office_panel.refresh_displays()

    def _find_office_binary(self):
        for name in ("office-rdp", "office-rdp.exe"):
            p = shutil.which(name)
            if p:
                return p
        candidates = [
            os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "..", "dist", "macos", "Office RDP.app", "Contents", "MacOS", "office-rdp"),
            "/tmp/office-rdp",
        ]
        for c in candidates:
            if os.path.exists(c):
                return c
        return None

    def connect_office_peer_id(self):
        peer_id = simpledialog.askstring("Office RDP", "Peer ID (e.g. 123 456 789):")
        rendezvous = simpledialog.askstring("Office RDP", "Rendezvous URL:")
        password = simpledialog.askstring("Office RDP", "Password (optional):", show="*")
        if not peer_id or not rendezvous:
            return
        try:
            import json, urllib.request, urllib.parse
            u = rendezvous.rstrip("/") + "/resolve?peerId=" + urllib.parse.quote(peer_id.replace(" ", ""))
            with urllib.request.urlopen(u) as r:
                info = json.load(r)
            addr = info.get("addr") or info.get("lanIp") or ""
            if addr and not addr.startswith("http"):
                addr = "http://" + addr
            if not addr:
                messagebox.showerror("Office RDP", "No address for peer id")
                return
            self._open_office_viewer(addr, password)
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))

    def _open_office_viewer(self, addr, password=None):
        viewer = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "office_viewer.py")
        if not os.path.exists(viewer):
            messagebox.showerror("Office RDP", "office_viewer.py not found")
            return
        try:
            args = ["python3", viewer, addr]
            if password:
                args += ["--password", password]
            subprocess.Popen(args)
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))

    def connect_office_host(self):
        url = simpledialog.askstring("Office RDP", "Host URL (http://ip:port):")
        if not url:
            return
        password = simpledialog.askstring("Office RDP", "Password (optional):", show="*")
        viewer = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "office_viewer.py")
        if not os.path.exists(viewer):
            messagebox.showerror("Office RDP", "office_viewer.py not found")
            return
        try:
            args = ["python3", viewer, url]
            if password:
                args += ["--password", password]
            subprocess.Popen(args)
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))

    def browse_office_hosts(self):
        url = simpledialog.askstring("Office RDP", "Rendezvous URL:")
        if not url:
            return
        try:
            import json, urllib.request
            with urllib.request.urlopen(url.rstrip("/") + "/peers") as r:
                peers = json.load(r)
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))
            return
        if not peers:
            messagebox.showinfo("Office RDP", "No hosts found.")
            return
        win = tk.Toplevel(self.root)
        win.title("Office RDP hosts")
        lb = tk.Listbox(win, width=60)
        lb.pack(fill="both", expand=True)
        for p in peers:
            lb.insert("end", f"{p.get('peerId')}  {p.get('alias')}  {p.get('addr','')}")
        def connect():
            sel = lb.curselection()
            if not sel:
                return
            line = lb.get(sel[0])
            parts = line.split()
            addr = parts[-1] if parts else ""
            if not addr or addr == "None":
                messagebox.showerror("Office RDP", "No address for host")
                return
            if not addr.startswith("http"):
                addr = "http://" + addr
            subprocess.Popen(["python3", os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "office_viewer.py"), addr])
            win.destroy()
        ttk.Button(win, text="Connect", command=connect).pack()

    def open_web_guest(self):
        url = simpledialog.askstring("Office RDP", "Rendezvous URL:")
        if not url:
            return
        import webbrowser
        webbrowser.open(url.rstrip("/") + "/client")

    def wake_on_lan(self):
        mac = simpledialog.askstring("Office RDP", "MAC (aa:bb:cc:dd:ee:ff):")
        if not mac:
            return
        binp = self._find_office_binary()
        if not binp:
            messagebox.showerror("Office RDP", "office-rdp binary not found")
            return
        try:
            subprocess.Popen([binp, "--wake", mac])
            self._status_var.set(f"Wake packet sent to {mac}")
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))

    def generate_pairing_code(self):
        binp = self._find_office_binary()
        if not binp:
            messagebox.showerror("Office RDP", "office-rdp binary not found")
            return
        try:
            out = subprocess.check_output([binp, "--pair-code"], text=True, timeout=5).strip()
            messagebox.showinfo("Office RDP", out)
        except Exception as e:
            messagebox.showerror("Office RDP", str(e))

    def stop_office_host(self):
        self._office_panel.stop_host()

    def start_office_host(self):
        self._office_panel.start_host()

    def _on_close(self):
        self.controller.disconnect_all()
        self.root.after(300, self.root.destroy)

    def run(self):
        self.root.mainloop()
