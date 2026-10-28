#!/usr/bin/env python3
"""Ubuntu RDP Manager — entry point.

GUI mode:      python3 ubuntu_rdp_manager.py
CLI mode:      python3 ubuntu_rdp_manager.py --list
               python3 ubuntu_rdp_manager.py --connect "Profile Name"

CLI mode avoids loading the GUI entirely and connects immediately —
behaves like launching raw sdl-freerdp yourself.
"""

import sys

from rdp_manager import __version__


def _cli(argv):
    """Handle --list / --connect / --version without importing tkinter."""
    if "--version" in argv or "-V" in argv:
        print(f"Ubuntu RDP Manager {__version__}")
        return 0
    from rdp_manager.profiles import ProfileStore
    store = ProfileStore()

    if "--list" in argv:
        from rdp_manager.connection import ConnectionController
        ctl = ConnectionController(None)
        for p in store.profiles:
            mark = "*" if ctl.is_active(p["id"]) else " "
            print(f"{mark} {p['name']:<24} {p['username']}@{p['host']}:{p['port']}")
        return 0

    if "--connect" in argv:
        i = argv.index("--connect")
        try:
            wanted = argv[i + 1]
        except IndexError:
            print("error: --connect needs a profile name", file=sys.stderr)
            return 2
        profile = next((p for p in store.profiles
                        if p["name"].lower() == wanted.lower()
                        or p["id"] == wanted), None)
        if not profile:
            names = ", ".join(p["name"] for p in store.profiles) or "(none)"
            print(f"error: no profile named {wanted!r}. Profiles: {names}",
                  file=sys.stderr)
            return 1
        # Headless connect: bridges + status on stderr, no GUI.
        from rdp_manager.connection import HeadlessApp
        return HeadlessApp().connect_and_wait(profile)

    print(__doc__)
    return 0


def main():
    argv = sys.argv[1:]
    if any(a in ("--list", "--connect", "--version", "-V", "--help", "-h")
           for a in argv):
        if "--help" in argv or "-h" in argv:
            print(__doc__)
            return
        sys.exit(_cli(argv))
    from rdp_manager.ui.app import UbuntuRDPApp
    UbuntuRDPApp().run()


if __name__ == "__main__":
    main()
