"""Ubuntu RDP Manager — lightweight RDP launcher with device redirection.

Design principles:
- stdlib-only core (tkinter GUI, no third-party runtime deps)
- the connect path never blocks on probes; bridges launch in parallel
- FreeRDP is the engine; this app is a thin, fast launcher around it
"""

__version__ = "4.0.0"
APP_NAME = "Ubuntu RDP Manager"
BUNDLE_ID = "com.eminentailabs.UbuntuRDPManager"
