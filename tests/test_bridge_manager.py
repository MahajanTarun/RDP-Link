"""BridgeManager orchestration tests (no real processes spawned)."""

from rdp_manager.bridges import BridgeManager


class FakeRoot:
    def __init__(self):
        self.scheduled = []

    def after(self, ms, fn=None):
        self.scheduled.append((ms, fn))


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
    # exactly one handshake monitor scheduled at 2.5 s, announcing
    health = [(ms, fn) for ms, fn in bm.app.root.scheduled if ms == 2500]
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
    assert bm.app.root.scheduled == []


def test_monitor_loop_rearms_every_15s(monkeypatch):
    app = FakeApp()
    bm = BridgeManager(app)
    bm.camera_procs["p1"] = object()          # pretend camera bridge alive
    monkeypatch.setattr("rdp_manager.bridges.probe_receiver",
                        lambda host, port, timeout=1.5, attempts=2: {"data": "open"})
    bm._monitor(PROFILE, announce=True)
    # re-armed itself
    assert any(ms == 15000 for ms, _ in app.root.scheduled)
    # announce surfaced a receiver-detected status
    assert any("receiver detected" in s for s in app.statuses)
