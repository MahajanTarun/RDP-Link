"""Parent-death watchdog for device-touching child processes.

Every bridge (camera/mic ffmpeg) and every permission probe runs under
this tiny wrapper instead of being spawned directly by the app. The
wrapper watches the app's PID; if the app disappears — normal quit,
crash, or force-kill — the wrapper terminates the child within ~1 second
so the camera/mic can never be left on by an orphaned process.

The FreeRDP session itself is deliberately NOT wrapped: losing the app
must never kill a live remote desktop the user may be working in.
Device privacy is the hard guarantee; the session is the user's to keep.

Protocol:  <this binary> --bridge-watchdog <parent_pid> -- <child args…>
Exit codes: child's exit code; 99 = watchdog killed the child (parent gone).
"""

import os
import signal
import subprocess
import sys
import time

WATCHDOG_KILLED = 99
_POLL_S = 0.5


def spawn(child_args, stdout=None, stderr=None, stdin=None, **popen_kwargs):
    """Spawn `child_args` under the watchdog. Returns the wrapper Popen —
    same interface as a direct Popen: wait()/poll()/stderr all forward the
    child's behavior (any extra Popen kwargs are passed through so callers
    can keep using text=True, bufsize=1, …).
    Works from source runs (python3) and frozen bundles (sys.executable)."""
    script = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "ubuntu_rdp_manager.py")
    if getattr(sys, "_MEIPASS", None):           # frozen: re-exec own binary
        wrapper_argv = [sys.executable, "--bridge-watchdog",
                        str(os.getpid()), "--", *child_args]
    else:                                        # source: run this file with python
        wrapper_argv = [sys.executable, script, "--bridge-watchdog",
                        str(os.getpid()), "--", *child_args]
    try:
        return subprocess.Popen(wrapper_argv, stdout=stdout,
                                stderr=stderr, stdin=stdin, **popen_kwargs)
    except OSError:
        # No fallback silent spawn: if the wrapper can't start, spawning a
        # plain child would reintroduce the orphan risk — surface it.
        raise


def _parent_alive(parent_pid):
    try:
        return os.getppid() == int(parent_pid)
    except (OSError, ValueError):
        return False


def _kill_child(proc):
    try:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                proc.kill()
    except OSError:
        pass


def run(parent_pid, child_args):
    """Watchdog main loop (called by the entry point's --bridge-watchdog).
    Blocks; exits with the child's exit code, or WATCHDOG_KILLED."""
    if not child_args:
        sys.stderr.write("[rdp-watchdog] no child command given\n")
        return 2
    proc = None
    def _forward(_signum, _frame):
        if proc is not None:
            _kill_child(proc)
        sys.exit(128 + (_signum or signal.SIGTERM))

    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        try:
            signal.signal(sig, _forward)
        except (OSError, ValueError):
            pass

    try:
        proc = subprocess.Popen(child_args)   # stdio inherited → forwarding free
    except OSError as e:
        sys.stderr.write(f"[rdp-watchdog] spawn failed: {e}\n")
        return 1

    while True:
        rc = proc.poll()
        if rc is not None:
            return rc
        if not _parent_alive(parent_pid):
            _kill_child(proc)
            return WATCHDOG_KILLED
        time.sleep(_POLL_S)
