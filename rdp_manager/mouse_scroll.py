"""Compatibility alias for :mod:`rdp_manager.scroll`."""

from . import scroll as _scroll


def __getattr__(name):
    return getattr(_scroll, name)


def __dir__():
    return dir(_scroll)
