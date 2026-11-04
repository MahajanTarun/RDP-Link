"""Headless CLI tests: no tkinter, no Go binary, real HTTP server."""

import json
import os
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from rdp_manager import cli
from rdp_manager import __version__

PASSWORD = "secret"


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _auth(self):
        if self.headers.get("X-Office-Password") != PASSWORD:
            self.send_response(401)
            self.end_headers()
            return False
        return True

    def do_GET(self):
        if not self._auth():
            return
        if self.path == "/info":
            body = json.dumps({"peerId": "123 456 789", "alias": "unit",
                               "lanIp": "127.0.0.1", "wanIp": "1.2.3.4",
                               "port": 8088}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path == "/frame.jpg":
            body = b"\xff\xd8\xff\xe0" + b"0" * 32
            self.send_response(200)
            self.send_header("Content-Type", "image/jpeg")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path == "/clipboard":
            body = json.dumps(
                {"text": getattr(self.server, "clipboard", "hello clipboard")}
            ).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        if not self._auth():
            return
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        payload = json.loads(raw or b"{}")
        self.server.requests.append((self.path, payload))
        if self.path == "/clipboard":
            self.server.clipboard = payload.get("text", "")
        self.send_response(204)
        self.end_headers()


@pytest.fixture
def server():
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    httpd.requests = []
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    try:
        yield httpd
    finally:
        httpd.shutdown()
        httpd.server_close()


@pytest.fixture
def base(server):
    return f"http://127.0.0.1:{server.server_address[1]}"


def test_version(capsys):
    assert cli.main(["--version"]) == 0
    assert __version__ in capsys.readouterr().out


def test_id_reads_identity(tmp_path, monkeypatch, capsys):
    config = tmp_path / ".config" / "office-rdp"
    config.mkdir(parents=True)
    (config / "identity.json").write_text(
        json.dumps({"peerId": "111 222 333", "alias": "box"}))
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(cli, "office_binary", lambda: None)
    assert cli.main(["--id"]) == 0
    assert "111 222 333" in capsys.readouterr().out


def test_local_status_json(tmp_path, monkeypatch, capsys):
    config = tmp_path / ".config" / "office-rdp"
    config.mkdir(parents=True)
    (config / "identity.json").write_text(
        json.dumps({"peerId": "111 222 333", "alias": "box"}))
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(cli, "office_binary", lambda: None)
    assert cli.main(["--status", "--json"]) == 0
    data = json.loads(capsys.readouterr().out)
    assert data["peerId"] == "111 222 333"
    assert data["alias"] == "box"


def test_connect_probe_and_save(base, server, tmp_path, capsys):
    shot = tmp_path / "frame.jpg"
    rc = cli.main(["--connect", base, "--password", PASSWORD, "--probe",
                   "--save-frame", str(shot)])
    assert rc == 0
    assert shot.read_bytes().startswith(b"\xff\xd8")
    out = capsys.readouterr().out
    assert "frame:" in out
    assert "saved:" in out


def test_connect_rejects_wrong_password(base, capsys):
    rc = cli.main(["--connect", base, "--password", "wrong", "--probe"])
    assert rc == 1
    assert "401" in capsys.readouterr().err


def test_connect_clipboard_and_input(base, server, capsys):
    rc = cli.main(["--connect", base, "--password", PASSWORD,
                   "--clipboard-get", "--clipboard-set", "paste me",
                   "--click", "10,20", "--type", "hello"])
    assert rc == 0
    assert "paste me" in capsys.readouterr().out
    assert ("/clipboard", {"text": "paste me"}) in server.requests
    assert ("/input", {"type": "click", "x": 10.0, "y": 20.0,
                       "button": 1}) in server.requests
    assert ("/input", {"type": "text", "text": "hello"}) in server.requests


def test_remote_status_json(base, capsys):
    assert cli.main(["--status", base, "--password", PASSWORD, "--json"]) == 0
    data = json.loads(capsys.readouterr().out)
    assert data["peerId"] == "123 456 789"


def test_connect_dispatches_profile(monkeypatch):
    class Store:
        profiles = [{"id": "p1", "name": "My Profile"}]

    class App:
        def connect_and_wait(self, profile):
            assert profile["name"] == "My Profile"
            return 0

    monkeypatch.setattr("rdp_manager.profiles.ProfileStore", Store)
    monkeypatch.setattr("rdp_manager.connection.HeadlessApp", App)
    assert cli.main(["--connect", "My Profile"]) == 0


def test_remote_options_require_connect(capsys):
    assert cli.main(["--probe"]) == 2
    assert "--connect" in capsys.readouterr().err


def test_cli_import_does_not_load_tkinter():
    code = ("import sys, rdp_manager.cli; "
            "print('tkinter' in sys.modules)")
    out = subprocess.check_output([sys.executable, "-c", code], text=True)
    assert out.strip() == "False"


def test_help_exits_cleanly(capsys):
    with pytest.raises(SystemExit) as exc:
        cli.main(["--help"])
    assert exc.value.code == 0
    assert "--connect" in capsys.readouterr().out


def test_launcher_routes_cli_flags():
    import ubuntu_rdp_manager as entry
    assert entry._is_cli(["--id"])
    assert entry._is_cli(["--connect", "host:8088"])
    assert entry._is_cli(["--nope"])
    assert not entry._is_cli([])


def test_share_uses_exec_without_password(monkeypatch):
    seen = {}
    monkeypatch.setattr(cli, "office_binary", lambda: "/bin/echo")
    monkeypatch.setattr(cli.os, "execv",
                        lambda binary, argv: seen.update(binary=binary,
                                                         argv=argv))
    assert cli.main(["--share", "--port", "9000"]) == 0
    assert seen["argv"][:3] == ["/bin/echo", "--host", "--port"]
    assert "--password" not in seen["argv"]


def test_share_writes_private_password_file(monkeypatch, tmp_path):
    seen = {}

    class Proc:
        def wait(self):
            return 0

    def fake_popen(argv):
        seen["argv"] = argv
        path = argv[argv.index("--password-file") + 1]
        seen["path"] = path
        seen["mode"] = oct(os.stat(path).st_mode & 0o777)
        return Proc()

    monkeypatch.setattr(cli, "office_binary", lambda: "/bin/echo")
    monkeypatch.setattr(cli.subprocess, "Popen", fake_popen)
    assert cli.main(["--share", "--password", "secret"]) == 0
    assert "--password" not in seen["argv"]
    assert seen["mode"] == "0o600"
    assert not os.path.exists(seen["path"])


def test_office_binary_finds_portable_tarball(tmp_path, monkeypatch):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    agent = bindir / "office-rdp-linux"
    agent.write_text("#!/bin/sh\nexit 0\n")
    agent.chmod(0o755)
    launcher = tmp_path / "rdp-link"
    launcher.write_text("")
    monkeypatch.setattr(cli.sys, "executable", str(launcher))
    monkeypatch.setenv("PATH", "")
    assert cli.office_binary() == str(agent)
