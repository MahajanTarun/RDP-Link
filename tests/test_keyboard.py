"""Keyboard layout detection + mapping + args tests."""

from unittest import mock

from rdp_manager import keyboard
from rdp_manager.connection import build_args

BASE = {"id": "t", "name": "T", "host": "1.2.3.4", "port": "3389",
        "username": "u", "smartSizing": True, "audio": "remote",
        "mic": "remote", "camera": "remote", "camDevice": "0",
        "clipboard": False, "fileTransfer": False, "certTofu": False,
        "keyboard": "auto"}

MAC_SAMPLE = """(
        {
        "Bundle ID" = "com.apple.PressAndHold";
        InputSourceKind = "Non Keyboard Input Method";
    },
        {
        InputSourceKind = "Keyboard Layout";
        "KeyboardLayout ID" = "-3";
        "KeyboardLayout Name" = "ABC-India";
    }
)"""

XKB_SAMPLE = """rules:      evdev
model:      pc105
layout:     us,de
options:    compose:ralt
"""


def test_map_macos_name_sample():
    code = keyboard._map_macos_name("ABC-India")
    assert code == "0x409"


def test_map_specific_before_generic():
    assert keyboard._map_macos_name("Canadian French - CSA") == "0xC0C"
    assert keyboard._map_macos_name("French") == "0x40C"
    assert keyboard._map_macos_name("German") == "0x407"
    assert keyboard._map_macos_name("British") == "0x809"
    assert keyboard._map_macos_name("Dvorak") == "0x10409"


def test_map_unknown_returns_none():
    assert keyboard._map_macos_name("Nürlkhyper") is None


def test_parse_mac_defaults_sample():
    with mock.patch.object(keyboard, "_run_util", return_value=(0, MAC_SAMPLE)):
        name = keyboard._mac_layout_name()
    assert name == "ABC-India"


def test_parse_xkb_sample():
    with mock.patch.object(keyboard, "_run_util", return_value=(0, XKB_SAMPLE)):
        assert keyboard._xkb_layout() == "us"


def test_detection_cached_and_skips_reruns():
    keyboard._cache.update(code=None, source=None, done=False)
    calls = {"mac": 0}
    with mock.patch.object(keyboard, "_mac_layout_name",
                           side_effect=lambda: calls.__setitem__(
                               "mac", calls["mac"] + 1) or "U.S."):
        code, source = keyboard.detect_client_layout()
        assert code == "0x409" and "U.S." in source
        code2, _ = keyboard.detect_client_layout()     # cached, no second call
        assert code2 == "0x409"
        assert calls["mac"] == 1
    keyboard._cache.update(code=None, source=None, done=False)


def test_resolve_fixed_value_verbatim():
    assert keyboard.resolve_for_connect({**BASE, "keyboard": "0x807"}) == "0x807"
    assert keyboard.resolve_for_connect({**BASE, "keyboard": "garbage"}) is None


def test_resolve_auto_unknown_layout_gives_none():
    keyboard._cache.update(code=None, source=None, done=True)
    assert keyboard.resolve_for_connect(dict(BASE)) is None
    keyboard._cache.update(code=None, source=None, done=False)


def test_args_include_kbd_flag():
    a = build_args(BASE, kbd_layout="0x409")
    assert a[2] == "/kbd:layout:0x409"


def test_kbd_syntax_freerdp2_vs_freerdp3():
    """FreeRDP 2.x uses the legacy /kbd:0x00000409 syntax (Linux guests)."""
    a3 = build_args(BASE, kbd_layout="0x409", freerdp_major_v=3)
    assert "/kbd:layout:0x409" in a3
    a2 = build_args(BASE, kbd_layout="0x409", freerdp_major_v=2)
    assert "/kbd:0x00000409" in a2
    a2b = build_args(BASE, kbd_layout="0x10409", freerdp_major_v=2)
    assert "/kbd:0x00010409" in a2b


def test_args_omit_kbd_when_unresolved():
    assert not any(x.startswith("/kbd") for x in build_args(BASE))


def test_profile_defaults_to_auto():
    from rdp_manager.profiles import normalize
    assert normalize({})["keyboard"] == "auto"
    assert normalize({"keyboard": "0x409"})["keyboard"] == "0x409"
    assert normalize({"keyboard": "Nonsense"})["keyboard"] == "auto"


def test_choices_have_deduped_unique_hex_ids():
    vals = [v for v, _ in keyboard.choices()]
    assert len(vals) == len(set(vals))
    assert all(v.startswith("0x") for v in vals)
