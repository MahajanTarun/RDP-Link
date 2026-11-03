import os

from rdp_manager.peers import PeerStore, normalize_peer


def test_normalize_defaults():
    p = normalize_peer({"peerId": "x"})
    assert p["targetKind"] == "peerId"
    assert p["pairing"]["state"] == "pending"
    assert p["perms"]["clipboard"] is True


def test_store_roundtrip(tmp_path):
    path = str(tmp_path / "peers.json")
    store = PeerStore(path=path)
    store.upsert({"peerId": "a", "alias": "host-a"})
    store.upsert({"peerId": "b", "alias": "host-b"})
    assert len(store.peers) == 2
    store2 = PeerStore(path=path)
    assert len(store2.peers) == 2
    store2.remove("a")
    assert len(store2.peers) == 1
    assert os.path.exists(path)
