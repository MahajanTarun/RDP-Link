"""Office RDP guest viewer (Tk).

Connects to a host agent over HTTP, renders screen frames, forwards mouse and
keyboard input, and syncs the clipboard. Works against the desktop Go agent and
the Android host service.
"""

import io
import json
import os
import sys
import tempfile
import threading
import time
import tkinter as tk
import urllib.error
import urllib.parse
import urllib.request
from tkinter import filedialog, messagebox, simpledialog

POLL_MS = 60

# Light theme palette shared by the viewer chrome. The remote image keeps its
# own colours; the surrounding UI stays bright, calm and high-contrast.
BG = "#f4f6fb"
SURFACE = "#ffffff"
BORDER = "#e2e8f0"
TEXT = "#0f172a"
MUTED = "#64748b"
ACCENT = "#2563eb"
ACCENT_HOVER = "#1d4ed8"
ACCENT_SOFT = "#eef4ff"
OK_SOFT = "#ecfdf5"
OK_TEXT = "#047857"
WARN_SOFT = "#fff7ed"
WARN_TEXT = "#b45309"
ERROR_SOFT = "#fef2f2"
ERROR_TEXT = "#b91c1c"
CANVAS_BG = "#e8edf6"

try:  # optional, gives smooth scaling and JPEG support
    from PIL import Image, ImageTk

    HAVE_PIL = True
except Exception:  # pragma: no cover - depends on the runtime image
    HAVE_PIL = False


class Chip(tk.Label):
    """Toolbar button that keeps its colours on every platform.

    ``tk.Button`` ignores ``bg``/``fg`` under the native macOS Aqua theme, which
    makes themed buttons invisible. A ``Label`` with a click binding and a
    hairline border renders identically on macOS, Windows and Linux.
    """

    def __init__(self, parent, text, command, primary=False):
        self._command = command
        self._base = ACCENT if primary else SURFACE
        self._hover = ACCENT_HOVER if primary else "#f1f5f9"
        super().__init__(
            parent, text=text, bg=self._base,
            fg="#ffffff" if primary else TEXT,
            padx=12, pady=5, cursor="hand2", borderwidth=0,
            highlightthickness=1,
            highlightbackground=ACCENT if primary else BORDER,
            highlightcolor=ACCENT if primary else "#cbd5e1")
        self.bind("<Button-1>", lambda _event: self._command())
        self.bind("<Enter>", lambda _event: self.configure(bg=self._hover))
        self.bind("<Leave>", lambda _event: self.configure(bg=self._base))


class Viewer:
    def __init__(self, host_url, password=None):
        self.host_url = host_url.rstrip("/")
        self.password = password
        self.root = tk.Tk()
        self.root.title(f"Office RDP — {self.host_url}")
        self.root.geometry("1200x760")
        self.root.minsize(860, 560)
        self.root.configure(bg=BG)

        toolbar = tk.Frame(self.root, bg=SURFACE)
        toolbar.pack(fill="x")
        tk.Frame(self.root, bg=BORDER, height=1).pack(fill="x")

        # Right-hand actions first so they keep a stable position.
        Chip(toolbar, "Disconnect", self._close).pack(side="right", padx=(0, 14))
        Chip(toolbar, "Fullscreen", self._toggle_fullscreen).pack(
            side="right", padx=(0, 8))
        Chip(toolbar, "Upload", self._upload_file).pack(side="right", padx=(0, 8))
        Chip(toolbar, "Download", self._download_file).pack(side="right", padx=(0, 8))

        brand = tk.Frame(toolbar, bg=SURFACE)
        brand.pack(side="left", padx=(16, 0), pady=10)
        mark = tk.Canvas(brand, width=22, height=22, bg=SURFACE,
                         highlightthickness=0)
        mark.create_oval(1, 1, 21, 21, fill=ACCENT, outline="")
        mark.create_text(11, 11, text="O", fill="#ffffff",
                         font=("TkDefaultFont", 10, "bold"))
        mark.pack(side="left", padx=(0, 8))
        tk.Label(brand, text="Office RDP", bg=SURFACE, fg=TEXT,
                 font=("TkDefaultFont", 14, "bold")).pack(side="left")

        self.status = tk.Label(toolbar, text="Connecting…", bg=WARN_SOFT,
                               fg=WARN_TEXT, padx=10, pady=3,
                               font=("TkDefaultFont", 10))
        self.status.pack(side="left", padx=(16, 0))

        self.canvas = tk.Canvas(self.root, bg=CANVAS_BG, highlightthickness=0)
        self.canvas.pack(fill="both", expand=True, padx=1, pady=(1, 1))
        self.canvas.focus_set()

        footer = tk.Frame(self.root, bg=SURFACE)
        footer.pack(fill="x")
        tk.Frame(self.root, bg=BORDER, height=1).pack(fill="x", before=footer)
        tk.Label(footer, text="Click to control · clipboard syncs automatically",
                 bg=SURFACE, fg=MUTED, padx=16, pady=5,
                 font=("TkDefaultFont", 9)).pack(side="left")
        tk.Label(footer, text="Esc exits fullscreen", bg=SURFACE, fg=MUTED,
                 padx=16, pady=5, font=("TkDefaultFont", 9)).pack(side="right")

        self.photo = None
        self._pil_image = None
        self._frame_size = (0, 0)
        self._offset = (0, 0)
        self._scale = 1.0
        self._last_clip = ""
        self._last_remote = ""
        self._stop = False
        self._frame_path = "/frame.jpg" if HAVE_PIL else "/frame.png"
        self._tmp = tempfile.NamedTemporaryFile(
            suffix=".jpg" if HAVE_PIL else ".png", delete=False)
        self._tmp.close()
        self._frame_count = 0
        self._fps_started = time.time()

        self.canvas.bind("<Motion>", self._on_move)
        self.canvas.bind("<Button-1>", self._on_click)
        self.canvas.bind("<Button-3>", self._on_right_click)
        self.canvas.bind("<MouseWheel>", self._on_wheel)
        self.canvas.bind("<Button-4>", lambda e: self._on_wheel(e, 1))
        self.canvas.bind("<Button-5>", lambda e: self._on_wheel(e, -1))
        self.root.bind("<Key>", self._on_key)
        self.root.bind("<F11>", self._toggle_fullscreen)
        self.root.protocol("WM_DELETE_WINDOW", self._close)

        threading.Thread(target=self._loop, daemon=True).start()
        threading.Thread(target=self._clip_loop, daemon=True).start()

    # -- status --------------------------------------------------------------
    def _set_status(self, text, kind="live"):
        palette = {
            "live": (OK_SOFT, OK_TEXT),
            "connecting": (WARN_SOFT, WARN_TEXT),
            "error": (ERROR_SOFT, ERROR_TEXT),
        }
        bg, fg = palette.get(kind, (ACCENT_SOFT, ACCENT))
        self.status.configure(text=text, bg=bg, fg=fg)

    # -- networking ----------------------------------------------------------
    def _auth_url(self, path, first=True):
        url = self.host_url + path
        if self.password:
            sep = "&" if "?" in url else "?"
            url += sep + "password=" + urllib.parse.quote(self.password)
        return url

    def _fetch(self, path, headers=None):
        request = urllib.request.Request(self._auth_url(path), headers=headers or {})
        with urllib.request.urlopen(request, timeout=3) as response:
            return response.read(), dict(response.headers)

    def _fetch_bytes(self, path, headers=None):
        return self._fetch(path, headers)[0]

    def _post(self, payload):
        try:
            request = urllib.request.Request(
                self._auth_url("/input"),
                data=json.dumps(payload).encode(),
                headers={"Content-Type": "application/json"},
            )
            urllib.request.urlopen(request, timeout=2)
        except Exception:
            pass

    def _post_clip(self, text):
        try:
            request = urllib.request.Request(
                self._auth_url("/clipboard"),
                data=json.dumps({"text": text}).encode(),
                headers={"Content-Type": "application/json"},
            )
            urllib.request.urlopen(request, timeout=3)
        except Exception:
            pass

    # -- frame loop ----------------------------------------------------------
    def _loop(self):
        etag = ""
        while not self._stop:
            try:
                headers = {"If-None-Match": etag} if etag else {}
                request = urllib.request.Request(
                    self._auth_url(self._frame_path), headers=headers)
                try:
                    with urllib.request.urlopen(request, timeout=3) as response:
                        data = response.read()
                        etag = response.headers.get("ETag", "")
                except urllib.error.HTTPError as exc:
                    if exc.code == 304:
                        time.sleep(POLL_MS / 1000.0)
                        continue
                    raise
                if HAVE_PIL:
                    # Decode off the Tk thread; only PhotoImage work stays on it.
                    image = Image.open(io.BytesIO(data)).convert("RGB")
                    self.root.after(0, self._update_image, image)
                else:
                    with open(self._tmp.name, "wb") as fh:
                        fh.write(data)
                    self.root.after(0, self._update, self._tmp.name)
                continue
            except Exception as exc:
                message = f"Disconnected: {exc}"
                try:
                    self.root.after(0, lambda m=message: self._set_status(m, "error"))
                except Exception:
                    pass
            time.sleep(POLL_MS / 1000.0)

    def _update_image(self, image):
        try:
            self._frame_size = image.size
            self._pil_image = image
            self._render_pil()
            self._tick_fps()
        except Exception as exc:
            self._set_status(f"Decode error: {exc}", "error")

    def _tick_fps(self):
        self._frame_count += 1
        elapsed = max(time.time() - self._fps_started, 0.001)
        if elapsed >= 1.0:
            fps = self._frame_count / elapsed
            self._frame_count = 0
            self._fps_started = time.time()
            self._set_status(
                f"{self._frame_size[0]}×{self._frame_size[1]} · {fps:.0f} fps")

    def _update(self, path):
        try:
            if HAVE_PIL:
                image = Image.open(path)
                self._frame_size = image.size
                image = image.convert("RGB")
                self._pil_image = image
                self._render_pil()
            else:
                self.photo = tk.PhotoImage(file=path)
                self._frame_size = (self.photo.width(), self.photo.height())
                self._render_plain()
            self._tick_fps()
        except Exception as exc:
            self._set_status(f"Decode error: {exc}", "error")

    def _render_pil(self):
        canvas_w = max(self.canvas.winfo_width(), 1)
        canvas_h = max(self.canvas.winfo_height(), 1)
        image_w, image_h = self._frame_size
        scale = min(canvas_w / image_w, canvas_h / image_h, 1.0)
        width = max(int(image_w * scale), 1)
        height = max(int(image_h * scale), 1)
        resized = self._pil_image.resize((width, height), Image.LANCZOS)
        self.photo = ImageTk.PhotoImage(resized)
        self._scale = scale
        self._offset = ((canvas_w - width) // 2, (canvas_h - height) // 2)
        self.canvas.delete("frame")
        self.canvas.create_image(self._offset[0], self._offset[1], anchor="nw",
                                 image=self.photo, tags="frame")

    def _render_plain(self):
        canvas_w = max(self.canvas.winfo_width(), 1)
        canvas_h = max(self.canvas.winfo_height(), 1)
        image_w, image_h = self._frame_size
        factor = 1
        while (image_w // factor > canvas_w or image_h // factor > canvas_h) and factor < 8:
            factor += 1
        shown = self.photo
        if factor > 1:
            shown = self.photo.subsample(factor, factor)
        self._scale = 1.0 / factor
        width, height = shown.width(), shown.height()
        self._offset = ((canvas_w - width) // 2, (canvas_h - height) // 2)
        self._shown = shown
        self.canvas.delete("frame")
        self.canvas.create_image(self._offset[0], self._offset[1], anchor="nw",
                                 image=shown, tags="frame")

    # -- input ---------------------------------------------------------------
    def _to_remote(self, x, y):
        rx = (x - self._offset[0]) / max(self._scale, 0.0001)
        ry = (y - self._offset[1]) / max(self._scale, 0.0001)
        return int(rx), int(ry)

    def _on_move(self, event):
        x, y = self._to_remote(event.x, event.y)
        self._post({"type": "move", "x": x, "y": y})

    def _on_click(self, event):
        x, y = self._to_remote(event.x, event.y)
        self._post({"type": "click", "x": x, "y": y, "button": 1})

    def _on_right_click(self, event):
        x, y = self._to_remote(event.x, event.y)
        self._post({"type": "click", "x": x, "y": y, "button": 3})

    def _on_wheel(self, event, direction=None):
        if direction is None:
            direction = 1 if getattr(event, "delta", 0) > 0 else -1
        self._post({"type": "wheel", "delta": direction})

    def _on_key(self, event):
        special = {
            "Return": "enter", "BackSpace": "backspace", "Escape": "escape",
            "Tab": "tab", "Home": "home", "Left": "left", "Right": "right",
            "Up": "up", "Down": "down", "Delete": "delete",
        }
        if event.keysym in special:
            self._post({"type": "key", "key": special[event.keysym]})
        elif event.char:
            self._post({"type": "text", "text": event.char})

    def _toggle_fullscreen(self, event=None):
        self.root.attributes("-fullscreen", not self.root.attributes("-fullscreen"))

    # -- clipboard -----------------------------------------------------------
    def _clip_loop(self):
        while not self._stop:
            try:
                remote = json.loads(self._fetch_bytes("/clipboard")).get("text", "")
                if remote != self._last_remote:
                    self._last_remote = remote
                    self.root.after(0, lambda t=remote: self._set_local_clip(t))
                try:
                    local = self.root.clipboard_get()
                except Exception:
                    local = ""
                if local != self._last_clip and local != self._last_remote:
                    self._last_clip = local
                    self._post_clip(local)
            except Exception:
                pass
            time.sleep(1.0)

    def _set_local_clip(self, text):
        try:
            self.root.clipboard_clear()
            self.root.clipboard_append(text)
        except Exception:
            pass

    # -- files ---------------------------------------------------------------
    def _upload_file(self):
        path = filedialog.askopenfilename()
        if not path:
            return
        try:
            with open(path, "rb") as fh:
                boundary = "----office-rdp"
                parts = [
                    ("--" + boundary).encode(),
                    ('Content-Disposition: form-data; name="file"; filename="%s"'
                     % os.path.basename(path)).encode(),
                    b"Content-Type: application/octet-stream",
                    b"",
                    fh.read(),
                    ("--" + boundary + "--").encode(),
                ]
                body = b"\r\n".join(parts)
                request = urllib.request.Request(
                    self._auth_url("/upload"), data=body,
                    headers={"Content-Type": "multipart/form-data; boundary=" + boundary},
                )
                urllib.request.urlopen(request, timeout=30)
                messagebox.showinfo("Office RDP", "Upload complete")
        except Exception as exc:
            messagebox.showerror("Office RDP", str(exc))

    def _download_file(self):
        name = simpledialog.askstring("Office RDP", "File name on host:")
        if not name:
            return
        try:
            data = urllib.request.urlopen(
                self._auth_url("/download?name=" + urllib.parse.quote(name)), timeout=30
            ).read()
            path = filedialog.asksaveasfilename(initialfile=name)
            if path:
                with open(path, "wb") as fh:
                    fh.write(data)
                messagebox.showinfo("Office RDP", "Download complete")
        except Exception as exc:
            messagebox.showerror("Office RDP", str(exc))

    def _close(self):
        self._stop = True
        try:
            os.unlink(self._tmp.name)
        except OSError:
            pass
        self.root.destroy()

    def run(self):
        self.canvas.focus_set()
        self.root.mainloop()


def probe(host_url, password=None):
    """Fetch one frame without opening a window (used by CI/validation)."""
    if not host_url:
        print("probe: host URL required", file=sys.stderr)
        return 2
    base = host_url.rstrip("/")
    if not base.startswith("http"):
        base = "http://" + base
    url = base + "/frame.jpg"
    if password:
        url += "?password=" + urllib.parse.quote(password)
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            data = response.read()
    except Exception as exc:
        print(f"probe: {exc}", file=sys.stderr)
        return 1
    size = ""
    try:
        from PIL import Image
        with Image.open(io.BytesIO(data)) as image:
            size = f"{image.width}x{image.height}"
    except Exception:
        pass
    print(f"probe: OK {len(data)} bytes {size}".rstrip())
    return 0


def main():
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("host_url", nargs="?")
    parser.add_argument("--password", default=None)
    parser.add_argument("--probe", action="store_true",
                        help="fetch one frame, print its size, then exit")
    args = parser.parse_args()
    if args.probe:
        sys.exit(probe(args.host_url, password=args.password))
    Viewer(args.host_url, password=args.password).run()


if __name__ == "__main__":
    main()
