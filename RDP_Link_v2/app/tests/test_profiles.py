"""Profile normalization / migration tests."""

from rdp_manager.profiles import normalize


def test_empty_gets_defaults():
    n = normalize({})
    assert n["port"] == "3389"
    assert n["audio"] == "host"
    assert n["mic"] == "host"
    assert n["camera"] == "remote"
    assert n["stdinPass"] is True
    assert n["certTofu"] is True


def test_v1_v2_boolean_migration():
    n = normalize({"id": "x", "audioOut": False, "microphone": False,
                   "camera": True, "micBridge": True})
    assert n["audio"] == "remote"
    assert n["mic"] == "remote"
    assert n["camera"] == "host"
    assert "audioOut" not in n and "microphone" not in n and "micBridge" not in n


def test_legacy_boolean_leak_through():
    n = normalize({"camera": True, "mic": False, "audio": "weird"})
    assert n["camera"] == "host"
    assert n["mic"] == "remote"
    assert n["audio"] == "host"


def test_native_mic_allowed_but_not_audio_or_camera():
    n = normalize({"mic": "native"})
    assert n["mic"] == "native"
    n = normalize({"audio": "native"})
    assert n["audio"] == "host"      # coerced back: audio has no native mode
    n = normalize({"camera": "native"})
    assert n["camera"] == "remote"


def test_bad_port_falls_back():
    assert normalize({"port": "abc"})["port"] == "3389"
    assert normalize({"port": "99999"})["port"] == "3389"
    assert normalize({"port": 3390})["port"] == "3390"


def test_cam_device_none_fills_default():
    assert normalize({"camDevice": None})["camDevice"] == "0"
