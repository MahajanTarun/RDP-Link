"""Mouse/trackpad scroll-direction detection, mapping and config tests."""

import json
from unittest import mock

import pytest

from rdp_manager import scroll
from rdp_manager.profiles import normalize

BASE = {"id": "t", "name": "T", "host": "1.2.3.4", "port": "3389",
        "username": "u", "smartSizing": True, "audio": "remote",
        "mic": "remote", "camera": "remote", "camDevice": "0",
        "clipboard": False, "fileTransfer": False, "certTofu": False,
        "mouseScroll": "auto"}


@pytest.fixture(autouse=True)
def _fresh_cache():
    scroll.reset_cache()
    yield
    scroll.reset_cache()


def test_parse_bool_spellings():
    for text in ("1", "true", "YES", "on", "natural\n"):
        assert scroll._parse_bool(text) is True
    for text in ("0", "false", "NO", "off", "traditional", "normal\n"):
        assert scroll._parse_bool(text) is False
    assert scroll._parse_bool("") is None
    assert scroll._parse_bool("what") is None


def test_mac_natural_scroll_reading():
    with mock.patch.object(scroll, "_run_util", return_value=(0, "1\n")):
        assert scroll._mac_natural_scroll() is True
    with mock.patch.object(scroll, "_run_util", return_value=(0, "0\n")):
        assert scroll._mac_natural_scroll() is False
    with mock.patch.object(scroll, "_run_util", return_value=(1, "")):
        assert scroll._mac_natural_scroll() is None


def test_linux_reads_touchpad_and_mouse():
    def fake(cmd, timeout=5):
        joined = " ".join(cmd)
        if "touchpad" in joined:
            return 0, "true\n"
        if "mouse" in joined:
            return 0, "false\n"
        return 1, ""

    with mock.patch.object(scroll, "_run_util", side_effect=fake):
        settings = scroll._linux_scroll_settings()
    assert settings == {"touchpad": True, "mouse": False}
    assert scroll._linux_natural_scroll(settings) is True


def test_linux_falls_back_to_mouse():
    def fake(cmd, timeout=5):
        joined = " ".join(cmd)
        if "touchpad" in joined:
            return 1, ""
        if "mouse" in joined:
            return 0, "false\n"
        return 1, ""

    with mock.patch.object(scroll, "_run_util", side_effect=fake):
        settings = scroll._linux_scroll_settings()
    assert settings == {"mouse": False}
    assert scroll._linux_natural_scroll(settings) is False


def test_detection_cached_and_skips_reruns():
    calls = {"mac": 0}

    def fake():
        calls["mac"] += 1
        return True

    with mock.patch.object(scroll.sys, "platform", "darwin"), \
            mock.patch.object(scroll, "_mac_natural_scroll", side_effect=fake):
        natural, source = scroll.detect_client_scroll()
        assert natural is True and "macOS" in source
        natural2, _ = scroll.detect_client_scroll()
        assert natural2 is True
        assert calls["mac"] == 1


def test_resolve_fixed_values():
    scroll._cache.update(natural=False, source="test", done=True)
    assert scroll.resolve_for_connect({**BASE, "mouseScroll": "natural"}) is True
    assert scroll.resolve_for_connect(
        {**BASE, "mouseScroll": "traditional"}) is False
    # Unknown values fall back to Auto (here the cached detection).
    assert scroll.resolve_for_connect(
        {**BASE, "mouseScroll": "garbage"}) is False


def test_resolve_auto_unknown_gives_none():
    scroll._cache.update(natural=None, source=None, done=True)
    assert scroll.resolve_for_connect(dict(BASE)) is None


def test_resolve_auto_uses_cached_detection():
    scroll._cache.update(natural=True, source="test", done=True)
    assert scroll.resolve_for_connect(dict(BASE)) is True


def test_apply_writes_and_preserves_other_settings(tmp_path):
    path = tmp_path / "freerdp" / "sdl-freerdp.json"
    path.parent.mkdir()
    path.write_text(json.dumps({"SDL_Grab": "SDL_SCANCODE_G"}))
    assert scroll.apply_scroll_preference(True, path=str(path)) is True
    data = json.loads(path.read_text())
    assert data["UseLocalMouseScrollDirection"] is True
    assert data["SDL_Grab"] == "SDL_SCANCODE_G"
    assert scroll.read_scroll_preference(str(path)) is True
    # idempotent
    assert scroll.apply_scroll_preference(True, path=str(path)) is True


def test_apply_never_clobbers_invalid_json(tmp_path):
    path = tmp_path / "sdl-freerdp.json"
    path.write_text("{not json")
    assert scroll.apply_scroll_preference(True, path=str(path)) is False
    assert path.read_text() == "{not json"


def test_apply_unknown_leaves_file_alone(tmp_path):
    path = tmp_path / "sdl-freerdp.json"
    assert scroll.apply_scroll_preference(None, path=str(path)) is False
    assert not path.exists()


def test_apply_for_connect_uses_profile(tmp_path):
    path = tmp_path / "sdl-freerdp.json"
    assert scroll.apply_for_connect(
        {**BASE, "mouseScroll": "natural"}, path=str(path)) is True
    assert scroll.read_scroll_preference(str(path)) is True
    assert scroll.apply_for_connect(
        {**BASE, "mouseScroll": "traditional"}, path=str(path)) is True
    assert scroll.read_scroll_preference(str(path)) is False


def test_profile_defaults_to_auto_and_validates():
    n = normalize({})
    assert n["mouseScroll"] == "auto"
    assert n["scroll"] == "auto"
    assert normalize({"mouseScroll": "natural"})["mouseScroll"] == "natural"
    assert normalize({"mouseScroll": "natural"})["scroll"] == "natural"
    assert normalize({"scroll": "traditional"})["mouseScroll"] == "traditional"
    assert normalize({"mouseScroll": "Nonsense"})["mouseScroll"] == "auto"
    assert normalize({"mouseScroll": True})["mouseScroll"] == "natural"


def test_choices_are_unique():
    vals = [v for v, _ in scroll.choices()]
    assert vals == ["natural", "traditional"]
    assert len(vals) == len(set(vals))
