"""End-to-end connect() flow tests — the path the Connect button runs."""

import subprocess

import pytest

from rdp_manager import bridges as bridges_mod
from rdp_manager import connection as conn_mod
from rdp_manager.connection import ConnectionController

EXE = "/bin/sleep"          # stand-in for sdl-freerdp (stays alive)


class FakeRoot:
    def __init__(self):
        self.jobs = []

    def after(self, ms, fn=None):
        self.jobs.append((ms, fn))


class FakeApp:
    def __init__(self):
        self.root = FakeRoot()
        self.statuses = []

    def set_status(self, text, error=False, warn=False):
        self.statuses.append((text, error, warn))

    def refresh_list(self):
        pass

    def report_permission_denied(self, profile, problems):
        pass


@pytest.fixture
def no_bridges_profile():
    return {"id": "flow1", "name": "Flow", "host": "127.0.0.1", "port": "1",
            "username": "u", "smartSizing": True, "audio": "remote",
            "mic": "remote", "camera": "remote", "camDevice": "0",
            "clipboard": False, "fileTransfer": False, "certTofu": False,
            "stdinPass": False, "savePassword": False, "keyboard": "auto"}


def test_connect_button_path_reaches_freerdp(monkeypatch, no_bridges_profile):
    """Regression: clicking Connect must spawn FreeRDP and mark active.
    (v4.4 broke this: watchdog.spawn rejected text= before FreeRDP ran.)"""
    monkeypatch.setattr(conn_mod, "find_freerdp", lambda: EXE)
    monkeypatch.setattr("rdp_manager.keyboard.resolve_for_connect",
                        lambda profile, blocking=True: "0x409")
    app = FakeApp()
    ctl = ConnectionController(app)
    try:
        ctl.connect(no_bridges_profile)
        assert "flow1" in ctl.processes, "FreeRDP never started"
        proc = ctl.processes["flow1"]
        assert proc.poll() is None           # running
        assert any("Connecting to Flow" in t for t, _, _ in app.statuses)
        assert ctl.is_active("flow1")
    finally:
        for p in list(ctl.processes.values()):
            p.kill()


def test_connect_spawns_bridges_via_watchdog(monkeypatch, no_bridges_profile):
    """Profile routes camera/mic to host -> bridges go through watchdog.spawn."""
    monkeypatch.setattr(conn_mod, "find_freerdp", lambda: EXE)
    monkeypatch.setattr("rdp_manager.keyboard.resolve_for_connect",
                        lambda profile, blocking=True: "0x409")
    spawned, yielded = [], []

    def fake_spawn(args, **kwargs):
        spawned.append((list(args), kwargs))
        proc = subprocess.Popen(["/bin/sleep", "0.4"], stderr=subprocess.PIPE,
                                text=True)
        yielded.append(proc)
        return proc

    monkeypatch.setattr(bridges_mod.watchdog, "spawn", fake_spawn)
    monkeypatch.setattr(bridges_mod, "find_ffmpeg", lambda: "/usr/bin/true")

    app = FakeApp()
    ctl = ConnectionController(app)
    profile = dict(no_bridges_profile, camera="host", mic="host")
    try:
        ctl.connect(profile)
        assert len(spawned) == 2, "camera and mic bridges must both spawn"
        cam_args = spawned[0][0]
        assert any("7272" in a for a in cam_args), "camera bridge UDP target"
        mic_args = spawned[1][0]
        assert any("7273" in a for a in mic_args), "mic bridge UDP target"
        # kwargs passthrough must survive (the v4.4 regressions were here)
        assert spawned[0][1].get("text") is True
        assert "flow1" in ctl.processes
    finally:
        for p in yielded:
            p.kill()


def test_watchdog_spawn_kwargs_passthrough_direct():
    """Direct unit: text/bufsize kwargs reach the underlying Popen."""
    proc = bridges_mod.watchdog.spawn(["/bin/sh", "-c", "echo hello"],
                                      stdout=subprocess.PIPE, text=True)
    out, _ = proc.communicate(timeout=10)
    assert out.strip() == "hello"
