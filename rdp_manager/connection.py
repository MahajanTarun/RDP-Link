"""ConnectionController — builds FreeRDP args and manages connections.

The connect path is deliberately free of any blocking work: bridges spawn
in parallel, the FreeRDP process starts immediately, password goes straight
to its stdin. Feels like launching raw sdl-freerdp.
"""

import os
import subprocess
import sys
import threading

from . import secrets
from .exe import find_freerdp
from .bridges import BridgeManager
from .permissions import probe_device
from .paths import ASKPASS_PATH, IS_MAC


def format_host(host):
    """Wrap IPv6 literals in brackets for /v:[addr]:port."""
    host = (host or "").strip()
    if ":" in host and not host.startswith("["):
        return f"[{host}]"
    return host


def sanitize_port(port):
    port = str(port or "").strip()
    return port if port.isdigit() and 0 < int(port) < 65536 else "3389"


def build_args(profile, stdin_pass=False, home=None):
    """FreeRDP flags for a profile (executable not included).
    No password ever appears here."""
    home = home or os.path.expanduser("~")
    args = [f"/v:{format_host(profile['host'])}:{sanitize_port(profile['port'])}",
            f"/u:{profile['username']}"]
    if profile.get("smartSizing", True):
        args.append("/smart-sizing")
    if profile.get("audio") == "host":
        args.append("/sound")
    if profile.get("mic") == "native":
        args.append("/microphone")
    if profile.get("clipboard", True):
        args.append("/clipboard")
    if profile.get("fileTransfer", True):
        args.append(f"/drive:MacFiles,{home}")
    if profile.get("certTofu", True):
        args.append("/cert:tofu")
    if stdin_pass:
        args.append("/from-stdin")
    return args


def shell_quote_cmd(exe, profile, stdin_pass=False):
    """Copy-pasteable command (password placeholder, never the real one)."""
    import shlex
    parts = [exe] + build_args(profile, stdin_pass=stdin_pass)
    quoted = " ".join(shlex.quote(p) for p in parts)
    if stdin_pass:
        quoted = f"printf '%s\\n' 'YOUR_PASSWORD' | {quoted}"
    return quoted


class ConnectionController:
    def __init__(self, app):
        self.app = app
        self.processes = {}           # profile id -> FreeRDP Popen
        self.bridges = BridgeManager(app)

    def is_active(self, profile_id):
        p = self.processes.get(profile_id)
        return p is not None and p.poll() is None

    def any_active(self):
        return any(p.poll() is None for p in self.processes.values())

    # -- connect / disconnect -------------------------------------------------

    def connect(self, profile):
        pid = profile["id"]
        if self.is_active(pid):
            self.app.set_status(f"{profile['name']} is already connected")
            return
        exe = find_freerdp()
        if not exe:
            self.app.set_status(
                "Couldn't find sdl-freerdp/xfreerdp — install FreeRDP "
                "(macOS: brew install freerdp; Ubuntu: sudo apt install freerdp3-x11 "
                "or freerdp2-x11)", error=True)
            return

        password = None
        if profile.get("savePassword"):
            password = secrets.store_get(pid)
        use_stdin = password is not None and profile.get("stdinPass", True)

        args = [exe] + build_args(profile, stdin_pass=use_stdin)
        env = dict(os.environ)
        if password is not None and os.access(ASKPASS_PATH, os.X_OK):
            env["FREERDP_ASKPASS"] = ASKPASS_PATH
            env["RDP_PROFILE_ID"] = pid

        if profile.get("camera") == "host" or profile.get("mic") == "host":
            if not self.bridges.start(profile):
                return

        stdin = subprocess.PIPE if use_stdin else subprocess.DEVNULL
        logf = open_log_append()
        try:
            proc = subprocess.Popen(args, env=env, stdin=stdin,
                                    stdout=logf, stderr=logf)
        except OSError as e:
            self.bridges.stop(pid)
            self.app.set_status(f"Couldn't start FreeRDP: {e}", error=True)
            return

        if use_stdin:
            try:
                proc.stdin.write(password.encode("utf-8") + b"\n")
                proc.stdin.flush()
                proc.stdin.close()
            except (BrokenPipeError, OSError):
                pass   # may have already exited; watcher reports

        if IS_MAC and (profile.get("camera") == "host"
                       or profile.get("mic") == "host"):
            self.request_permissions(profile)

        self.processes[pid] = proc
        self.app.refresh_list()
        extras = []
        if profile.get("audio") == "host":
            extras.append("audio→Mac")
        if profile.get("mic") == "host":
            extras.append("mic bridge")
        elif profile.get("mic") == "native":
            extras.append("mic native")
        if profile.get("camera") == "host":
            extras.append("camera bridge")
        if profile.get("fileTransfer"):
            extras.append("files")
        suffix = f" ({', '.join(extras)})" if extras else ""
        direct = "direct" if use_stdin else "manual login"
        self.app.set_status(f"Connecting to {profile['name']}{suffix}… [{direct}]")
        threading.Thread(target=self._watch, args=(proc, profile),
                         daemon=True).start()

    def _watch(self, proc, profile):
        rc = proc.wait()
        self.app.root.after(0, lambda: self._on_exit(profile, rc))

    def _on_exit(self, profile, rc):
        self.processes.pop(profile["id"], None)
        self.bridges.stop(profile["id"])
        self.app.refresh_list()
        self.app.set_status(f"{profile['name']} disconnected (exit {rc})")

    def disconnect(self, profile):
        proc = self.processes.get(profile["id"])
        if proc is None or proc.poll() is not None:
            self.processes.pop(profile["id"], None)
            self.bridges.stop(profile["id"])
            self.app.refresh_list()
            self.app.set_status(f"{profile['name']} is not connected")
            return
        _terminate(proc)
        self.app.set_status(f"Disconnecting {profile['name']}…")

    def toggle(self, profile):
        if self.is_active(profile["id"]):
            self.disconnect(profile)
        else:
            self.connect(profile)

    def disconnect_all(self):
        for proc in list(self.processes.values()):
            _terminate(proc)
        self.bridges.stop_all()

    # -- permissions (macOS only) ---------------------------------------------

    def request_permissions(self, profile):
        if not IS_MAC:
            return
        def work():
            problems = []
            if profile.get("camera") == "host":
                ok, detail = probe_device("camera", profile.get("camDevice", "0"))
                if ok is False:
                    problems.append(("Camera", detail))
            if profile.get("mic") == "host":
                ok, detail = probe_device("microphone")
                if ok is False:
                    problems.append(("Microphone", detail))
            if problems:
                self.app.root.after(0, lambda: self.app.report_permission_denied(
                    profile, problems))
        threading.Thread(target=work, daemon=True).start()


def _terminate(proc, wait_s=3):
    """SIGTERM -> wait -> SIGKILL so a hung FreeRDP can never linger."""
    if proc.poll() is not None:
        return
    try:
        proc.terminate()
        proc.wait(timeout=wait_s)
    except subprocess.TimeoutExpired:
        try:
            proc.kill()
        except OSError:
            pass
    except OSError:
        pass


class HeadlessApp:
    """Minimal App interface for --connect CLI mode (no GUI, no Tk)."""

    def __init__(self):
        self.root = _HeadlessRoot()

    def set_status(self, text, error=False, warn=False):
        stream = sys.stderr
        print(f"[rdp] {text}", file=stream, flush=True)

    def refresh_list(self):
        pass

    def report_permission_denied(self, profile, problems):
        names = " and ".join(n for n, _ in problems)
        print(f"[rdp] WARNING: {names} permission denied — grant it to the "
              f"app in System Settings", file=sys.stderr)

    def connect_and_wait(self, profile):
        ctl = ConnectionController(self)
        ctl.connect(profile)
        proc = ctl.processes.get(profile["id"])
        if not proc:
            return 1
        try:
            rc = proc.wait()
        except KeyboardInterrupt:
            _terminate(proc)
            rc = proc.wait()
        finally:
            ctl.bridges.stop(profile["id"])
        return rc if isinstance(rc, int) else 0


def open_log_append():
    from .logutil import current_log_path
    return open(current_log_path(), "a", encoding="utf-8")


class _HeadlessRoot:
    """after() shim so BridgeManager timers work without Tk."""

    def after(self, ms, func=None):
        import threading
        t = threading.Timer(ms / 1000.0, func)
        t.daemon = True
        t.start()
        return t
