"""Setup assistant: detect and fix OS prerequisites.

The goal is the shortest possible path from "installed" to "working": every
capability the app needs is probed, and a Fix button triggers the exact OS
prompt, deep link or privileged command. Consents that the OS reserves for the
user (screen recording, accessibility, camera, microphone) are requested, not
bypassed.
"""

import json
import os
import platform
import shlex
import shutil
import subprocess
import sys

PLATFORM = platform.system()


def _run(cmd, timeout=8):
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        return out.returncode, (out.stdout or "") + (out.stderr or "")
    except Exception as exc:  # noqa: BLE001 - reported to the user
        return 1, str(exc)


def _helper():
    """Locate the bundled macOS TCC helper."""
    here = os.path.dirname(os.path.abspath(__file__))
    app_root = os.path.dirname(here)
    candidates = [
        os.path.join(app_root, "assets", "rdp-link-tcc"),
        os.path.join(getattr(sys, "_MEIPASS", ""), "assets", "rdp-link-tcc"),
        os.path.join(os.path.dirname(sys.executable), "tools", "rdp-link-tcc"),
        os.path.join(app_root, "..", "dist", "macos", "bin", "rdp-link-tcc"),
        "/tmp/rdp-link-tcc",
    ]
    for path in candidates:
        if path and os.path.exists(path):
            return path
    return None


def _tcc(command="status"):
    helper = _helper()
    if not helper:
        return None
    code, out = _run([helper, command], timeout=12)
    if code != 0:
        return None
    try:
        return json.loads(out.strip().splitlines()[-1])
    except Exception:
        return None


def _agent_binary():
    here = os.path.dirname(os.path.abspath(__file__))
    app_root = os.path.dirname(here)
    candidates = [
        os.path.join(app_root, "dist", "macos", "RDP Link.app", "Contents", "MacOS", "office-rdp"),
        os.path.join(os.path.dirname(sys.executable), "office-rdp"),
        os.path.join(os.path.dirname(sys.executable), "office-rdp.exe"),
        "/usr/bin/office-rdp",
        shutil.which("office-rdp") or "",
    ]
    for path in candidates:
        if path and os.path.exists(path):
            return path
    return None


def _item(key, label, status, detail, fix=None, action=None):
    return {"key": key, "label": label, "status": status, "detail": detail,
            "fix": fix, "action": action}


# ---------------------------------------------------------------- macOS ----
def _macos_items():
    items = []
    tcc = _tcc("status") or {}

    def add_tcc(key, label, value, request, pane):
        ok = value is True or value == "granted"
        status = "ok" if ok else "fail"
        detail = "Granted" if ok else "Not granted"
        if value == "restricted":
            detail = "Restricted by policy"
        def fix():
            if request:
                _tcc(request)
            if pane:
                _tcc(pane)
            return True
        items.append(_item(key, label, status, detail,
                           None if ok else "Open settings", fix))

    if "screen" in tcc:
        add_tcc("screen", "Screen recording", tcc.get("screen"),
                "request-screen", "open-screen")
        add_tcc("accessibility", "Accessibility (remote input)",
                tcc.get("accessibility"), "request-accessibility", "open-accessibility")
        cam = tcc.get("camera", "unknown")
        add_tcc("camera", "Camera", cam, "request-camera", "open-camera")
        mic = tcc.get("microphone", "unknown")
        add_tcc("microphone", "Microphone", mic, "request-microphone", "open-microphone")
    else:
        items.append(_item("tcc", "Privacy permissions", "warn",
                           "Bundled permission helper not found — open System Settings → Privacy",
                           "Open Privacy", lambda: _open_url(
                               "x-apple.systempreferences:com.apple.preference.security?Privacy")))

    # Firewall: the Application Firewall can block incoming LAN connections.
    agent = _agent_binary()
    if agent:
        code, out = _run(["/usr/libexec/ApplicationFirewall/socketfilterfw",
                          "--getappblocked", agent])
        blocked = "blocked" in out.lower()
        items.append(_item("firewall", "Incoming firewall",
                           "fail" if blocked else "ok",
                           "Blocked" if blocked else "Allowed",
                           None if not blocked else "Allow",
                           None if not blocked else (lambda: _mac_firewall(agent))))
    # Background host at login. The agent writes this LaunchAgent itself
    # (office-rdp --install-service).
    plists = [
        os.path.expanduser("~/Library/LaunchAgents/com.eminentailabs.officerdp.plist"),
        os.path.expanduser("~/Library/LaunchAgents/com.eminentailabs.rdplink.host.plist"),
    ]
    installed = any(os.path.exists(path) for path in plists)
    items.append(_item("service", "Start host at login",
                       "ok" if installed else "warn",
                       "Installed" if installed else "Not installed",
                       None if installed else "Install",
                       None if installed else _mac_service))

    items.append(_item("vcam", "Virtual camera", "info",
                       "Optional: requires a camera extension (Apple signing) or OBS"))
    items.append(_item("usb", "USB redirection", "info",
                       "Optional: requires VirtualHere or a DriverKit USB extension"))
    return items


def _applescript_string(value):
    """Escape a value so it can be used inside an AppleScript string literal."""
    return value.replace("\\", "\\\\").replace('"', '\\"')


def _mac_firewall_script(agent):
    """Build the AppleScript that allows the agent through the firewall.

    The path is quoted for the shell and escaped for the AppleScript
    literal. Without both, a path with a space or an apostrophe (for
    example "/Volumes/Tarun's SSD/...") breaks the command.
    """
    quoted = _applescript_string(shlex.quote(agent))
    return (
        'do shell script "/usr/libexec/ApplicationFirewall/socketfilterfw --add '
        f"{quoted} && /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "
        f'{quoted}" with administrator privileges'
    )


def _mac_firewall(agent):
    _run(["osascript", "-e", _mac_firewall_script(agent)], timeout=60)
    return True


def _mac_service():
    agent = _agent_binary()
    if not agent:
        return False
    _run([agent, "--install-service"], timeout=30)
    return True


def _open_url(url):
    if PLATFORM == "Darwin":
        _run(["open", url])
    elif PLATFORM == "Windows":
        os.startfile(url)  # type: ignore[attr-defined]
    else:
        _run(["xdg-open", url])
    return True


# -------------------------------------------------------------- Windows ----
def _windows_items():
    items = []
    code, out = _run(["netsh", "advfirewall", "firewall", "show", "rule",
                      "name=RDP Link"])
    allowed = code == 0 and "RDP Link" in out
    items.append(_item("firewall", "Windows Firewall rule",
                       "ok" if allowed else "fail",
                       "Present" if allowed else "Missing",
                       None if allowed else "Allow",
                       None if allowed else _win_firewall))
    # Startup entry for the launcher.
    import winreg  # noqa: PLC0415 - Windows only
    key = r"Software\Microsoft\Windows\CurrentVersion\Run"
    found = False
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, key) as h:
            winreg.QueryValueEx(h, "RDP Link")
            found = True
    except OSError:
        pass
    items.append(_item("service", "Start at login",
                       "ok" if found else "warn",
                       "Installed" if found else "Not installed",
                       None if found else "Install",
                       None if found else _win_startup))
    items.append(_item("camera", "Camera & microphone access", "info",
                       "Open Windows privacy settings if access is blocked",
                       "Open settings", lambda: _open_url("ms-settings:privacy-webcam")))
    items.append(_item("vcam", "Virtual camera", "info",
                       "Windows 11 supports a user-mode virtual camera without a driver"))
    items.append(_item("usb", "USB redirection", "info",
                       "Requires the usbip-win2 signed driver or VirtualHere"))
    return items


def _win_firewall():
    params = (
        'advfirewall firewall add rule name="RDP Link" dir=in action=allow '
        'program="%s" enable=yes profile=private' % (sys.executable,)
    )
    try:
        import ctypes  # noqa: PLC0415
        ctypes.windll.shell32.ShellExecuteW(None, "runas", "netsh", params, None, 1)
    except Exception:
        return False
    return True


def _win_startup():
    import winreg  # noqa: PLC0415
    key = r"Software\Microsoft\Windows\CurrentVersion\Run"
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, key, 0, winreg.KEY_SET_VALUE) as h:
        winreg.SetValueEx(h, "RDP Link", 0, winreg.REG_SZ, f'"{sys.executable}"')
    return True


# ---------------------------------------------------------------- Linux ----
def _linux_items():
    items = []
    # Input injection: uinput access or the input group.
    uinput = os.path.exists("/dev/uinput")
    writable = uinput and os.access("/dev/uinput", os.W_OK)
    in_input = "input" in _groups()
    ok = writable or in_input
    items.append(_item("input", "Remote input (uinput)",
                       "ok" if ok else ("warn" if uinput else "fail"),
                       "Accessible" if ok else "Needs the input group or a udev rule",
                       None if ok else "Fix",
                       None if ok else _linux_input))
    # Firewall
    code, out = _run(["ufw", "status"])
    if code == 0:
        allowed = "8088" in out and "ALLOW" in out
        items.append(_item("firewall", "Firewall (ufw) port 8088",
                           "ok" if allowed else "warn",
                           "Allowed" if allowed else "Not allowed",
                           None if allowed else "Allow",
                           None if allowed else _linux_firewall))
    # Modules for virtual camera / USB export.
    vcam = os.path.exists("/sys/module/v4l2loopback")
    items.append(_item("vcam", "Virtual camera (v4l2loopback)",
                       "ok" if vcam else "warn",
                       "Loaded" if vcam else "Not loaded",
                       None if vcam else "Load",
                       None if vcam else (lambda: _pkexec(
                           "modprobe v4l2loopback video_nr=10 card_label='RDP Link Camera'"))))
    usbip = os.path.exists("/sys/module/usbip_host")
    items.append(_item("usbip", "USB/IP export (usbip-host)",
                       "ok" if usbip else "warn",
                       "Loaded" if usbip else "Not loaded",
                       None if usbip else "Load",
                       None if usbip else (lambda: _pkexec("modprobe usbip-host"))))
    # User service
    code, out = _run(["systemctl", "--user", "is-enabled", "office-rdp.service"])
    enabled = code == 0 and "enabled" in out
    items.append(_item("service", "Host service at login",
                       "ok" if enabled else "warn",
                       "Enabled" if enabled else "Not enabled",
                       None if enabled else "Install",
                       None if enabled else _linux_service))
    return items


def _groups():
    try:
        import grp  # noqa: PLC0415
        return {grp.getgrgid(g).gr_name for g in os.getgroups()}
    except Exception:
        return set()


def _pkexec(command):
    _run(["pkexec", "sh", "-c", command], timeout=120)
    return True


def _linux_input():
    return _pkexec(
        "groupadd -f input; usermod -aG input $PKEXEC_UID 2>/dev/null; "
        "printf 'KERNEL==\"uinput\", MODE=\"0660\", GROUP=\"input\"\\n' "
        "> /etc/udev/rules.d/60-rdp-link-uinput.rules; udevadm control --reload")


def _linux_firewall():
    return _pkexec("ufw allow 8088/tcp")


def _linux_service():
    unit = os.path.expanduser("~/.config/systemd/user/office-rdp.service")
    os.makedirs(os.path.dirname(unit), exist_ok=True)
    agent = shutil.which("office-rdp") or "office-rdp"
    with open(unit, "w") as fh:
        fh.write(
            "[Unit]\nDescription=RDP Link host\n\n"
            f"[Service]\nExecStart={shlex.quote(agent)} --host --port 8088\n"
            "Restart=always\n\n"
            "[Install]\nWantedBy=default.target\n")
    _run(["systemctl", "--user", "daemon-reload"])
    _run(["systemctl", "--user", "enable", "--now", "office-rdp.service"])
    return True


# ---------------------------------------------------------------- public ----
def collect():
    if PLATFORM == "Darwin":
        return _macos_items()
    if PLATFORM == "Windows":
        return _windows_items()
    return _linux_items()


def run_action(item):
    action = item.get("action")
    if not action:
        return False
    try:
        return bool(action())
    except Exception:
        return False


def summarize(items):
    bad = sum(1 for i in items if i["status"] in ("fail", "warn"))
    return bad, len(items)
