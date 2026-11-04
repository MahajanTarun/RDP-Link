#!/usr/bin/env python3
"""RDP Link — entry point.

GUI mode:      python3 ubuntu_rdp_manager.py
Headless CLI:  python3 ubuntu_rdp_manager.py --id
               python3 ubuntu_rdp_manager.py --status
               python3 ubuntu_rdp_manager.py --share --port 8088
               python3 ubuntu_rdp_manager.py --connect 192.168.1.20:8088
               python3 ubuntu_rdp_manager.py --connect 123456789 \
                   --rendezvous https://relay.example.com --probe
               python3 ubuntu_rdp_manager.py --list
               python3 ubuntu_rdp_manager.py --connect-profile "Profile Name"

The CLI never imports tkinter, so it works over SSH and in containers/CI.
The legacy --connect-profile path talks to FreeRDP; --connect accepts a
profile name, an RDP Link peer ID, a host:port or an http(s) URL.
"""

import os
import sys

CLI_FLAGS = {
    "--id", "--status", "--share", "--connect", "--connect-profile", "--list",
    "--doctor", "--displays", "--peers", "--probe", "--save-frame",
    "--clipboard-get", "--clipboard-set", "--click", "--type", "--key",
    "--open", "--json", "--password", "--password-file", "--rendezvous",
    "--port", "--display", "--read-only", "--test-pattern", "--relay-insecure",
    "--relay-ca", "--timeout", "--version", "-V", "--help", "-h",
}


def _is_cli(argv):
    if not argv:
        return False
    if argv[0].startswith("-"):
        return True
    return any(arg in CLI_FLAGS for arg in argv)


def main():
    argv = sys.argv[1:]
    # Viewer mode: the frozen app re-enters itself so the guest window ships
    # inside the same bundle on every platform.
    if "--viewer" in argv:
        rest = [a for a in argv if a != "--viewer"]
        sys.argv = [sys.argv[0]] + rest
        try:
            from office_viewer import main as viewer_main
        except ImportError:
            import importlib.util
            path = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "office_viewer.py")
            spec = importlib.util.spec_from_file_location("office_viewer", path)
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            viewer_main = module.main
        viewer_main()
        return
    # Device watchdog mode: never load the GUI, spawn & supervise one child.
    if "--bridge-watchdog" in argv:
        from rdp_manager import watchdog
        i = argv.index("--bridge-watchdog")
        try:
            parent = int(argv[i + 1])
        except (IndexError, ValueError):
            parent = os.getppid()
        sep = i + 2 if (i + 2 < len(argv) and argv[i + 2] == "--") else None
        child = argv[sep + 1:] if sep else []
        sys.exit(watchdog.run(parent, child))
    if _is_cli(argv):
        from rdp_manager.cli import main as cli_main
        sys.exit(cli_main(argv))
    from rdp_manager.ui.app import UbuntuRDPApp
    UbuntuRDPApp().run()


if __name__ == "__main__":
    main()

