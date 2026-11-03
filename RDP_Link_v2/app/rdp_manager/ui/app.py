"""Main application window — a thin, fast launcher around FreeRDP."""

import os
import shutil
import subprocess
import tkinter as tk
import uuid
from tkinter import ttk, messagebox, simpledialog

from .. import APP_NAME, secrets, logutil
from ..logo import LOGO_PNG
from ..connection import ConnectionController, shell_quote_cmd
from ..deploy import copy_setup_command, deploy_receivers
from ..profiles import ProfileStore, normalize
from ..viewer_launch import viewer_argv
from .dialogs import PasswordPrompt
from .editor import ProfileEditor
from .office_panel import OfficePanel
from .setup import SetupDialog

class UbuntuRDPApp:
    def __init__(self):
        self.store = ProfileStore()
        self.controller = ConnectionController(self)
        logutil.cleanup()

        self.root = tk.Tk()
        self.root.title(APP_NAME)
        self.root.geometry("1040x860")
        self.root.minsize(920, 700)

        self._status_var = tk.StringVar(value="Ready")
        self._office_var = tk.StringVar(value="RDP Link ID: run office-rdp --id")
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
        """Ant Design 5 light tokens mapped onto ttk."""
        style = ttk.Style(self.root)
        try:
            style.theme_use("clam")
        except tk.TclError:
            pass

        bg, surface = "#f5f5f5", "#ffffff"
        border, split = "#d9d9d9", "#f0f0f0"
        text, secondary = "#1f1f1f", "#595959"
        primary = "#1677ff"

        self.root.configure(bg=bg)
        style.configure(".", background=bg, foreground=text,
                        font=("TkDefaultFont", 12))
        style.configure("TFrame", background=bg)
        style.configure("Header.TFrame", background=surface)
        style.configure("TLabel", background=bg, foreground=text)
        style.configure("HeaderTitle.TLabel", background=surface, foreground=text,
                        font=("TkDefaultFont", 19, "bold"))
        style.configure("HeaderSub.TLabel", background=surface, foreground=secondary,
                        font=("TkDefaultFont", 11))
        style.configure("Muted.TLabel", background=bg, foreground=secondary)
        style.configure("TSeparator", background=split, bordercolor=split,
                        lightcolor=split, darkcolor=split)

        style.configure("Treeview", rowheight=40, font=("TkDefaultFont", 12),
                        background=surface, fieldbackground=surface,
                        foreground=text, borderwidth=0,
                        bordercolor=split, lightcolor=surface, darkcolor=surface)
        style.map("Treeview",
                  background=[("selected", "#e6f4ff")],
                  foreground=[("selected", text)])
        style.configure("Treeview.Heading", font=("TkDefaultFont", 11, "bold"),
                        background="#fafafa", foreground=secondary,
                        relief="flat", padding=(10, 10))
        style.map("Treeview.Heading", background=[("active", "#f0f0f0")])

        style.configure("TEntry", fieldbackground=surface, foreground=text,
                        bordercolor=border, lightcolor=border, darkcolor=border,
                        padding=(8, 5))
        style.map("TEntry", bordercolor=[("focus", primary)],
                  lightcolor=[("focus", primary)], darkcolor=[("focus", primary)])
        style.configure("TCombobox", fieldbackground=surface, foreground=text,
                        bordercolor=border, arrowcolor=secondary, padding=(8, 4))

        style.configure("TButton", padding=(15, 5), background=surface,
                        foreground=text, bordercolor=border, relief="solid",
                        borderwidth=1)
        style.map("TButton",
                  foreground=[("active", primary), ("pressed", primary)],
                  bordercolor=[("active", primary), ("pressed", primary)],
                  background=[("pressed", "#f5f5f5"), ("active", "#ffffff")])
        style.configure("Accent.TButton", background=primary, foreground="#ffffff",
                        bordercolor=primary, relief="solid", borderwidth=1)
        style.map("Accent.TButton",
                  background=[("disabled", "#f5f5f5"), ("pressed", "#0958d9"),
                              ("active", "#4096ff")],
                  foreground=[("disabled", "#bfbfbf")],
                  bordercolor=[("disabled", border)])
        style.configure("Danger.TButton", background=surface, foreground="#cf1322",
                        bordercolor="#ffccc7", relief="solid", borderwidth=1)
        style.map("Danger.TButton",
                  background=[("disabled", "#f5f5f5"), ("pressed", "#ff4d4f"),
                              ("active", "#ff4d4f")],
                  foreground=[("disabled", "#bfbfbf"), ("active", "#ffffff")],
                  bordercolor=[("active", "#ff4d4f")])
    # -- UI -------------------------------------------------------------------

    def _build_ui(self):
        # Ant Design "Layout.Header": white bar + hairline divider.
        header = tk.Frame(self.root, bg="#ffffff")
        header.pack(fill="x")
        header_inner = ttk.Frame(header, style="Header.TFrame", padding=(20, 12))
        header_inner.pack(fill="x")
        brand = ttk.Frame(header_inner, style="Header.TFrame")
        brand.pack(side="left")
        self._logo_img = tk.PhotoImage(data=LOGO_PNG).subsample(4)
        tk.Label(brand, image=self._logo_img, bg="#ffffff").pack(
            side="left", padx=(0, 12))
        title_box = ttk.Frame(brand, style="Header.TFrame")
        title_box.pack(side="left")
        ttk.Label(title_box, text="RDP Link",
                  style="HeaderTitle.TLabel").pack(anchor="w")
        ttk.Label(title_box, text="Remote access to your own devices",
                  style="HeaderSub.TLabel").pack(anchor="w", pady=(2, 0))
        ttk.Button(header_inner, text="Setup", command=self.open_setup).pack(
            side="right", padx=(0, 8), pady=(2, 0))
        ttk.Button(header_inner, text="+  Add device", command=self.add_profile,
                   style="Accent.TButton").pack(side="right", pady=(2, 0))
        tk.Frame(self.root, bg="#f0f0f0", height=1).pack(fill="x")

        # Footer is packed before the content so it stays pinned to the
        # bottom edge at any window size; the table shrinks instead.
        tk.Frame(self.root, bg="#f0f0f0", height=1).pack(side="bottom", fill="x")
        footer = tk.Frame(self.root, bg="#ffffff")
        footer.pack(side="bottom", fill="x")
        footer_inner = ttk.Frame(footer, style="Header.TFrame", padding=(20, 10))
        footer_inner.pack(fill="x")

        self._dot_canvas = tk.Canvas(footer_inner, width=8, height=8,
                                     highlightthickness=0, bg="#ffffff")
        self._dot_canvas.pack(side="left")
        self._dot = self._dot_canvas.create_oval(1, 1, 7, 7, fill="#52c41a",
                                                 outline="")
        ttk.Label(footer_inner, textvariable=self._status_var,
                  style="HeaderSub.TLabel").pack(side="left", padx=(8, 12))

        self._edit_btn = ttk.Button(footer_inner, text="Edit…",
                                    command=self.edit_profile)
        self._edit_btn.pack(side="right")
        self._disconnect_all_btn = ttk.Button(footer_inner, text="Disconnect All",
                                              style="Danger.TButton",
                                              command=self.disconnect_all)
        self._disconnect_all_btn.pack(side="right", padx=(0, 8))
        self._connect_btn = ttk.Button(footer_inner, text="Connect",
                                       style="Accent.TButton",
                                       command=self.toggle_selected)
        self._connect_btn.pack(side="right", padx=(0, 8))

        outer = ttk.Frame(self.root, padding=(20, 16))
        outer.pack(fill="both", expand=True)
        self._office_panel = OfficePanel(outer, status_callback=self.set_status)
        self._office_panel.pack(fill="x", pady=(0, 16))

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
        self.tree.tag_configure("connected", background="#f6ffed",
                                foreground="#389e0d")
        self.tree.tag_configure("odd", background="#fafafa")
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
        self._menu.add_command(label="RDP Link: Connect to host…",
                               command=self.connect_office_host)
        self._menu.add_command(label="RDP Link: Connect by Peer ID…",
                               command=self.connect_office_peer_id)
        self._menu.add_command(label="RDP Link: Browse hosts…",
                               command=self.browse_office_hosts)
        self._menu.add_command(label="RDP Link: Open Web Guest…",
                               command=self.open_web_guest)
        self._menu.add_command(label="RDP Link: Generate pairing code",
                               command=self.generate_pairing_code)
        self._menu.add_command(label="RDP Link: Wake on LAN…",
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
        for index, p in enumerate(self.store.profiles):
            active = self.controller.is_active(p["id"])
            pw = "🔑 " if p.get("savePassword") and secrets.store_has(p["id"]) else ""
            state = "● Connected" if active else ""
            chips = self.controller.bridges.device_chips(p["id"])
            if chips:
                state = f"{state} {chips}".strip()
            tag = "connected" if active else ("odd" if index % 2 else "even")
            self.tree.insert("", "end", iid=p["id"], tags=(tag,),
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

    def open_setup(self):
        SetupDialog(self.root)

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
            os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "..", "dist", "macos", "RDP Link.app", "Contents", "MacOS", "office-rdp"),
            "/tmp/office-rdp",
        ]
        for c in candidates:
            if os.path.exists(c):
                return c
        return None

    def connect_office_peer_id(self):
        peer_id = simpledialog.askstring("RDP Link", "Peer ID (e.g. 123 456 789):")
        rendezvous = simpledialog.askstring("RDP Link", "Rendezvous URL:")
        password = simpledialog.askstring("RDP Link", "Password (optional):", show="*")
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
                messagebox.showerror("RDP Link", "No address for peer id")
                return
            self._open_office_viewer(addr, password)
        except Exception as e:
            messagebox.showerror("RDP Link", str(e))

    def _open_office_viewer(self, addr, password=None):
        try:
            subprocess.Popen(viewer_argv(addr, password))
        except Exception as e:
            messagebox.showerror("RDP Link", str(e))

    def connect_office_host(self):
        url = simpledialog.askstring("RDP Link", "Host URL (http://ip:port):")
        if not url:
            return
        password = simpledialog.askstring("RDP Link", "Password (optional):", show="*")
        try:
            subprocess.Popen(viewer_argv(url, password))
        except Exception as e:
            messagebox.showerror("RDP Link", str(e))

    def _signed_peer_list(self, rendezvous_url):
        """Ask the agent to list the peers of a rendezvous server.

        The registry is only readable with a signed request, so the agent
        (which owns the device key) performs the call.
        """
        import json
        import subprocess
        binary = self._find_office_binary()
        if not binary:
            return None
        try:
            out = subprocess.check_output(
                [binary, "--list-peers", rendezvous_url.strip()],
                text=True, timeout=15)
        except Exception:
            return None
        try:
            return json.loads(out)
        except Exception:
            return None

    def browse_office_hosts(self):
        url = simpledialog.askstring("RDP Link", "Rendezvous URL:")
        if not url:
            return
        peers = self._signed_peer_list(url)
        if peers is None:
            messagebox.showerror(
                "RDP Link",
                "Could not list the hosts. The rendezvous only answers signed "
                "requests; make sure the RDP Link agent is installed.")
            return
        if not peers:
            messagebox.showinfo("RDP Link", "No hosts found.")
            return
        win = tk.Toplevel(self.root)
        win.title("RDP Link hosts")
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
                messagebox.showerror("RDP Link", "No address for host")
                return
            if not addr.startswith("http"):
                addr = "http://" + addr
            subprocess.Popen(viewer_argv(addr))
            win.destroy()
        ttk.Button(win, text="Connect", command=connect).pack()

    def open_web_guest(self):
        url = simpledialog.askstring("RDP Link", "Rendezvous URL:")
        if not url:
            return
        import webbrowser
        webbrowser.open(url.rstrip("/") + "/client")

    def wake_on_lan(self):
        mac = simpledialog.askstring("RDP Link", "MAC (aa:bb:cc:dd:ee:ff):")
        if not mac:
            return
        binp = self._find_office_binary()
        if not binp:
            messagebox.showerror("RDP Link", "office-rdp binary not found")
            return
        try:
            subprocess.Popen([binp, "--wake", mac])
            self._status_var.set(f"Wake packet sent to {mac}")
        except Exception as e:
            messagebox.showerror("RDP Link", str(e))

    def generate_pairing_code(self):
        binp = self._find_office_binary()
        if not binp:
            messagebox.showerror("RDP Link", "office-rdp binary not found")
            return
        try:
            out = subprocess.check_output([binp, "--pair-code"], text=True, timeout=5).strip()
            messagebox.showinfo("RDP Link", out)
        except Exception as e:
            messagebox.showerror("RDP Link", str(e))

    def stop_office_host(self):
        self._office_panel.stop_host()

    def start_office_host(self):
        self._office_panel.start_host()

    def _on_close(self):
        self.controller.disconnect_all()
        self.root.after(300, self.root.destroy)

    def run(self):
        self.root.mainloop()
