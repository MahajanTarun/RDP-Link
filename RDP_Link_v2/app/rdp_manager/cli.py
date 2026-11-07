"""Headless RDP Link CLI.

Runs without importing tkinter: identity, status, screen sharing, remote
checks, screenshots, clipboard and input can all be driven from a terminal.

    rdp-link --id
    rdp-link --status
    rdp-link --share --port 8088
    rdp-link --connect 192.168.1.20:8088 --probe
    rdp-link --connect 123456789 --rendezvous https://relay.example.com
    rdp-link --connect HOST --save-frame shot.jpg
    rdp-link --connect HOST --clipboard-get
    rdp-link --connect HOST --type "hello" --click 120,80

The legacy FreeRDP profile manager keeps working:

    rdp-link --list
    rdp-link --connect-profile "Profile Name"
"""

import getpass
import json
import os
import shutil
import socket
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from argparse import ArgumentParser

DEFAULT_PORT = 8088
CONFIG_DIR = os.path.join(os.path.expanduser("~"), ".config", "office-rdp")


# -- helpers -----------------------------------------------------------------

def office_binary():
    """Locate the Go agent that ships next to the launcher or on PATH."""
    for name in ("office-rdp", "office-rdp.exe"):
        found = shutil.which(name)
        if found:
            return found
    here = os.path.dirname(os.path.abspath(sys.executable))
    candidates = [
        os.path.join(here, "office-rdp"),
        os.path.join(here, "office-rdp.exe"),
        os.path.join(here, "tools", "office-rdp"),
        os.path.join(here, "tools", "office-rdp-arm64"),
        os.path.join(here, "bin", "office-rdp"),
        os.path.join(here, "bin", "office-rdp-linux"),
        os.path.join(here, "bin", "office-rdp-linux-arm64"),
        os.path.join(here, "bin", "office-rdp-linux-amd64"),
        os.path.join(here, "..", "dist", "windows", "office-rdp.exe"),
        os.path.join(here, "..", "dist", "macos", "bin", "office-rdp-arm64"),
    ]
    for path in candidates:
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return os.path.abspath(path)
    return None


def identity_paths():
    home = os.path.expanduser("~")
    return [
        os.path.join(home, ".config", "office-rdp", "identity.json"),
        os.path.join(home, "Library", "Application Support", "Office RDP",
                     "identity.json"),
    ]


def local_identity():
    """Return (peerId, alias, path) from the same identity file the agent uses."""
    for path in identity_paths():
        try:
            with open(path) as fh:
                data = json.load(fh)
        except (OSError, ValueError):
            continue
        peer = str(data.get("peerId") or "").strip()
        if peer:
            return peer, str(data.get("alias") or "").strip(), path
    return "", "", ""


def _run(argv):
    try:
        return subprocess.call(argv)
    except OSError as exc:
        print(f"error: cannot run {argv[0]}: {exc}", file=sys.stderr)
        return 1


def _password(args, prompt=False):
    if args.password:
        return args.password
    if args.password_file:
        try:
            with open(args.password_file) as fh:
                value = fh.read().strip()
            if value:
                return value
        except OSError as exc:
            print(f"error: cannot read password file: {exc}", file=sys.stderr)
    env = os.environ.get("RDPLINK_PASSWORD", "").strip()
    if env:
        return env
    if prompt and sys.stdin.isatty():
        try:
            return getpass.getpass("Access password: ")
        except (EOFError, KeyboardInterrupt):
            return ""
    return ""


def _request(url, password="", method="GET", body=None, timeout=15):
    data = None
    headers = {}
    if password:
        headers["X-Office-Password"] = password
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers,
                                     method=method)
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return response.status, response.headers, response.read()


def _normalise_target(target, rendezvous):
    """Turn a peer ID, host:port or URL into a base URL."""
    target = (target or "").strip()
    if target.startswith(("http://", "https://")):
        return target.rstrip("/")
    digits = "".join(ch for ch in target if ch.isdigit())
    if len(digits) == 9 and len(target.replace(" ", "")) == 9:
        if not rendezvous:
            raise ValueError("a 9-digit peer ID needs --rendezvous")
        url = rendezvous.rstrip("/") + "/resolve?peerId=" + digits
        _, _, raw = _request(url, timeout=10)
        info = json.loads(raw.decode() or "{}")
        addr = str(info.get("addr") or "").strip()
        if addr:
            host, _, port = addr.rpartition(":")
            try:
                with socket.create_connection((host or addr, int(port or 80)),
                                              timeout=0.8):
                    return "http://" + addr
            except (OSError, ValueError):
                pass
        relay = str(info.get("url") or "").strip()
        if relay:
            return relay.rstrip("/")
        if addr:
            return "http://" + addr
        raise ValueError("the rendezvous returned no address for this peer")
    return "http://" + target


def _print_json(value):
    print(json.dumps(value, indent=2, sort_keys=True))


# -- local commands ----------------------------------------------------------

def cmd_id(args):
    binary = office_binary()
    if binary:
        return _run([binary, "--id"])
    peer, alias, _ = local_identity()
    if not peer:
        print("error: no RDP Link identity yet; run --share once or install "
              "office-rdp", file=sys.stderr)
        return 1
    print(peer)
    if alias:
        print("Alias:", alias, file=sys.stderr)
    return 0


def cmd_status(args):
    if args.status:
        return cmd_remote_status(args, args.status)

    peer, alias, path = local_identity()
    binary = office_binary()
    info = {
        "peerId": peer,
        "alias": alias,
        "identity": path,
        "configDir": CONFIG_DIR,
        "officeBinary": binary or "",
        "port": args.port,
    }
    if binary:
        try:
            out = subprocess.run([binary, "--version"], capture_output=True,
                                 text=True, timeout=10)
            info["version"] = out.stdout.strip()
        except (OSError, subprocess.SubprocessError):
            pass
    if args.json:
        _print_json(info)
        return 0
    if not peer:
        print("Not initialised yet (run `rdp-link --share`).")
        return 1
    print(f"RDP Link ID : {peer}")
    if alias:
        print(f"Alias       : {alias}")
    print(f"Identity    : {path}")
    print(f"Port        : {info['port']}")
    print(f"Agent       : {info.get('officeBinary') or 'not found'}")
    if info.get("version"):
        print(f"Version     : {info['version']}")
    return 0


def cmd_remote_status(args, target):
    password = _password(args, prompt=True)
    base = _normalise_target(target, args.rendezvous)
    try:
        _, _, raw = _request(base + "/info", password, timeout=args.timeout)
    except urllib.error.HTTPError as exc:
        print(f"error: {base}/info -> {exc.code} {exc.reason}", file=sys.stderr)
        return 1
    except (urllib.error.URLError, OSError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    info = json.loads(raw.decode() or "{}")
    if args.json:
        _print_json(info)
        return 0
    print(f"Connected to: {base}")
    print(f"RDP Link ID : {info.get('peerId', '?')}")
    print(f"Alias       : {info.get('alias', '?')}")
    print(f"LAN         : {info.get('lanIp', '?')}:{info.get('port', '?')}")
    print(f"WAN         : {info.get('wanIp', '?')}:{info.get('port', '?')}")
    return 0


def cmd_share(args):
    binary = office_binary()
    if not binary:
        print("error: office-rdp not found; install the package or put the "
              "agent on PATH", file=sys.stderr)
        return 1
    argv = [binary, "--host", "--port", str(args.port),
            "--display", str(args.display)]
    if args.read_only:
        argv.append("--read-only")
    if args.test_pattern:
        argv.append("--test-pattern")
    if args.rendezvous:
        argv += ["--rendezvous", args.rendezvous]
    if args.relay_insecure:
        argv.append("--relay-insecure")
    if args.relay_ca:
        argv += ["--relay-ca", args.relay_ca]
    if args.password_file:
        argv += ["--password-file", args.password_file]

    if not args.password:
        print("Starting headless host:", " ".join(argv), file=sys.stderr)
        try:
            os.execv(binary, argv)
        except OSError as exc:
            print(f"error: cannot start host: {exc}", file=sys.stderr)
            return 1
        return 0

    # Keep the password off the process list: write it to a 0600 file, let the
    # agent read it at startup, and remove the file when the host exits.
    import tempfile
    handle, path = tempfile.mkstemp(prefix="rdplink-cli-", suffix=".pw")
    try:
        os.fchmod(handle, 0o600)
        os.write(handle, args.password.encode())
        os.close(handle)
        argv += ["--password-file", path]
        print("Starting headless host:", " ".join(argv), file=sys.stderr)
        process = subprocess.Popen(argv)
        return process.wait()
    except OSError as exc:
        print(f"error: cannot start host: {exc}", file=sys.stderr)
        return 1
    finally:
        try:
            os.unlink(path)
        except OSError:
            pass


def cmd_delegate(args, flag):
    binary = office_binary()
    if not binary:
        print("error: office-rdp not found", file=sys.stderr)
        return 1
    argv = [binary, flag]
    if flag == "--list-peers":
        if not args.rendezvous:
            print("error: --peers needs --rendezvous", file=sys.stderr)
            return 2
        argv.append(args.rendezvous)
    return _run(argv)


# -- guest commands ----------------------------------------------------------

def _looks_like_link_target(target):
    if "://" in target or ":" in target:
        return True
    digits = "".join(ch for ch in target if ch.isdigit())
    return len(digits) == 9 and len(target.replace(" ", "")) == 9


def cmd_connect(args, target):
    from .profiles import ProfileStore

    store = ProfileStore()
    profile = next((p for p in store.profiles
                    if p["name"].lower() == target.lower() or p["id"] == target),
                   None)
    if profile and not _looks_like_link_target(target):
        from .connection import HeadlessApp
        return HeadlessApp().connect_and_wait(profile)
    return cmd_link(args, target)


def cmd_link(args, target):
    password = _password(args, prompt=True)
    try:
        base = _normalise_target(target, args.rendezvous)
        _, _, raw = _request(base + "/info", password, timeout=args.timeout)
    except urllib.error.HTTPError as exc:
        hint = " (check the password)" if exc.code == 401 else ""
        print(f"error: cannot reach {target}: {exc.code} {exc.reason}{hint}",
              file=sys.stderr)
        return 1
    except (urllib.error.URLError, OSError, ValueError) as exc:
        print(f"error: cannot reach {target}: {exc}", file=sys.stderr)
        return 1

    info = json.loads(raw.decode() or "{}")
    if args.json:
        _print_json({"url": base, "info": info})
    else:
        print(f"Connected to {info.get('alias') or 'host'} "
              f"({info.get('peerId', '?')}) at {base}")

    actions = 0
    if args.probe or args.save_frame:
        actions += _fetch_frame(args, base, password)
    if args.clipboard_set is not None:
        actions += _clipboard_set(args, base, password)
    if args.clipboard_get:
        actions += _clipboard_get(args, base, password)
    if args.click:
        actions += _click(args, base, password)
    if args.type_text is not None:
        actions += _input(args, base, password,
                          {"type": "text", "text": args.type_text})
    if args.key:
        actions += _input(args, base, password,
                          {"type": "key", "key": args.key})
    if args.open:
        actions += _open_viewer(base, password)
    return 0 if actions >= 0 else 1


def _fetch_frame(args, base, password):
    data = b""
    ctype = ""
    for path in ("/frame.jpg", "/frame.png"):
        try:
            _, headers, data = _request(base + path, password,
                                        timeout=args.timeout)
            ctype = headers.get("Content-Type", "")
            if data:
                break
        except urllib.error.HTTPError as exc:
            if exc.code in (404, 415, 503):
                continue
            print(f"error: {path} -> {exc.code} {exc.reason}", file=sys.stderr)
            return -1
        except (urllib.error.URLError, OSError) as exc:
            print(f"error: {exc}", file=sys.stderr)
            return -1
    if not data:
        print("error: the host returned no frame (screen permission?)",
              file=sys.stderr)
        return -1

    size = ""
    try:
        from PIL import Image
        import io
        with Image.open(io.BytesIO(data)) as image:
            size = f" {image.width}x{image.height}"
    except Exception:
        pass
    saved = ""
    if args.save_frame:
        path = args.save_frame
        if not os.path.splitext(path)[1]:
            path += ".png" if "png" in ctype else ".jpg"
        with open(path, "wb") as fh:
            fh.write(data)
        saved = path
    if args.json:
        _print_json({"frame": {"bytes": len(data), "size": size.strip(),
                               "contentType": ctype, "saved": saved}})
    else:
        print(f"frame: {len(data)} bytes{size} {ctype}".rstrip())
        if saved:
            print(f"saved: {saved}")
    return 1


def _clipboard_get(args, base, password):
    try:
        _, _, raw = _request(base + "/clipboard", password, timeout=args.timeout)
    except (urllib.error.URLError, OSError) as exc:
        print(f"error: clipboard: {exc}", file=sys.stderr)
        return -1
    try:
        text = json.loads(raw.decode() or "{}").get("text", "")
    except ValueError:
        text = raw.decode(errors="replace")
    if args.json:
        _print_json({"clipboard": text})
    else:
        print(text)
    return 1


def _clipboard_set(args, base, password):
    return _input(args, base, password, {"type": "clipboard"},
                  path="/clipboard", body={"text": args.clipboard_set})


def _click(args, base, password):
    try:
        x_text, y_text = args.click.split(",", 1)
        event = {"type": "click", "x": float(x_text), "y": float(y_text),
                 "button": 1}
    except ValueError:
        print("error: --click expects X,Y", file=sys.stderr)
        return -1
    return _input(args, base, password, event)


def _input(args, base, password, event, path="/input", body=None):
    payload = body if body is not None else event
    try:
        _request(base + path, password, method="POST", body=payload,
                 timeout=args.timeout)
    except (urllib.error.URLError, OSError) as exc:
        print(f"error: {path}: {exc}", file=sys.stderr)
        return -1
    if args.json:
        key = "clipboard" if path == "/clipboard" else "input"
        _print_json({key: payload})
    return 1


def _open_viewer(base, password):
    """Launch the GUI viewer when the user explicitly asks for it."""
    try:
        from .viewer_launch import open_viewer
    except Exception as exc:  # pragma: no cover - packaging issue
        print(f"error: viewer unavailable: {exc}", file=sys.stderr)
        return -1
    used_browser, argv = open_viewer(base, password or None)
    print("viewer: launched" + (" (browser fallback)" if used_browser else ""))
    return 1


# -- CLI installation --------------------------------------------------------

def _launcher_argv():
    """The command that starts this launcher (frozen app or Python script)."""
    if getattr(sys, "frozen", False):
        return [sys.executable]
    script = os.path.abspath(
        os.path.join(os.path.dirname(__file__), "..", "ubuntu_rdp_manager.py"))
    return [sys.executable, script]


def _write_wrapper(path, argv):
    import shlex
    body = ("#!/bin/sh\n"
            "exec " + " ".join(shlex.quote(part) for part in argv) +
            ' "$@"\n')
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)
    os.chmod(path, 0o755)


def _cli_dirs():
    override = os.environ.get("RDPLINK_CLI_BIN")
    if override:
        return [override]
    if os.name == "nt":
        return []
    return ["/usr/local/bin",
            os.path.join(os.path.expanduser("~"), ".local", "bin")]


def _agent_paths():
    """Wrappers for the Go tools that sit next to the launcher."""
    here = os.path.dirname(os.path.abspath(sys.executable))
    found = {}
    for name in ("office-rdp", "office-rdp.exe"):
        for base in (here, os.path.join(here, "tools")):
            path = os.path.join(base, name)
            if os.path.isfile(path):
                found["office-rdp"] = path
                break
    rendezvous = os.path.join(here, "tools", "office-rdp-rendezvous-arm64")
    if os.path.isfile(rendezvous):
        found["office-rdp-rendezvous"] = rendezvous
    return found


def cmd_install_cli(args):
    if os.name == "nt":
        return _install_windows_cli(remove=False)
    launcher = _launcher_argv()
    agents = _agent_paths()
    installed = []
    for directory in _cli_dirs():
        try:
            os.makedirs(directory, exist_ok=True)
            _write_wrapper(os.path.join(directory, "rdp-link"), launcher)
            installed.append(os.path.join(directory, "rdp-link"))
            for name, path in agents.items():
                _write_wrapper(os.path.join(directory, name), [path])
                installed.append(os.path.join(directory, name))
            break
        except OSError:
            continue
    if not installed:
        print("error: cannot write a CLI wrapper (try sudo, or set "
              "RDPLINK_CLI_BIN to a writable directory)", file=sys.stderr)
        return 1
    for path in installed:
        print("installed:", path)
    if installed[0].startswith("/usr/local/bin"):
        print("Open a new terminal and run: rdp-link --help")
    else:
        print("Add this directory to PATH:", os.path.dirname(installed[0]))
    return 0


def _windows_user_path(install):
    import winreg
    target = os.path.dirname(os.path.abspath(sys.executable))
    key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, "Environment", 0,
                         winreg.KEY_READ | winreg.KEY_SET_VALUE)
    try:
        current, _ = winreg.QueryValueEx(key, "Path")
    except FileNotFoundError:
        current = ""
    parts = [p for p in current.split(";") if p]
    present = any(os.path.normcase(p) == os.path.normcase(target) for p in parts)
    if install and not present:
        parts.append(target)
    if not install:
        parts = [p for p in parts
                 if os.path.normcase(p) != os.path.normcase(target)]
    winreg.SetValueEx(key, "Path", 0, winreg.REG_EXPAND_SZ, ";".join(parts))
    winreg.CloseKey(key)
    try:
        import ctypes
        ctypes.windll.user32.SendMessageTimeoutW(
            0xFFFF, 0x001A, 0, ctypes.c_wchar_p("Environment"), 0x0002, 5000,
            None)
    except Exception:
        pass
    return target


def _install_windows_cli(remove):
    if not getattr(sys, "frozen", False):
        print("error: --install-cli must be run from the installed "
              "rdp-link.exe", file=sys.stderr)
        return 1
    target = _windows_user_path(install=not remove)
    action = "removed from" if remove else "added to"
    print(f"{target} {action} the user PATH")
    print("Open a new terminal for the change to take effect.")
    return 0


def cmd_uninstall_cli(args):
    if os.name == "nt":
        return _install_windows_cli(remove=True)
    removed = 0
    for directory in _cli_dirs():
        for name in ("rdp-link", "office-rdp", "office-rdp-rendezvous"):
            path = os.path.join(directory, name)
            try:
                os.unlink(path)
                print("removed:", path)
                removed += 1
            except OSError:
                pass
    if not removed:
        print("nothing to remove")
    return 0


# -- legacy profile commands -------------------------------------------------

def cmd_list(args):
    from .profiles import ProfileStore
    from .connection import ConnectionController
    store = ProfileStore()
    ctl = ConnectionController(None)
    for profile in store.profiles:
        mark = "*" if ctl.is_active(profile["id"]) else " "
        print(f"{mark} {profile['name']:<24} "
              f"{profile['username']}@{profile['host']}:{profile['port']}")
    return 0


def cmd_connect_profile(args, name):
    from .profiles import ProfileStore
    from .connection import HeadlessApp
    store = ProfileStore()
    profile = next((p for p in store.profiles
                    if p["name"].lower() == name.lower() or p["id"] == name),
                   None)
    if not profile:
        names = ", ".join(p["name"] for p in store.profiles) or "(none)"
        print(f"error: no profile named {name!r}. Profiles: {names}",
              file=sys.stderr)
        return 1
    return HeadlessApp().connect_and_wait(profile)


# -- parser ------------------------------------------------------------------

def build_parser():
    parser = ArgumentParser(
        prog="rdp-link",
        description="RDP Link headless CLI — share, connect and inspect from "
                    "a terminal without a GUI.")
    parser.add_argument("--version", "-V", action="store_true",
                        help="print the version and exit")
    parser.add_argument("--id", action="store_true",
                        help="print this device's 9-digit RDP Link ID")
    parser.add_argument("--install-cli", action="store_true",
                        help="install the rdp-link command on PATH")
    parser.add_argument("--uninstall-cli", action="store_true",
                        help="remove the installed rdp-link command")
    parser.add_argument("--status", nargs="?", const="", metavar="TARGET",
                        help="show local status, or the status of TARGET")
    parser.add_argument("--share", action="store_true",
                        help="run the screen-sharing host agent (headless)")
    parser.add_argument("--connect", metavar="TARGET",
                        help="connect to a peer ID, host:port or URL")
    parser.add_argument("--connect-profile", metavar="NAME",
                        help="connect to a legacy FreeRDP profile")
    parser.add_argument("--list", action="store_true",
                        help="list legacy FreeRDP profiles")
    parser.add_argument("--doctor", action="store_true",
                        help="run host capture/input diagnostics")
    parser.add_argument("--displays", action="store_true",
                        help="list capturable displays")
    parser.add_argument("--peers", action="store_true",
                        help="list rendezvous peers (needs --rendezvous)")
    parser.add_argument("--probe", action="store_true",
                        help="fetch one frame and print its size")
    parser.add_argument("--save-frame", metavar="PATH",
                        help="save one frame to PATH")
    parser.add_argument("--clipboard-get", action="store_true",
                        help="print the host clipboard")
    parser.add_argument("--clipboard-set", metavar="TEXT",
                        help="write TEXT to the host clipboard")
    parser.add_argument("--click", metavar="X,Y",
                        help="click at host screen coordinates")
    parser.add_argument("--type", dest="type_text", metavar="TEXT",
                        help="type TEXT on the host")
    parser.add_argument("--key", metavar="KEY",
                        help="press a key or special key (enter, tab, …)")
    parser.add_argument("--open", action="store_true",
                        help="also open the GUI viewer")
    parser.add_argument("--json", action="store_true",
                        help="print machine-readable JSON")
    parser.add_argument("--password", metavar="PW",
                        help="access password (prefer --password-file or "
                             "RDPLINK_PASSWORD)")
    parser.add_argument("--password-file", metavar="FILE",
                        help="read the access password from FILE")
    parser.add_argument("--rendezvous", metavar="URL",
                        help="rendezvous server for 9-digit peer IDs")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT,
                        help=f"host port for --share (default {DEFAULT_PORT})")
    parser.add_argument("--display", type=int, default=0,
                        help="display index to capture (default 0)")
    parser.add_argument("--read-only", action="store_true",
                        help="start the host in view-only mode")
    parser.add_argument("--test-pattern", action="store_true",
                        help="stream a synthetic pattern instead of the screen")
    parser.add_argument("--relay-insecure", action="store_true",
                        help="skip relay TLS verification (testing only)")
    parser.add_argument("--relay-ca", metavar="FILE",
                        help="custom CA bundle for the relay")
    parser.add_argument("--timeout", type=float, default=15,
                        help="HTTP timeout in seconds (default 15)")
    return parser


def main(argv=None):
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.version:
        from . import __version__
        print(f"RDP Link {__version__}")
        return 0
    if args.id:
        return cmd_id(args)
    if args.install_cli:
        return cmd_install_cli(args)
    if args.uninstall_cli:
        return cmd_uninstall_cli(args)
    if args.share:
        return cmd_share(args)
    if args.doctor:
        return cmd_delegate(args, "--doctor")
    if args.displays:
        return cmd_delegate(args, "--displays")
    if args.peers:
        return cmd_delegate(args, "--list-peers")
    if args.list:
        return cmd_list(args)
    if args.connect_profile:
        return cmd_connect_profile(args, args.connect_profile)
    if args.status is not None:
        return cmd_status(args)
    if args.connect:
        return cmd_connect(args, args.connect)

    remote = (args.probe or args.save_frame or args.clipboard_get
              or args.clipboard_set is not None or args.click
              or args.type_text is not None or args.key)
    if remote:
        print("error: add --connect TARGET to use remote options",
              file=sys.stderr)
        return 2
    parser.print_help()
    return 0
