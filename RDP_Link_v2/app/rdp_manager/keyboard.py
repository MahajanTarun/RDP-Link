"""Keyboard layout: detect the client's active layout and send it to the
server via FreeRDP's /kbd:layout:<id>.

"Auto" (default) detects the currently selected device layout
(macOS: `defaults read com.apple.HIToolbox …`; Linux: `setxkbmap -query`)
at app start in the background, so the connect path never blocks.
Detection runs once per session and is cached; an unresolvable layout
means no flag — the server keeps its own default.
"""

import re
import subprocess
import sys
import threading

# Windows KLID-style layout ids (what FreeRDP's /kbd:layout expects).
# Keys are positive-detection patterns; FIRST match wins, so put specific
# names before generic ones (e.g. "Canadian French" before "French").
_MAC_NAME_HINTS = [
    ("Canadian French", 0x0C0C),
    ("Belgian", 0x080C),
    ("Swiss German", 0x0807),
    ("Swiss French", 0x100C),
    ("British", 0x0809),
    ("U.S. International", 0x0409),
    ("Dvorak", 0x00010409),
    ("ABC", 0x0409),
    ("US", 0x0409),
    ("U.S.", 0x0409),
    ("German", 0x0407),
    ("French", 0x040C),
    ("Spanish", 0x040A),
    ("Italian", 0x0410),
    ("Portuguese", 0x0816),
    ("Brazilian", 0x0416),
    ("Dutch", 0x0413),
    ("Russian", 0x0419),
    ("Ukrainian", 0x0422),
    ("Swedish", 0x041D),
    ("Danish", 0x0406),
    ("Norwegian", 0x0414),
    ("Finnish", 0x040B),
    ("Polish", 0x0415),
    ("Turkish", 0x041F),
    ("Japanese", 0x0411),
    ("Korean", 0x0412),
    ("Czech", 0x0405),
    ("Hungarian", 0x040E),
    ("Croatian", 0x041A),
    ("Slovak", 0x041B),
    ("Slovenian", 0x0424),
    ("Greek", 0x0408),
    ("Romanian", 0x0418),
    ("Bulgarian", 0x0402),
    ("Hebrew", 0x040D),
    ("Arabic", 0x0401),
]

# setxkbmap layout codes -> same ids
_XKB_LAYOUTS = {
    "us": 0x0409, "gb": 0x0809, "uk": 0x0809, "de": 0x0407, "fr": 0x040C,
    "es": 0x040A, "it": 0x0410, "pt": 0x0816, "br": 0x0416, "nl": 0x0413,
    "ru": 0x0419, "ua": 0x0422, "se": 0x041D, "dk": 0x0406, "no": 0x0414,
    "fi": 0x040B, "pl": 0x0415, "tr": 0x041F, "jp": 0x0411, "kr": 0x0412,
    "cz": 0x0405, "hu": 0x040E, "hr": 0x041A, "sk": 0x041B, "si": 0x0424,
    "gr": 0x0408, "ro": 0x0418, "bg": 0x0402, "il": 0x040D, "be": 0x080C,
    "ca": 0x1009, "ch": 0x0807, "dvorak": 0x00010409,
}

_cache = {"code": None, "source": None, "done": False}
_lock = threading.Lock()


def _run_util(cmd, timeout=5):
    """Run a small detection utility; returns (rc, stdout) or (-1, '')."""
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError):
        return -1, ""
    return (r.returncode, r.stdout or "")


def _map_macos_name(name):
    """macOS 'KeyboardLayout Name' -> FreeRDP hex id string (or None).
    Direct-substring only: 'French' must NOT match 'Canadian French'."""
    if not name:
        return None
    low = name.lower()
    for hint, hid in _MAC_NAME_HINTS:
        if hint.lower() in low:
            return f"0x{hid:X}"
    return None


def _mac_layout_name():
    """Current macOS keyboard layout name ('ABC-India', 'U.S.', …)."""
    rc, out = _run_util(["defaults", "read", "com.apple.HIToolbox",
                         "AppleSelectedInputSources"])
    if rc != 0:
        return None
    names = re.findall(r'"KeyboardLayout Name"\s*=\s*"([^"]+)"', out)
    return names[0] if names else None   # first entry = selected layout


def _xkb_layout():
    rc, out = _run_util(["setxkbmap", "-query"])
    if rc != 0:
        return None
    m = re.search(r"^layout:\s*(\S+)", out, re.M)
    if not m:
        return None
    return m.group(1).split(",")[0]      # first layout in multi-layout lists


def _map_xkb(xkb):
    """setxkbmap layout code -> FreeRDP hex id string (or None)."""
    base = (xkb or "").split("(")[0].strip().lower()
    if base not in _XKB_LAYOUTS:
        return None
    hid = _XKB_LAYOUTS["dvorak"] if "dvorak" in (xkb or "").lower() \
        else _XKB_LAYOUTS[base]
    return f"0x{hid:X}"


def detect_client_layout(force=False):
    """Detect once per session; returns (code_string|None, source|None)."""
    with _lock:
        if _cache["done"] and not force:
            return _cache["code"], _cache["source"]
        name = _mac_layout_name() if sys.platform == "darwin" else None
        code, source = None, None
        if sys.platform == "darwin":
            if name:
                code = _map_macos_name(name)
                source = f"macOS layout '{name}'"
                if code is None:
                    source = f"macOS layout '{name}' (unmapped)"
            else:
                source = "macOS layout unknown"
        else:
            xkb = _xkb_layout()
            code = _map_xkb(xkb)
            if xkb:
                source = f"xkb layout '{xkb}'"
                if code is None:
                    source = f"xkb layout '{xkb}' (unmapped)"
            else:
                source = "xkb layout unknown"
        _cache.update(code=code, source=source, done=True)
        return code, source


def resolve_for_connect(profile, blocking=True):
    """FreeRDP /kbd layout id for a profile, or None when unknown.

    profile['keyboard'] == 'auto'  -> detected client layout (cached)
    otherwise the stored value ('0x409') is used verbatim."""
    want = profile.get("keyboard", "auto")
    if want == "auto":
        if blocking:
            code, _ = detect_client_layout()
        else:
            with _lock:
                code = _cache["code"] if _cache["done"] else None
        return code
    return want if re.fullmatch(r"0x[0-9A-Fa-f]+", str(want)) else None


def warmup():
    """Kick off detection in the background at app start."""
    def work():
        try:
            detect_client_layout()
        except Exception:
            pass
    threading.Thread(target=work, daemon=True).start()


def choices():
    """[(value, label)] for the editor combobox, hex ids deduplicated."""
    seen, out = set(), []
    for hint, code in _MAC_NAME_HINTS:
        hex_id = f"0x{code:X}"
        if hex_id in seen:
            continue
        seen.add(hex_id)
        out.append((hex_id, f"{hint.title()} ({hex_id})"))
    return out
