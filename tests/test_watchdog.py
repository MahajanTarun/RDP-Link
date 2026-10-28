"""Watchdog tests: forwarding, forwarding SIGTERM, and parent-death kill."""

import os
import signal
import subprocess
import sys
import time

import pytest

from rdp_manager import watchdog

SLEEPER = ["/bin/sh", "-c", "sleep 30"]


def _entry():
    """The file that understands --bridge-watchdog (works frozen or source)."""
    return [sys.executable,
            os.path.join(os.path.dirname(os.path.dirname(
                os.path.abspath(__file__))), "ubuntu_rdp_manager.py")]


@pytest.fixture(autouse=True)
def _fast_poll(monkeypatch):
    monkeypatch.setattr(watchdog, "_POLL_S", 0.1)


def test_wrapper_forwards_child_exit_code():
    proc = watchdog.spawn(["/bin/sh", "-c", "exit 7"])
    assert proc.wait(timeout=10) == 7


def test_wrapper_sigterm_kills_child():
    proc = watchdog.spawn(SLEEPER)
    time.sleep(0.3)
    proc.terminate()
    assert proc.wait(timeout=5) in (128 + signal.SIGTERM, -signal.SIGTERM)
    # child must be gone too — no orphan sleeper of this wrapper's family
    time.sleep(0.5)
    out = subprocess.run(["pgrep", "-fl", "sleep 30"],
                         capture_output=True, text=True).stdout
    assert "sleep 30" not in out, "orphaned child survived SIGTERM!"


def test_parent_death_kills_child():
    """The core guarantee: app (parent) hard-killed -> child dies <= ~1.5s."""
    fake_app = subprocess.Popen(["/bin/sleep", "60"])   # pretend it's the app
    wrapper = subprocess.Popen(
        _entry() + ["--bridge-watchdog", str(fake_app.pid), "--", *SLEEPER],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(0.5)
    fake_app.kill()                                     # SIGKILL = worst case
    fake_app.wait()
    t0 = time.time()
    rc = wrapper.wait(timeout=4)
    elapsed = time.time() - t0
    assert rc == watchdog.WATCHDOG_KILLED, f"wrapper rc={rc}"
    assert elapsed < 3.0, "child survived too long after parent death"
    time.sleep(0.5)
    out = subprocess.run(["pgrep", "-fl", "sleep 30"],
                         capture_output=True, text=True).stdout
    assert "sleep 30" not in out, "orphaned child survived parent death!"


def test_parent_death_survivor_check_positive_case():
    """Sanity: without watchdog, a child would outlive the parent (design)."""
    parent = subprocess.Popen(["/bin/sleep", "60"])
    child = subprocess.Popen(SLEEPER)
    parent.kill(); parent.wait()
    time.sleep(0.5)
    alive = child.poll() is None
    child.kill(); child.wait()
    assert alive, "premise broken: child died on its own"


def test_watchdog_rejects_empty_argv():
    assert watchdog.run(1, []) == 2


def test_device_chips_reflect_active_bridges():
    from rdp_manager.bridges import BridgeManager

    class FakeApp:
        class root:
            @staticmethod
            def after(ms, fn=None):
                pass

        @staticmethod
        def set_status(text, error=False, warn=False):
            pass

    bm = BridgeManager(FakeApp())
    assert bm.device_chips("p1") == ""
    bm.camera_procs["p1"] = subprocess.Popen(["/bin/sleep", "5"])
    try:
        assert bm.device_chips("p1") == "🎥"
        bm.mic_procs["p1"] = subprocess.Popen(["/bin/sleep", "5"])
        assert bm.device_chips("p1") == "🎥 🎤"
        bm.camera_procs["p1"].terminate(); bm.camera_procs["p1"].wait()
        assert bm.device_chips("p1") == "🎤"
    finally:
        for p in list(bm.camera_procs.values()) + list(bm.mic_procs.values()):
            p.kill()
