"""Build the command line for the guest viewer window.

Frozen builds (PyInstaller on Windows, Linux and macOS) re-enter the same
executable with ``--viewer`` so the viewer ships inside the app bundle and
never depends on a system Python. Source checkouts run ``office_viewer.py``
with the current interpreter.
"""

import os
import sys
import tempfile
import time


def _cleanup_stale_password_files(max_age=3600):
    """Remove abandoned viewer credential files from failed launches."""
    now = time.time()
    try:
        names = os.listdir(tempfile.gettempdir())
    except OSError:
        return
    for name in names:
        if not (name.startswith("rdplink-pw-") and name.endswith(".txt")):
            continue
        path = os.path.join(tempfile.gettempdir(), name)
        try:
            if now - os.stat(path).st_mtime > max_age:
                os.unlink(path)
        except OSError:
            pass


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
    _cleanup_stale_password_files()
    tail = [address]
    if password:
        tail += ["--password-file", write_password_file(password)]

    if getattr(sys, "frozen", False):
        return [sys.executable, "--viewer"] + tail

    app_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    script = os.path.join(app_root, "office_viewer.py")
    return [sys.executable, script] + tail


def discard_password_file(argv):
    """Remove the private password file if the viewer could not be spawned."""
    try:
        index = argv.index("--password-file")
        os.unlink(argv[index + 1])
    except (ValueError, IndexError, OSError):
        pass
