"""BridgeManager orchestration tests (no real processes spawned)."""

from rdp_manager.bridges import BridgeManager


class FakeRoot:
    """after() runs 0ms jobs immediately (main-loop callback marshaling),
    stores real timers for inspection."""

    def __init__(self):
        self.timers = []

    def after(self, ms, fn=None):
        if ms == 0:
            fn()
        else:
            self.timers.append((ms, fn))


class FakeApp:
    def __init__(self):
        self.root = FakeRoot()
        self.statuses = []

    def set_status(self, text, error=False, warn=False):
        self.statuses.append(text)


PROFILE = {"id": "p1", "name": "T", "host": "192.0.2.9", "port": "3389",
           "username": "u", "camera": "host", "mic": "host"}


def test_start_spawns_needed_bridges_and_schedules_handshake(monkeypatch):
    bm = BridgeManager(FakeApp())
    spawned = []
    monkeypatch.setattr(bm, "start_camera", lambda p: spawned.append("cam") or True)
    monkeypatch.setattr(bm, "start_mic", lambda p: spawned.append("mic") or True)

    assert bm.start(PROFILE) is True
    assert spawned == ["cam", "mic"]
    health = [(ms, fn) for ms, fn in bm.app.root.timers if ms == 2500]
    assert len(health) == 1


def test_start_skips_routed_remote_and_still_schedules(monkeypatch):
    bm = BridgeManager(FakeApp())
    spawned = []
    monkeypatch.setattr(bm, "start_camera", lambda p: spawned.append("cam") or True)
    monkeypatch.setattr(bm, "start_mic", lambda p: spawned.append("mic") or True)

    profile = dict(PROFILE, camera="remote")
    assert bm.start(profile) is True
    assert spawned == ["mic"]


def test_start_failure_aborts_and_does_not_schedule(monkeypatch):
    bm = BridgeManager(FakeApp())
    monkeypatch.setattr(bm, "start_camera", lambda p: False)
    assert bm.start(PROFILE) is False
    assert bm.app.root.timers == []


def _run_monitor(bm, app, profile, announce=False, monkeypatch=None):
    """_monitor probes in a thread; make the thread synchronous for tests."""
    import threading

    class SyncThread:
        def __init__(self, target, args=None, kwargs=None, daemon=None):
            self._target = target
            self._args = args or ()
        def start(self):
            self._target(*self._args)

    monkeypatch.setattr(threading, "Thread", SyncThread)
    bm._monitor(profile, announce=announce)


def test_handshake_probe_off_main_thread_and_applies_status(monkeypatch):
    """The core v4.4.2 fix: probe_receiver must NOT run on the Tk thread.
    FakeRoot runs after(0) immediately, but the 15s timer just gets stored."""
    app = FakeApp()
    bm = BridgeManager(app)
    bm.camera_procs["p1"] = object()
    monkeypatch.setattr("rdp_manager.bridges.probe_receiver",
                        lambda host, port, timeout=1.5, attempts=2:
                        {"data": "open"})
    _run_monitor(bm, app, PROFILE, announce=True, monkeypatch=monkeypatch)
    # re-armed itself for 15s (healthy)
    assert any(ms == 15000 for ms, _ in app.root.timers)
    assert any("receiver detected" in s for s in app.statuses)


def test_handshake_backoff_after_three_misses(monkeypatch):
    app = FakeApp()
    bm = BridgeManager(app)
    bm.camera_procs["p1"] = object()
    bm._misses["p1"] = 3
    monkeypatch.setattr("rdp_manager.bridges.probe_receiver",
                        lambda host, port, timeout=1.5, attempts=2: None)
    _run_monitor(bm, app, PROFILE, monkeypatch=monkeypatch)
    assert any(ms == 60000 for ms, _ in app.root.timers)
    assert not any(ms == 15000 for ms, _ in app.root.timers)
    assert any("no receiver response" in s for s in app.statuses)
    assert bm._misses["p1"] == 4   # counted this round too
