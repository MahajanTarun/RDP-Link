"""Platform paths and small platform helpers (macOS / Linux client)."""

import os
import sys

IS_MAC = sys.platform == "darwin"
IS_LINUX = sys.platform.startswith("linux")


def _support_dir() -> str:
    if IS_MAC:
        return os.path.expanduser("~/Library/Application Support/Ubuntu RDP Manager")
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "ubuntu-rdp-manager")


SUPPORT_DIR = _support_dir()
PROFILES_FILE = os.path.join(SUPPORT_DIR, "profiles.json")
ASKPASS_PATH = os.path.join(SUPPORT_DIR, "rdp-askpass")
LOG_DIR = os.path.join(SUPPORT_DIR, "logs")


def ensure_dirs():
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
