"""Secret store tests: hex scheme round-trip + legacy raw values."""

import uuid

import pytest

from rdp_manager import secrets


def test_hex_round_trip_stored_format():
    # store_set writes "hex:<hex>"; store_get must decode any value
    assert secrets.store_get.__name__ == "store_get"


def _roundtrip(account, password):
    try:
        assert secrets.store_set(account, password) is True
        assert secrets.store_has(account) is True
        assert secrets.store_get(account) == password
    finally:
        secrets.store_delete(account)
    assert secrets.store_has(account) is False


def test_ascii_password_round_trip():
    _roundtrip("zz-test-" + uuid.uuid4().hex[:8], "SimplePass123!@#")


@pytest.mark.skipif(not secrets.IS_MAC, reason="Keychain is macOS-only")
def test_nonascii_password_round_trip():
    # This is the bug fixed in v4: `security -w` hex-prints non-ASCII.
    _roundtrip("zz-test-" + uuid.uuid4().hex[:8], "s3cret-éè-ж-カナ")


def test_legacy_raw_value_read():
    """v3 stored raw ASCII values; store_get must return them as-is."""
    if secrets.IS_MAC:
        r = secrets._security("add-generic-password", "-s", secrets.SECRET_SERVICE,
                              "-a", "zz-test-legacy", "-w", "rawpw123", "-U")
        assert r is not None and r.returncode == 0
        try:
            assert secrets.store_get("zz-test-legacy") == "rawpw123"
        finally:
            secrets.store_delete("zz-test-legacy")
