"""Mouse / trackpad scrolling: make the remote session follow the scroll
direction of the pointing device currently in use.

FreeRDP's SDL clients normally re-invert wheel events that the OS marks as
"natural" scrolling.  That is why a Mac trackpad scrolls backwards in a
remote session after the same gesture works locally.  FreeRDP exposes a
per-user SDL preference for this:

    {"UseLocalMouseScrollDirection": true}

We detect the client's natural-scrolling setting once per session (macOS:
``com.apple.swipescrolldirection``; Linux: GNOME's mouse/touchpad
``natural-scroll`` keys) in a background thread, cache it, and merge the
FreeRDP preference into ``sdl-freerdp.json`` just before launch.  Unknown
settings leave the user's FreeRDP configuration untouched; the server/client
default then applies.
"""

import json
import os
import subprocess
import sys
import threading

# Values stored in a profile's "mouseScroll" key.
SCROLL_AUTO = "auto"
SCROLL_NATURAL = "natural"          # trackpad-style: content follows fingers
SCROLL_TRADITIONAL = "traditional"  # mouse-wheel-style: wheel up = page up
SCROLL_VALUES = (SCROLL_NATURAL, SCROLL_TRADITIONAL)

# Profile keys accepted for compatibility with older/hand-written profiles.
_PROFILE_KEYS = ("mouseScroll", "scroll", "scrollDirection", "naturalScroll")

_cache = {"natural": None, "source": None, "done": False}
_lock = threading.Lock()
_write_lock = threading.Lock()

_SDL_PREFERENCE = "UseLocalMouseScrollDirection"


def _run_util(cmd, timeout=5):
    """Run a small detection utility; returns (rc, stdout) or (-1, '')."""
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    except (OSError, subprocess.SubprocessError):
        return -1, ""
    return (r.returncode, r.stdout or "")


def _parse_bool(text):
    """Parse the many spellings used by defaults/gsettings/KDE config."""
    value = (text or "").strip().lower()
    if value in ("1", "true", "yes", "on", "natural"):
        return True
    if value in ("0", "false", "no", "off", "traditional", "normal"):
        return False
    return None


def _mac_natural_scroll():
    """True when macOS 'Natural scrolling' is enabled, else False/None."""
    rc, out = _run_util(["defaults", "read", "NSGlobalDomain",
                         "com.apple.swipescrolldirection"])
    if rc != 0:
        # Older macOS releases accept the -g shorthand.
        rc, out = _run_util(["defaults", "read", "-g",
                             "com.apple.swipescrolldirection"])
    return _parse_bool(out) if rc == 0 else None


def _gsettings_bool(schema, key):
    rc, out = _run_util(["gsettings", "get", schema, key])
    if rc != 0:
        return None
    return _parse_bool(out)


def _kde_natural_scroll():
    """KDE Plasma stores input settings in kcminputrc (try 6 then 5)."""
    for tool in ("kreadconfig6", "kreadconfig5"):
        rc, out = _run_util([tool, "--file", "kcminputrc",
                             "--group", "Touchpad",
                             "--key", "NaturalScroll"])
        if rc == 0:
            return _parse_bool(out)
    return None


def _linux_scroll_settings():
    """Per-device natural-scroll values known to the desktop.

    GNOME deliberately keeps separate settings for a trackpad and a mouse;
    returning both lets the caller combine them for whichever device is in
    use.
    """
    settings = {}
    touchpad = _gsettings_bool("org.gnome.desktop.peripherals.touchpad",
                               "natural-scroll")
    if touchpad is not None:
        settings["touchpad"] = touchpad
    mouse = _gsettings_bool("org.gnome.desktop.peripherals.mouse",
                            "natural-scroll")
    if mouse is not None:
        settings["mouse"] = mouse
    if not settings:
        kde = _kde_natural_scroll()
        if kde is not None:
            settings["touchpad"] = kde
    return settings


def _linux_natural_scroll(settings=None):
    """Natural-scroll value for the pointing devices in use.

    GNOME has separate mouse and touchpad settings.  ``True`` means at least
    one device uses natural scrolling: FreeRDP then keeps each device's own
    direction (a non-flipped wheel event is untouched, a flipped trackpad
    event is not re-inverted), so mixing a mouse and a trackpad still does
    the right thing.  A profile can override Auto with a fixed direction.
    """
    if settings is None:
        settings = _linux_scroll_settings()
    if not settings:
        return None
    return any(settings.values())


def _cached_natural():
    """Read the cached value, tolerating the keyboard-style 'code' key."""
    value = _cache.get("natural")
    if value is None:
        value = _cache.get("code")
    if value is None:
        value = _cache.get("value")
    return value


def detect_client_scroll(force=False):
    """Detect once per session; returns (natural|None, source|None)."""
    with _lock:
        if _cache["done"] and not force:
            return _cached_natural(), _cache["source"]
        if sys.platform == "darwin":
            natural = _mac_natural_scroll()
            if natural is None:
                source = "macOS natural scrolling unknown"
            else:
                source = ("macOS natural scrolling on" if natural
                          else "macOS natural scrolling off")
        elif sys.platform.startswith("linux"):
            settings = _linux_scroll_settings()
            natural = _linux_natural_scroll(settings)
            if settings:
                detail = ", ".join(
                    f"{name}={'natural' if value else 'traditional'}"
                    for name, value in sorted(settings.items()))
                source = f"Linux scroll settings ({detail})"
            else:
                source = "Linux scroll settings unknown"
        else:
            natural, source = None, "scroll direction unsupported on this OS"
        # Keep the legacy "code" spelling alongside "natural" so callers
        # written against the keyboard module can share the cache contract.
        _cache.update(natural=natural, code=natural, value=natural,
                      source=source, done=True)
        return natural, source


def _choice_from_profile(profile):
    """Profile value normalised to auto/natural/traditional."""
    for key in _PROFILE_KEYS:
        if key not in profile:
            continue
        value = profile.get(key)
        if value is None:
            continue
        if isinstance(value, bool):
            return SCROLL_NATURAL if value else SCROLL_TRADITIONAL
        text = str(value).strip().lower()
        if text in ("", SCROLL_AUTO):
            return SCROLL_AUTO
        if text in ("natural", "true", "yes", "on", "1", "local"):
            return SCROLL_NATURAL
        if text in ("traditional", "normal", "false", "no", "off", "0",
                    "inverted", "reverse"):
            return SCROLL_TRADITIONAL
        return SCROLL_AUTO
    return SCROLL_AUTO


def resolve_for_connect(profile, blocking=True):
    """FreeRDP UseLocalMouseScrollDirection value for a profile.

    profile['mouseScroll'] == 'auto'      -> detected local direction
    'natural' / 'traditional'             -> explicit override
    Returns True (use local/natural direction), False (traditional), or
    None when Auto detection could not determine the setting.
    """
    want = _choice_from_profile(profile)
    if want == SCROLL_AUTO:
        if blocking:
            natural, _ = detect_client_scroll()
        else:
            with _lock:
                natural = _cached_natural() if _cache["done"] else None
        return natural
    return want == SCROLL_NATURAL


def _default_config_path():
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "freerdp", "sdl-freerdp.json")


_IMPORT_CONFIG_PATH = _default_config_path()
CONFIG_PATH = _IMPORT_CONFIG_PATH


def freerdp_config_path():
    """Path FreeRDP uses for its SDL user configuration."""
    # Honour an explicit override (tests/embedders) while following a late
    # XDG_CONFIG_HOME change when the constant was not modified.
    if CONFIG_PATH != _IMPORT_CONFIG_PATH:
        return CONFIG_PATH
    return _default_config_path()


def read_scroll_preference(path=None):
    """Current UseLocalMouseScrollDirection value, or None if unavailable."""
    path = path or freerdp_config_path()
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError):
        return None
    if not isinstance(data, dict):
        return None
    value = data.get(_SDL_PREFERENCE)
    return value if isinstance(value, bool) else None


def apply_scroll_preference(natural, path=None):
    """Merge the SDL preference into FreeRDP's user config.

    Returns True when the config now reflects ``natural`` (and False when
    the value is unknown or the existing file cannot safely be updated).
    Other FreeRDP settings in the file are preserved.
    """
    if not isinstance(natural, bool):
        return False
    path = path or freerdp_config_path()
    with _write_lock:
        try:
            with open(path, "r", encoding="utf-8") as f:
                data = json.load(f)
        except FileNotFoundError:
            data = {}
        except (OSError, ValueError):
            # Never destroy a config we do not understand.
            return False
        if not isinstance(data, dict):
            return False
        if data.get(_SDL_PREFERENCE) is natural:
            return True
        data[_SDL_PREFERENCE] = natural
        parent = os.path.dirname(path)
        tmp = f"{path}.rdplink.tmp"
        try:
            if parent:
                os.makedirs(parent, exist_ok=True)
            with open(tmp, "w", encoding="utf-8") as f:
                json.dump(data, f, indent=4)
                f.write("\n")
            os.replace(tmp, path)
        except OSError:
            try:
                os.unlink(tmp)
            except OSError:
                pass
            return False
        return True


def apply_for_connect(profile, blocking=True, path=None):
    """Resolve the profile's scroll direction and apply it to FreeRDP."""
    natural = resolve_for_connect(profile, blocking=blocking)
    return apply_scroll_preference(natural, path=path)


def warmup():
    """Kick off scroll-direction detection in the background at app start."""
    def work():
        try:
            detect_client_scroll()
        except Exception:
            pass
    threading.Thread(target=work, daemon=True).start()


def reset_cache():
    """Forget the cached value (tests and sign-out/device changes)."""
    with _lock:
        _cache.update(natural=None, code=None, value=None,
                      source=None, done=False)


def choices():
    """[(value, label)] for the editor combobox (Auto is added by the UI)."""
    return [
        (SCROLL_NATURAL, "Natural (trackpad-style)"),
        (SCROLL_TRADITIONAL, "Traditional (mouse-wheel-style)"),
    ]


# Descriptive aliases used by integrations/documentation.
detect_client_scroll_direction = detect_client_scroll
detect_client_natural_scroll = detect_client_scroll
detect_scroll = detect_client_scroll
resolve_scroll_for_connect = resolve_for_connect
write_scroll_preference = apply_scroll_preference
apply_scroll_setting = apply_scroll_preference
_mac_scroll = _mac_natural_scroll
_linux_scroll = _linux_natural_scroll


def detect_client_scroll_choice(force=False):
    """Like detect_client_scroll() but returns 'natural'/'traditional'."""
    natural, source = detect_client_scroll(force=force)
    if natural is None:
        return None, source
    return (SCROLL_NATURAL if natural else SCROLL_TRADITIONAL), source
