"""Small shared dialogs."""

import tkinter as tk
from tkinter import ttk


def _pw_show_toggle(entry, var):
    entry.config(show="" if var.get() else "●")


class PasswordPrompt(tk.Toplevel):
    """Ask for a password to store, then connect."""

    def __init__(self, parent, profile, on_save_and_connect):
        super().__init__(parent)
        self.on_save_and_connect = on_save_and_connect
        self.title("Save password")
        self.resizable(False, False)
        self.transient(parent)
        self.grab_set()

        self._pw_var = tk.StringVar()
        self._pw_show = tk.BooleanVar()
        frm = ttk.Frame(self, padding=20)
        frm.grid(sticky="nsew")
        ttk.Label(frm, text="Save password",
                  font=("TkDefaultFont", 15, "bold")).grid(
            row=0, column=0, sticky="w", pady=(0, 8))
        ttk.Label(frm, text="This password will be stored and used for "
                            "direct connect.").grid(row=1, column=0,
                                                    sticky="w", pady=(0, 10))
        ttk.Label(frm, text="Password").grid(row=2, column=0, sticky="w")
        row = ttk.Frame(frm)
        row.grid(row=3, column=0, sticky="ew", pady=(2, 14))
        entry = tk.Entry(row, textvariable=self._pw_var, show="●", width=30)
        entry.pack(side="left")
        ttk.Checkbutton(row, text="👁", variable=self._pw_show,
                        command=lambda: _pw_show_toggle(entry, self._pw_show)
                        ).pack(side="left", padx=(4, 0))
        btns = ttk.Frame(frm)
        btns.grid(row=4, column=0, sticky="e")
        ttk.Button(btns, text="Cancel", command=self.destroy).pack(side="right")
        ttk.Button(btns, text="Save and Connect",
                   command=self._go).pack(side="right", padx=(0, 8))
        self.bind("<Return>", lambda e: self._go())
        self.bind("<Escape>", lambda e: self.destroy())
        entry.focus_set()

    def _go(self):
        pw = self._pw_var.get()
        self.destroy()
        if pw:
            self.on_save_and_connect(pw)
