"""Setup & permissions assistant (Tk)."""

import threading
import tkinter as tk
from tkinter import ttk

from .. import setupcheck

BG = "#f5f5f5"
SURFACE = "#ffffff"
SPLIT = "#f0f0f0"
BORDER = "#d9d9d9"
TEXT = "#1f1f1f"
MUTED = "#595959"
PRIMARY = "#1677ff"
OK_BG, OK_FG = "#f6ffed", "#389e0d"
WARN_BG, WARN_FG = "#fffbe6", "#d48806"
FAIL_BG, FAIL_FG = "#fff2f0", "#cf1322"
INFO_BG, INFO_FG = "#fafafa", "#595959"


class SetupDialog(tk.Toplevel):
    """Lists each OS prerequisite with live status and a Fix action."""

    def __init__(self, parent):
        super().__init__(parent)
        self.title("RDP Link — Setup & permissions")
        self.configure(bg=BG)
        self.geometry("720x620")
        self.minsize(640, 520)
        self.transient(parent)
        self.items = []
        self._build()
        self.refresh()

    def _build(self):
        header = tk.Frame(self, bg=SURFACE)
        header.pack(fill="x")
        inner = tk.Frame(header, bg=SURFACE, padx=20, pady=14)
        inner.pack(fill="x")
        tk.Label(inner, text="Setup & permissions", bg=SURFACE, fg=TEXT,
                 font=("TkDefaultFont", 18, "bold")).pack(anchor="w")
        self.subtitle = tk.Label(inner, text="Checking…", bg=SURFACE, fg=MUTED,
                                 font=("TkDefaultFont", 11))
        self.subtitle.pack(anchor="w", pady=(2, 0))
        tk.Frame(self, bg=SPLIT, height=1).pack(fill="x")

        footer = tk.Frame(self, bg=SURFACE)
        footer.pack(side="bottom", fill="x")
        tk.Frame(self, bg=SPLIT, height=1).pack(side="bottom", fill="x")
        row = tk.Frame(footer, bg=SURFACE, padx=20, pady=10)
        row.pack(fill="x")
        ttk.Button(row, text="Refresh", command=self.refresh).pack(side="left")
        ttk.Button(row, text="Close", command=self.destroy).pack(side="right")

        self.body = tk.Frame(self, bg=BG, padx=16, pady=12)
        self.body.pack(fill="both", expand=True)

    def refresh(self):
        self.subtitle.configure(text="Checking…")
        threading.Thread(target=self._collect, daemon=True).start()

    def _collect(self):
        items = setupcheck.collect()
        self.after(0, lambda: self._render(items))

    def _render(self, items):
        for child in self.body.winfo_children():
            child.destroy()
        self.items = items
        bad, total = setupcheck.summarize(items)
        if bad:
            self.subtitle.configure(
                text=f"{bad} of {total} items need attention", fg=WARN_FG)
        else:
            self.subtitle.configure(text="Everything is ready", fg=OK_FG)

        for item in items:
            card = tk.Frame(self.body, bg=SURFACE, highlightbackground=SPLIT,
                            highlightthickness=1)
            card.pack(fill="x", pady=4)
            inner = tk.Frame(card, bg=SURFACE, padx=14, pady=10)
            inner.pack(fill="x")

            left = tk.Frame(inner, bg=SURFACE)
            left.pack(side="left", fill="x", expand=True)
            tk.Label(left, text=item["label"], bg=SURFACE, fg=TEXT,
                     font=("TkDefaultFont", 12, "bold")).pack(anchor="w")
            tk.Label(left, text=item["detail"], bg=SURFACE, fg=MUTED,
                     font=("TkDefaultFont", 10), wraplength=430,
                     justify="left").pack(anchor="w", pady=(2, 0))

            colors = {
                "ok": (OK_BG, OK_FG, "Ready"),
                "warn": (WARN_BG, WARN_FG, "Action needed"),
                "fail": (FAIL_BG, FAIL_FG, "Blocked"),
                "info": (INFO_BG, INFO_FG, "Optional"),
            }[item["status"]]
            tk.Label(inner, text=colors[2], bg=colors[0], fg=colors[1],
                     padx=8, pady=2).pack(side="right", padx=(8, 0))
            if item.get("fix"):
                ttk.Button(inner, text=item["fix"],
                           command=lambda i=item: self._fix(i)).pack(side="right")

    def _fix(self, item):
        self.subtitle.configure(text="Applying…")

        def work():
            setupcheck.run_action(item)
            self.after(0, self.refresh)

        threading.Thread(target=work, daemon=True).start()
