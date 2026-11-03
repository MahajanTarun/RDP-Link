"""Platform paths, resource lookup, and legacy-profile migration
(macOS / Linux client)."""

import os
import shutil
import sys

IS_MAC = sys.platform == "darwin"
IS_LINUX = sys.platform.startswith("linux")

APP_DIR_NAME = "RDP Link"

# Pre-rename ("Ubuntu RDP Manager") support dir — migrated once, on first run.
LEGACY_DIR_NAMES = ("Ubuntu RDP Manager", "ubuntu-rdp-manager")


def _support_dir() -> str:
    if IS_MAC:
        return os.path.expanduser(f"~/Library/Application Support/{APP_DIR_NAME}")
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "rdp-link")


def _legacy_support_dir():
    if IS_MAC:
        legacy = os.path.expanduser("~/Library/Application Support")
        for name in LEGACY_DIR_NAMES:
            if os.path.isdir(os.path.join(legacy, name)):
                return os.path.join(legacy, name)
        return None
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    for name in LEGACY_DIR_NAMES:
        if os.path.isdir(os.path.join(base, name)):
            return os.path.join(base, name)
    return None


SUPPORT_DIR = _support_dir()
PROFILES_FILE = os.path.join(SUPPORT_DIR, "profiles.json")
ASKPASS_PATH = os.path.join(SUPPORT_DIR, "rdp-askpass")
LOG_DIR = os.path.join(SUPPORT_DIR, "logs")

_MIGRATED = False


def migrate_legacy() -> bool:
    """Copy profiles/secrets/cache from a pre-rename install once.

    Returns True when a migration happened. Never overwrites newer state.
    Passwords in the macOS Keychain need no migration: their service name
    is unchanged on purpose (see rdp_manager.__init__)."""
    global _MIGRATED
    if _MIGRATED:
        return False
    legacy = _legacy_support_dir()
    if legacy is None or os.path.exists(PROFILES_FILE):
        _MIGRATED = True
        return False
    os.makedirs(SUPPORT_DIR, exist_ok=True)
    for name in ("profiles.json", "secrets.json", "secrets.json.tmp"):
        src = os.path.join(legacy, name)
        if os.path.exists(src) and not os.path.exists(os.path.join(SUPPORT_DIR, name)):
            shutil.copy2(src, os.path.join(SUPPORT_DIR, name))
    for entry in os.listdir(legacy):               # .perm-ok-* markers etc.
        if entry.startswith(".perm-ok-") and not os.path.exists(
                os.path.join(SUPPORT_DIR, entry)):
            shutil.copy2(os.path.join(legacy, entry),
                         os.path.join(SUPPORT_DIR, entry))
    _MIGRATED = True
    return True


def ensure_dirs():
    migrate_legacy()
    os.makedirs(SUPPORT_DIR, exist_ok=True)
    os.makedirs(LOG_DIR, exist_ok=True)


def resource_dir() -> str:
    """Directory holding bundled resources (receiver scripts, icons).

    Works from a source checkout and from PyInstaller bundles."""
    if getattr(sys, "_MEIPASS", None):              # PyInstaller onefile/onedir
        return os.path.join(sys._MEIPASS, "receiver")
    # rdp_manager/paths.py -> project root
    return os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                        "receiver")


def receiver_script(name: str) -> str:
    return os.path.join(resource_dir(), name)


def open_path_in_gui(path: str):
    """Open a file/folder with the desktop's default handler."""
    if IS_MAC:
        subprocess_open(["open", path])
    else:
        subprocess_open(["xdg-open", path])


def subprocess_open(args):
    import subprocess
    subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
