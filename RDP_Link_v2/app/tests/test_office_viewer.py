"""Tests for the guest viewer: input sending, throttling and auth headers."""

import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from office_viewer import InputSender


class _Handler(BaseHTTPRequestHandler):
    received = []

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        type(self).received.append({
            "path": self.path,
            "body": json.loads(body or b"{}"),
            "password": self.headers.get("X-Office-Password"),
        })
        self.send_response(204)
        self.end_headers()

    def log_message(self, *_args):
        pass


@pytest.fixture
def server():
    _Handler.received = []
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{httpd.server_address[1]}"
    httpd.shutdown()


def test_input_sender_posts_events(server):
    stop = threading.Event()
    sender = InputSender(server, "secret", stop)
    sender.start()
    sender.send({"type": "click", "x": 10, "y": 20})
    deadline = time.time() + 5
    while time.time() < deadline and not _Handler.received:
        time.sleep(0.05)
    stop.set()
    assert _Handler.received, "no input event arrived"
    event = _Handler.received[0]
    assert event["body"]["type"] == "click"
    assert event["path"] == "/input"
    assert event["password"] == "secret"


def test_input_sender_coalesces_moves(server):
    stop = threading.Event()
    sender = InputSender(server, "", stop)
    sender.start()
    for index in range(50):
        sender.send({"type": "move", "x": index, "y": index})
    sender.send({"type": "click", "x": 1, "y": 1})
    deadline = time.time() + 5
    while time.time() < deadline and len(_Handler.received) < 2:
        time.sleep(0.05)
    stop.set()
    moves = [e for e in _Handler.received if e["body"]["type"] == "move"]
    # Fifty pointer moves collapse into a single event.
    assert len(moves) <= 2
    assert any(e["body"]["type"] == "click" for e in _Handler.received)


def test_input_sender_keeps_the_connection(server):
    stop = threading.Event()
    sender = InputSender(server, "", stop)
    sender.start()
    for _ in range(5):
        sender.send({"type": "click", "x": 1, "y": 1})
        time.sleep(0.05)
    deadline = time.time() + 5
    while time.time() < deadline and len(_Handler.received) < 5:
        time.sleep(0.05)
    stop.set()
    assert len(_Handler.received) >= 5


def test_input_sender_uses_the_path_prefix():
    stop = threading.Event()
    sender = InputSender("http://127.0.0.1:1/t/123456789/", "", stop)
    assert sender._prefix == "/t/123456789"
