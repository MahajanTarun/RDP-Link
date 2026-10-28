"""Ubuntu RDP → RDP Link — lightweight RDP launcher with device redirection.

Design principles:
- stdlib-only core (tkinter GUI, no third-party runtime deps)
- the connect path never blocks on probes; bridges launch in parallel
- FreeRDP is the engine; this app is a thin, fast launcher around it
"""

__version__ = "4.2.0"
APP_NAME = "RDP Link"
BUNDLE_ID = "com.eminentailabs.rdplink"

# Keychain/secret service name. Deliberately unchanged from the app's
# "Ubuntu RDP Manager" days so existing users' saved passwords keep working
# after the rename. Invisible to users; see docs/SECURITY.md.
SECRET_SERVICE = "com.eminentailabs.UbuntuRDPManager"
