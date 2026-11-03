"""Permission cache fast-path tests (no device opened)."""

import pytest

from rdp_manager import paths, permissions


@pytest.fixture(autouse=True)
def tmp_support(monkeypatch, tmp_path):
    # point SUPPORT_DIR at a scratch dir so we never touch real state
    monkeypatch.setattr(paths, "SUPPORT_DIR", str(tmp_path))
    permissions.reset_permission_cache()
    yield
    permissions.reset_permission_cache()


def test_no_permission_prompt_at_startup_logic():
    """Cached-ok => probe never opens the device (no ffmpeg call at all)."""
    permissions._set_cache("camera", True)
    # find_ffmpeg would be called only on a miss; break it to prove we don't
    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setattr(permissions, "find_ffmpeg", lambda: None)
    ok, detail = permissions.probe_device("camera", "0")
    assert (ok, detail) == (True, "")


@pytest.mark.skipif(not permissions.probe_device.__module__, reason="always")
def test_denied_clears_cache(tmp_path, monkeypatch):
    assert not permissions._cached_ok("camera")

    def fake_run(args, timeout):
        return 1, "Permission denied"   # _run_probe returns (rc, err)

    monkeypatch.setattr(permissions, "_run_probe", fake_run)
    monkeypatch.setattr(permissions, "find_ffmpeg", lambda: "/bin/true")
    ok, detail = permissions.probe_device("camera", "0")
    assert ok is False and detail == "denied"
    assert not permissions._cached_ok("camera")   # cache cleared -> re-probe


def test_reset_selective():
    permissions._set_cache("camera", True)
    permissions._set_cache("microphone", True)
    permissions.reset_permission_cache("camera")
    assert not permissions._cached_ok("camera")
    assert permissions._cached_ok("microphone")
