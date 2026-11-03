"""Build the command line for the guest viewer window.

Frozen builds (PyInstaller on Windows, Linux and macOS) re-enter the same
executable with ``--viewer`` so the viewer ships inside the app bundle and
never depends on a system Python. Source checkouts run ``office_viewer.py``
with the current interpreter.
"""

import os
import sys
import tempfile


def write_password_file(password):
    """Store a password in a private file and return its path.

    The password is not passed on the command line, where every local user
    could read it from the process list.
    """
    fd, path = tempfile.mkstemp(prefix="rdplink-pw-", suffix=".txt")
    with os.fdopen(fd, "w") as fh:
        fh.write(password)
    os.chmod(path, 0o600)
    return path


def viewer_argv(address, password=None):
    """Return the argv list that opens the viewer for *address*."""
    tail = [address]
    if password:
        tail += ["--password-file", write_password_file(password)]

    if getattr(sys, "frozen", False):
        return [sys.executable, "--viewer"] + tail

    app_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    script = os.path.join(app_root, "office_viewer.py")
    return [sys.executable, script] + tail
