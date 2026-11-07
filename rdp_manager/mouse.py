"""Compatibility alias for :mod:`rdp_manager.scroll`.

The feature is about mouse/trackpad scrolling, so some callers naturally
import ``rdp_manager.mouse``.  Delegate every attribute to the canonical
implementation to keep one cache and one behaviour.
"""

from . import scroll as _scroll

__all__ = []


def __getattr__(name):
    return getattr(_scroll, name)


def __dir__():
    return dir(_scroll)
