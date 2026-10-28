"""Profile storage + schema migration (v1/v2/v3 -> v4)."""

import json
import os
import uuid

from .paths import PROFILES_FILE, ensure_dirs

# Per-device routing options. "native" (raw RDP /microphone) is mic-only.
ROUTES = {
    "audio": ("host", "remote"),
    "mic": ("host", "native", "remote"),
    "camera": ("host", "remote"),
}
ROUTE_LABELS = {
    "host": "This computer",
    "native": "Native RDP microphone (xrdp 0.10+)",
    "remote": "Remote PC's own device",
}

DEFAULTS = {
    "port": "3389",
    "smartSizing": True,
    "savePassword": False,
    "audio": "host",
    "mic": "host",
    "camera": "remote",
    "camDevice": "0",
    "clipboard": True,
    "fileTransfer": True,
    "stdinPass": True,
    "certTofu": True,
}


def default_profiles():
    return [{
        "id": str(uuid.uuid4()),
        "name": "Ubuntu PC",
        "host": "192.168.1.10",
        "port": "3389",
        "username": "user",
        "smartSizing": True,
        "savePassword": False,
        "audio": "host",
        "mic": "host",
        "camera": "remote",
        "camDevice": "0",
        "clipboard": True,
        "fileTransfer": True,
        "stdinPass": True,
        "certTofu": True,
    }]


def normalize(p):
    """Validate one profile dict; migrate legacy keys; fill defaults."""
    p = dict(p)
    # migrate legacy boolean device flags (v1/v2) into route strings
    if "audio" not in p:
        p["audio"] = "host" if p.get("audioOut", True) else "remote"
    if "mic" not in p:
        p["mic"] = "host" if p.get("microphone", p.get("micBridge", True)) else "remote"
    for legacy in ("audioOut", "microphone", "micBridge"):
        p.pop(legacy, None)
    for key, val in DEFAULTS.items():
        if p.get(key) is None:
            p[key] = val
        p.setdefault(key, val)
    for key, allowed in ROUTES.items():
        if p[key] in allowed:
            continue
        if p[key] is True:                 # legacy boolean leaking through
            p[key] = "host"
        elif p[key] is False:
            p[key] = "remote"
        elif key == "mic" and p[key] == "host":
            p[key] = "host"
        else:
            p[key] = DEFAULTS[key]         # e.g. "native" on audio/camera
    # port sanity
    port = str(p["port"]).strip()
    p["port"] = port if port.isdigit() and 0 < int(port) < 65536 else "3389"
    return p


class ProfileStore:
    """Loads/saves profiles.json atomically; migrations on load."""

    def __init__(self, path=None):
        self.path = path or PROFILES_FILE
        ensure_dirs()
        self._install_askpass()
        try:
            with open(self.path, "r", encoding="utf-8") as f:
                raw = json.load(f)
            if not isinstance(raw, list):
                raise ValueError("profiles.json is not a list")
            self.profiles = [normalize(p) for p in raw]
        except (OSError, ValueError, json.JSONDecodeError):
            self.profiles = default_profiles()
            self.save()

    @staticmethod
    def _install_askpass():
        from .secrets import install_askpass
        install_askpass()

    def save(self):
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(self.profiles, f, indent=2)
        os.replace(tmp, self.path)

    def find(self, profile_id):
        for p in self.profiles:
            if p["id"] == profile_id:
                return p
        return None

    def upsert(self, profile):
        existing = self.find(profile["id"])
        if existing is not None:
            self.profiles[self.profiles.index(existing)] = profile
        else:
            self.profiles.append(profile)
        self.save()

    def remove(self, profile_id):
        self.profiles = [p for p in self.profiles if p["id"] != profile_id]
        self.save()
