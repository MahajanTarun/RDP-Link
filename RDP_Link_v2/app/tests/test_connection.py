"""FreeRDP argument building tests."""

import os

from rdp_manager.connection import build_args, format_host, sanitize_port

HOME = os.path.expanduser("~")

BASE = {"id": "t", "name": "T", "host": "1.2.3.4", "port": "3389",
        "username": "u", "smartSizing": True, "audio": "host", "mic": "host",
        "camera": "host", "camDevice": "0", "clipboard": True,
        "fileTransfer": True, "stdinPass": True, "certTofu": True}


def test_full_profile():
    a = build_args(BASE, stdin_pass=True)
    assert a == ["/v:1.2.3.4:3389", "/u:u", "/smart-sizing", "/sound",
                 "/clipboard", f"/drive:MacFiles,{HOME}", "/cert:tofu",
                 "/from-stdin"]


def test_everything_off():
    p = dict(BASE, audio="remote", mic="remote", camera="remote",
             clipboard=False, fileTransfer=False, certTofu=False,
             smartSizing=False)
    assert build_args(p) == ["/v:1.2.3.4:3389", "/u:u"]


def test_native_mic():
    p = dict(BASE, mic="native")
    assert "/microphone" in build_args(p)
    assert "/sound" in build_args(p)


def test_password_never_in_args():
    assert not any("pw" in x.lower() or "pass" in x.lower()
                   for x in build_args(BASE, stdin_pass=True))


def test_ipv6_host():
    assert format_host("::1") == "[::1]"
    assert format_host("[::2]") == "[::2]"
    assert format_host("192.168.1.1") == "192.168.1.1"
    a = build_args(dict(BASE, host="::1", port="3390"))
    assert a[0] == "/v:[::1]:3390"


def test_port_sanitized():
    assert sanitize_port("abc") == "3389"
    assert sanitize_port("0") == "3389"
    assert sanitize_port("70000") == "3389"
    assert sanitize_port(" 3390 ") == "3390"
    a = build_args(dict(BASE, port="abc"))
    assert a[0] == "/v:1.2.3.4:3389"
