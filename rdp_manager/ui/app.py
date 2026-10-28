"""Main application window — a thin, fast launcher around FreeRDP."""

import subprocess
import tkinter as tk
import uuid
from tkinter import ttk, messagebox

from .. import APP_NAME, secrets, logutil
from ..connection import ConnectionController, shell_quote_cmd
from ..deploy import copy_setup_command, deploy_receivers
from ..profiles import ProfileStore, normalize
from .dialogs import PasswordPrompt
from .editor import ProfileEditor

class UbuntuRDPApp:
    def __init__(self):
        self.store = ProfileStore()
        self.controller = ConnectionController(self)
        logutil.cleanup()

        self.root = tk.Tk()
        self.root.title(APP_NAME)
        self.root.geometry("800x500")
        self.root.minsize(700, 400)

        self._status_var = tk.StringVar(value="Ready")
        self._style_ui()
        self._build_ui()
        self.refresh_list()
        self.root.protocol("WM_DELETE_WINDOW", self._on_close)
        # Warm the keyboard-layout cache in the background so the connect
        # path never blocks on 'defaults read'/'setxkbmap'.
        from ..keyboard import warmup
        warmup()

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

        ttk.Separator(outer).pack(fill="x", pady=(14, 10))

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
        self._menu.add_command(label="Show session log",
                               command=logutil.open_log)
        self._menu.add_command(label="Check Permissions…",
                               command=self.check_permissions)
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
            self.tree.insert("", "end", iid=p["id"],
                             tags=("connected",) if active else (),
                             values=(
                f'{pw}{p["name"]}',
                f'{p["username"]}  •  {p["host"]}:{p["port"]}',
                self._routing_summary(p),
                "● Connected" if active else "",
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

    def _on_close(self):
        self.controller.disconnect_all()
        self.root.after(300, self.root.destroy)

    def run(self):
        self.root.mainloop()
