"""macOS TCC permission probing for camera/microphone.

Opening the device via ffmpeg is what triggers the macOS permission prompt,
attributed to the app when running from the signed .app bundle.
On Linux this module is a no-op (no TCC).

In-flight probe processes are tracked so a quick app quit can never leave
an orphaned ffmpeg holding the camera.
"""

import atexit
import subprocess
import sys

from .exe import find_ffmpeg

CAMERA_SIZE = "1280x720"

_inflight = set()


def _kill_inflight():
    for proc in list(_inflight):
        try:
            if proc.poll() is None:
                proc.terminate()
        except OSError:
            pass
    _inflight.clear()


atexit.register(_kill_inflight)


def _run_probe(args, timeout):
    """subprocess.run that also registers the child for atexit cleanup."""
    proc = subprocess.Popen(args, stdout=subprocess.DEVNULL,
                            stderr=subprocess.PIPE, text=True)
    _inflight.add(proc)
    try:
        _, err = proc.communicate(timeout=timeout)
        return proc.returncode, err or ""
    except subprocess.TimeoutExpired:
        proc.terminate()
        try:
            proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            proc.kill()
        return None, "timed out"
    finally:
        _inflight.discard(proc)


def probe_device(kind, device="0", timeout=8):
    """Return (ok, detail): True/False = known result, None = undetermined.
    detail == 'denied' means the macOS TCC permission is off."""
    if not sys.platform == "darwin":
        return (True, "")  # Linux has no TCC; nothing to pre-probe
    ff = find_ffmpeg()
    if not ff:
        return (None, "ffmpeg not found")
    if kind == "camera":
        args = [ff, "-hide_banner", "-loglevel", "error", "-f", "avfoundation",
                "-framerate", "30", "-video_size", CAMERA_SIZE,
                "-pixel_format", "uyvy422", "-i", str(device),
                "-frames:v", "1", "-f", "null", "-"]
    else:  # microphone
        args = [ff, "-hide_banner", "-loglevel", "error", "-f", "avfoundation",
                "-i", ":0", "-t", "0.5", "-ac", "1", "-ar", "48000",
                "-f", "null", "-"]
    try:
        rc, err = _run_probe(args, timeout=timeout)
    except OSError as e:
        return (None, str(e))
    if rc == 0:
        return (True, "")
    if rc is None:
        return (None, "timed out")
    err = err.strip()
    low = err.lower()
    if ("denied" in low or "not authorized" in low or "no permission" in low
            or "not permitted" in low or "not allowed" in low):
        return (False, "denied")
    last = err.splitlines()[-1] if err else f"exit {rc}"
    return (False, last)
