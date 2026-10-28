"""Session logging: one log file per app run, trimmed to the last 10.

Bridges and FreeRDP stderr are piped here so any failure is diagnosable
from the "Show session log" action.
"""

import os
import time

from .paths import LOG_DIR, ensure_dirs, open_path_in_gui

_CURRENT = None


def current_log_path():
    global _CURRENT
    if _CURRENT is None:
        ensure_dirs()
        _CURRENT = os.path.join(LOG_DIR, "session-%s.log" % time.strftime("%Y%m%d-%H%M%S"))
    return _CURRENT


def open_log():
    path = current_log_path()
    if os.path.exists(path):
        open_path_in_gui(path)


def cleanup(keep=10):
    try:
        logs = sorted(f for f in os.listdir(LOG_DIR) if f.startswith("session-"))
        for old in logs[:-keep]:
            os.unlink(os.path.join(LOG_DIR, old))
    except OSError:
        pass
