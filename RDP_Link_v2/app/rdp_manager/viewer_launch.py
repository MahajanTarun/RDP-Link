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


def tk_probe_ok():
    """Run the Tk preflight; return True when Tk can initialise."""
    import subprocess
    try:
        if getattr(sys, "frozen", False):
            argv = [sys.executable, "--viewer", "--tk-probe"]
        else:
            app_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
            script = os.path.join(app_root, "office_viewer.py")
            argv = [sys.executable, script, "--tk-probe"]
        result = subprocess.run(argv, capture_output=True, text=True, timeout=10)
        return result.returncode == 0 and "TK_OK" in (result.stdout or "")
    except Exception:
        return False


def browser_viewer_url(address, password=None):
    """Build the host viewer URL with the password in the fragment."""
    base = address.rstrip("/")
    if not base.startswith("http"):
        base = "http://" + base
    if password:
        import urllib.parse
        return base + "/#" + urllib.parse.urlencode({"pw": password})
    return base + "/"


def open_viewer(address, password=None):
    """Open the viewer, falling back to the browser viewer when Tk is broken.

    Returns (used_browser: bool, argv_or_None).
    """
    import subprocess
    import webbrowser
    if tk_probe_ok():
        argv = viewer_argv(address, password)
        try:
            subprocess.Popen(argv)
            return False, argv
        except Exception:
            discard_password_file(argv)
    # Fallback: serve the same viewer page from the host.
    url = browser_viewer_url(address, password)
    try:
        webbrowser.open(url)
    except Exception:
        pass
    return True, None
