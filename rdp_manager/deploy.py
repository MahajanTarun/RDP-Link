"""Zero-setup host side: push receiver scripts to the Linux host over SSH.

No SSH libraries — uses the system `ssh`/`scp` binaries. The install runs in
a visible terminal window so the user can answer sudo prompts; after that
the receivers run as systemd services and no setup is ever needed again.
"""

import subprocess

from .paths import receiver_script, IS_MAC

CAMERA_SCRIPT = "camera-receiver.sh"
MIC_SCRIPT = "mic-receiver.sh"


def setup_command(host, username=None, port=None):
    """Shell one-liner that installs both receivers on `host`."""
    user_at = f"{username}@{host}" if username else host
    port_flag = f"-p {port} " if port and str(port) != "3389" else ""
    scripts = (f'"{receiver_script(CAMERA_SCRIPT)}" '
               f'"{receiver_script(MIC_SCRIPT)}"')
    remote = ("sudo -v; bash /tmp/urdp-camera-receiver.sh --install; "
              "bash /tmp/urdp-mic-receiver.sh --install; "
              "rm -f /tmp/urdp-camera-receiver.sh /tmp/urdp-mic-receiver.sh")
    return (f"scp -o ConnectTimeout=8 {port_flag}{scripts} "
            f"{user_at}:/tmp/ && "
            f"ssh -tt -o ConnectTimeout=8 {port_flag}{user_at} \"{remote}\"")


def open_terminal(cmd):
    """Run `cmd` in a visible terminal window; returns True when launched."""
    try:
        if IS_MAC:
            subprocess.run(
                ["osascript", "-e",
                 f'tell application "Terminal"\n'
                 f'do script "{cmd}"\nactivate\nend tell'],
                check=True, capture_output=True, timeout=10)
            return True
        for term, style in (("gnome-terminal", ["--"]),
                            ("konsole", ["-e"]),
                            ("x-terminal-emulator", ["-e"]),
                            ("xterm", ["-e"])):
            exe = None
            from shutil import which
            exe = which(term)
            if exe:
                subprocess.Popen([exe] + style + [cmd],
                                 stdout=subprocess.DEVNULL,
                                 stderr=subprocess.DEVNULL)
                return True
    except (OSError, subprocess.SubprocessError, subprocess.TimeoutExpired):
        pass
    return False


def deploy_receivers(profile):
    """Open a terminal that installs the receivers on the profile's host."""
    cmd = setup_command(profile["host"], profile.get("username"),
                        profile.get("port"))
    return open_terminal(cmd)


def copy_setup_command(profile) -> str:
    return setup_command(profile["host"], profile.get("username"),
                         profile.get("port"))


def receiver_health_hint(host):
    """Manual diagnostic command shown when handshake fails."""
    return (f"ssh {host} -- '/usr/local/bin/ubuntu-rdp-camera-receiver "
            f"--check || ~/camera-receiver.sh --check'")
