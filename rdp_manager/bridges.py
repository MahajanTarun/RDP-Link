"""Camera / microphone bridges + receiver handshake.

Each bridge streams ffmpeg output over UDP to the Linux host, where
receiver/camera-receiver.sh (v4l2loopback webcam) or receiver/mic-receiver.sh
(PipeWire/Pulse "MacMic" source) feeds the remote session.

v4 additions:
- quality: explicit MPEG-2 bitrate (ffmpeg's 200 kbit/s default was unusable)
- handshake: receiver scripts run a tiny control responder (UDP). The app
  pings it to report "receiver detected / not reachable" instead of firing
  packets into the void.
- logs: bridge stderr is appended to the session log.
- race-safe watchers and terminate->wait->kill cleanup.
"""

import os
import re
import signal
import socket
import subprocess
import sys
import threading

from .exe import find_ffmpeg
from .logutil import current_log_path

CAMERA_PORT = 7272
MIC_PORT = 7273
CAMERA_CTL_PORT = 7274
MIC_CTL_PORT = 7275
CAMERA_SIZE = "1280x720"
CAMERA_FPS = "30"

# libav options that fix the "default 200 kbit/s MPEG-2" quality problem
CAMERA_QUALITY = ["-b:v", "3M", "-maxrate", "5M", "-bufsize", "2M", "-g", "30"]
MIC_QUALITY = ["-c:a", "mp2", "-b:a", "128k"]

_PING = b"RDPCTL1 PING"


# ------------------------------------------------------------- handshake --

def probe_receiver(host, ctl_port, timeout=1.5, attempts=2):
    """Ping a receiver control responder. Returns dict(role=..., data=...)
    or None when the receiver (or the network path) is unreachable.
    Retries so a just-starting receiver isn't lost to an early ICMP refusal."""
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.settimeout(timeout)
    try:
        for _ in range(attempts):
            try:
                s.sendto(_PING, (host, ctl_port))
                data, _ = s.recvfrom(512)
            except (ConnectionRefusedError, socket.timeout):
                continue          # responder starting / network path cold
            except OSError:
                return None
            text = data.decode("utf-8", "replace").strip()
            if not text.startswith("RDPCTL1 PONG"):
                continue
            out = {}
            for token in text.split():
                if "=" in token:
                    k, v = token.split("=", 1)
                    out[k] = v
            return out
        return None
    finally:
        s.close()


# ---------------------------------------------------- device enumeration --

def list_cameras():
    """Cross-platform camera list as ['0 — Name', ...] (video devices only)."""
    ff = find_ffmpeg()
    if not ff:
        return ["0"]
    if sys.platform == "darwin":
        try:
            r = subprocess.run([ff, "-hide_banner", "-list_devices", "true",
                                "-f", "avfoundation", "-i", ""],
                               capture_output=True, text=True, timeout=10)
        except (OSError, subprocess.TimeoutExpired):
            return ["0"]
        devs, in_video = [], False
        for line in r.stderr.splitlines():
            if "AVFoundation video devices" in line:
                in_video = True
                continue
            if "AVFoundation audio devices" in line:
                break  # audio entries must NOT appear in the camera picker
            m = re.search(r"\[(\d+)\]\s+(.+?)\s*$", line)
            if in_video and m:
                devs.append(f"{m.group(1)} — {m.group(2)}")
        return devs or ["0"]
    # Linux: v4l2-ctl when available, else raw /dev/video*
    try:
        r = subprocess.run(["v4l2-ctl", "--list-devices"],
                           capture_output=True, text=True, timeout=5)
        if r.returncode == 0:
            devs, name = [], None
            for line in r.stdout.splitlines():
                if not line.strip():
                    name = None
                elif not line.startswith("\t"):
                    name = line.strip()
                elif name and "/dev/video" in line:
                    node = line.strip().split()[0]
                    devs.append(f"{node.split('/')[-1]} — {name}")
            if devs:
                return devs
    except (OSError, subprocess.TimeoutExpired):
        pass
    try:
        return [p for p in sorted(os.listdir("/dev")) if p.startswith("video")] or ["0"]
    except OSError:
        return ["0"]


def _camera_input_args(device):
    if sys.platform == "darwin":
        return ["-f", "avfoundation", "-framerate", CAMERA_FPS,
                "-video_size", CAMERA_SIZE, "-pixel_format", "uyvy422",
                "-i", str(device)]
    return ["-f", "v4l2", "-i", str(device)]


def _mic_input_args():
    if sys.platform == "darwin":
        return ["-f", "avfoundation", "-i", ":0"]
    return ["-f", "pulse", "-i", "default"]


def _camera_source_hint(device, err):
    """Translate common camera-open failures into human advice."""
    low = (err or "").lower()
    if sys.platform == "darwin":
        if "denied" in low or "not authorized" in low:
            return ("macOS camera permission is off — System Settings → "
                    "Privacy & Security → Camera, then press Test… again")
        if "input/output error" in low or "timed out" in low:
            return (f"device {device} didn't open — pick the right camera "
                    f"in the device list and press Test…")
    else:
        if "permission denied" in low:
            return f"no access to {device} — add your user to the 'video' group"
    return (err or "").splitlines()[-1] if err else "unknown error"


# ---------------------------------------------------------------- engine ---

class BridgeManager:
    """Owns bridge processes for all profiles + their health monitors."""

    def __init__(self, app):
        self.app = app
        self.camera_procs = {}   # profile id -> Popen
        self.mic_procs = {}
        self._recv_ok = {}       # profile id -> bool | None (last handshake)

    # -- lifecycle ---------------------------------------------------------

    def start(self, profile):
        """Start the bridges this profile needs and schedule the receiver
        handshake. Returns True when everything spawned."""
        pid = profile["id"]
        ok = True
        if profile.get("camera") == "host":
            ok = self.start_camera(profile)
        if ok and profile.get("mic") == "host":
            ok = self.start_mic(profile)
        if ok:
            self._recv_ok[pid] = None
            self.schedule_health(profile, delay=2500, announce=True)
        return ok

    def stop(self, profile_id):
        self._terminate(self.camera_procs.pop(profile_id, None))
        self._terminate(self.mic_procs.pop(profile_id, None))
        self._recv_ok.pop(profile_id, None)

    def stop_all(self):
        for group in (self.camera_procs, self.mic_procs):
            for proc in list(group.values()):
                self._terminate(proc)
        self.camera_procs.clear()
        self.mic_procs.clear()
        self._recv_ok.clear()

    def active(self):
        return any(p.poll() is None for p in
                   list(self.camera_procs.values()) + list(self.mic_procs.values()))

    # -- process helpers -----------------------------------------------------

    @staticmethod
    def _terminate(proc, wait_s=3):
        if proc is None:
            return
        if proc.poll() is None:
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

    def _log(self, label, proc):
        """Route a bridge's stderr into the session log; race-safe pop."""
        tag = label
        with open(current_log_path(), "a", encoding="utf-8") as lf:
            lf.write(f"--- {tag} bridge started ---\n")
            lf.flush()
            while proc.poll() is None:
                line = proc.stderr.readline()
                if line:
                    lf.write(line)
                    lf.flush()

    def _watch(self, label, proc, profile, procmap):
        rc = proc.wait()
        if procmap.get(profile["id"]) is proc:      # race guard: only pop our own
            procmap.pop(profile["id"], None)
        if rc not in (0, 0 - signal.SIGTERM):
            self.app.root.after(0, lambda: self.app.set_status(
                f"{label} bridge stopped (exit {rc}) — is the receiver script "
                f"running on {profile['host']}? See session log.", error=True))

    # -- bridges -------------------------------------------------------------

    def start_camera(self, profile):
        ff = find_ffmpeg()
        if not ff:
            self.app.set_status("Camera bridge needs ffmpeg", error=True)
            return False
        dev = str(profile.get("camDevice", "0") or "0")
        args = [ff, "-hide_banner", "-loglevel", "warning",
                ] + _camera_input_args(dev) + [
                "-f", "mpegts", "-pkt_size", "1316"] + CAMERA_QUALITY + [
                f"udp://{profile['host']}:{CAMERA_PORT}"]
        return self._spawn("Camera", args, profile, self.camera_procs)

    def start_mic(self, profile):
        ff = find_ffmpeg()
        if not ff:
            self.app.set_status("Microphone bridge needs ffmpeg", error=True)
            return False
        args = [ff, "-hide_banner", "-loglevel", "warning",
                ] + _mic_input_args() + [
                "-ac", "1", "-ar", "48000",
                "-f", "mpegts", "-pkt_size", "1316"] + MIC_QUALITY + [
                f"udp://{profile['host']}:{MIC_PORT}"]
        return self._spawn("Microphone", args, profile, self.mic_procs)

    def _spawn(self, label, args, profile, procmap):
        try:
            proc = subprocess.Popen(args, stdout=subprocess.DEVNULL,
                                    stderr=subprocess.PIPE, text=True,
                                    bufsize=1)
        except OSError as e:
            self.app.set_status(f"Couldn't start {label.lower()} bridge: {e}",
                                error=True)
            return False
        procmap[profile["id"]] = proc
        threading.Thread(target=self._log, daemon=True,
                         args=(label, proc)).start()
        threading.Thread(target=self._watch, daemon=True,
                         args=(label, proc, profile, procmap)).start()
        self.app.root.after(2500, lambda: self._health(
            label, proc, profile, procmap))
        return True

    # -- health ---------------------------------------------------------------

    def schedule_health(self, profile, delay, announce=False):
        self.app.root.after(delay, lambda: self._monitor(profile, announce))

    def _monitor(self, profile, announce=False):
        pid = profile["id"]
        active = ((self.camera_procs.get(pid) and profile.get("camera") == "host") or
                  (self.mic_procs.get(pid) and profile.get("mic") == "host"))
        if not active:
            return
        self._handshake(profile, announce)
        self.schedule_health(profile, delay=15000)   # quiet re-check

    def _handshake(self, profile, announce):
        pid = profile["id"]
        checks = []
        if self.camera_procs.get(pid) and profile.get("camera") == "host":
            checks.append(("Camera", CAMERA_CTL_PORT))
        if self.mic_procs.get(pid) and profile.get("mic") == "host":
            checks.append(("Microphone", MIC_CTL_PORT))
        for label, port in checks:
            resp = probe_receiver(profile["host"], port)
            ok = bool(resp)
            if self._recv_ok.get(pid) != ok or announce:
                if ok:
                    data = resp.get("data", "open")
                    if data == "open":
                        msg = f"{label} bridge: receiver detected on {profile['host']} ✓"
                    else:
                        msg = (f"{label}: receiver script running but its data "
                               f"receiver is down — restart it on {profile['host']}")
                    self.app.set_status(msg, error=(data != "open"))
                else:
                    self.app.set_status(
                        f"{label}: no receiver response on {profile['host']} — "
                        f"right-click the profile → “Set up receivers on host…”",
                        warn=True)
            self._recv_ok[pid] = ok

    def _health(self, label, proc, profile, procmap, first=False):
        """2.5 s after spawn: did the bridge die immediately?"""
        if procmap.get(profile["id"]) is not proc or proc.poll() is None:
            return
        self.app.set_status(
            f"{label} stream stopped (exit {proc.returncode}) — check macOS "
            f"{label.lower()} permission and the session log", error=True)
