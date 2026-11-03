"""Build the command line for the guest viewer window.

Frozen builds (PyInstaller on Windows, Linux and macOS) re-enter the same
executable with ``--viewer`` so the viewer ships inside the app bundle and
never depends on a system Python. Source checkouts run ``office_viewer.py``
with the current interpreter.
"""

import os
import sys


def viewer_argv(address, password=None):
    """Return the argv list that opens the viewer for *address*."""
    tail = [address]
    if password:
        tail += ["--password", password]

    if getattr(sys, "frozen", False):
        return [sys.executable, "--viewer"] + tail

    app_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    script = os.path.join(app_root, "office_viewer.py")
    return [sys.executable, script] + tail
