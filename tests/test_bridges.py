"""Receiver handshake protocol + camera enumeration tests."""

import subprocess
import sys

from rdp_manager.bridges import probe_receiver, list_cameras


def _spawn_responder(role, ctl_port, data_port, data_listening):
    """Python stand-in for the receiver control responder (same protocol)."""
    code = f"""
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", {ctl_port}))
data_sock = None
if {data_listening}:
    data_sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    data_sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    data_sock.bind(("127.0.0.1", {data_port}))
while True:
    data, addr = s.recvfrom(256)
    if data.strip() != b"RDPCTL1 PING":
        continue
    state = "open" if {data_listening} else "closed"
    s.sendto(f"RDPCTL1 PONG role={role} data={{state}}".encode(), addr)
"""
    proc = subprocess.Popen([sys.executable, "-c", code])
    return proc


def _wait_responder(ctl_port, deadline_s=3.0):
    """Wait until the test responder answers (avoids bind/startup race)."""
    import time
    end = time.time() + deadline_s
    while time.time() < end:
        if probe_receiver("127.0.0.1", ctl_port, timeout=0.8, attempts=1):
            return True
    return False


def test_handshake_receiver_up():
    p = _spawn_responder("camera", 47274, 47272, data_listening=True)
    try:
        assert _wait_responder(47274)
        resp = probe_receiver("127.0.0.1", 47274, timeout=3)
        assert resp == {"role": "camera", "data": "open"}
    finally:
        p.terminate()


def test_handshake_receiver_up_data_closed():
    p = _spawn_responder("mic", 47275, 47273, data_listening=False)
    try:
        assert _wait_responder(47275)
        resp = probe_receiver("127.0.0.1", 47275, timeout=3)
        assert resp is not None
        assert resp["role"] == "mic"
        assert resp["data"] == "closed"   # app will say "data receiver down"
    finally:
        p.terminate()


def test_handshake_no_receiver():
    assert probe_receiver("127.0.0.1", 47299, timeout=0.4) is None


def test_list_cameras_returns_video_only():
    devs = list_cameras()
    assert isinstance(devs, list) and len(devs) > 0
    joined = " ".join(devs)
    if sys.platform == "darwin":
        # regression: v3 mixed audio devices into the camera picker
        assert "Microphone" not in joined
