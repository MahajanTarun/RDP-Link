"""Executable discovery for FreeRDP and ffmpeg (PATH + known prefixes)."""

import os
import shutil


def _known_prefixes():
    return ("/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/usr/sbin",
            "/snap/bin")


def find_exe(*names):
    """Return the first executable found among `names` (PATH + common prefixes)."""
    for name in names:
        hit = shutil.which(name)
        if hit:
            return hit
        for prefix in _known_prefixes():
            cand = os.path.join(prefix, name)
            if os.access(cand, os.X_OK):
                return cand
    return None


def find_freerdp():
    return find_exe("sdl-freerdp", "xfreerdp")


def find_ffmpeg():
    return find_exe("ffmpeg")
