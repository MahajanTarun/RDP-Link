"""Peer storage for RDP Link (peers.json). Mirrors profiles.ProfileStore discipline."""

import json
import os
import uuid
from datetime import datetime, timezone

from .paths import SUPPORT_DIR, ensure_dirs


def _peers_file():
    ensure_dirs()
    return os.path.join(SUPPORT_DIR, "peers.json")


DEFAULTS_PERMS = {"input": "ask", "clipboard": True, "files": "ask"}


def normalize_peer(p):
    p = dict(p)
    p.setdefault("peerId", "")
    p.setdefault("alias", "")
    p.setdefault("displayName", "")
    p.setdefault("targetKind", "peerId")
    if p["targetKind"] not in ("peerId", "mdns", "ip", "legacyRdp"):
        p["targetKind"] = "peerId"
    pairing = p.get("pairing") or {}
    pairing.setdefault("state", "pending")
    pairing.setdefault("certFp", "")
    p["pairing"] = pairing
    p.setdefault("caps", {})
    p.setdefault("perms", dict(DEFAULTS_PERMS))
    if not p.get("lastSeen"):
        p["lastSeen"] = datetime.now(timezone.utc).isoformat()
    if not p.get("peerId"):
        p["peerId"] = str(uuid.uuid4())
    return p


class PeerStore:
    def __init__(self, path=None):
        self.path = path or _peers_file()
        ensure_dirs()
        try:
            with open(self.path, "r", encoding="utf-8") as f:
                raw = json.load(f)
            if not isinstance(raw, list):
                raise ValueError("peers.json is not a list")
            self.peers = [normalize_peer(p) for p in raw]
        except (OSError, ValueError, json.JSONDecodeError):
            self.peers = []
            self.save()

    def save(self):
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(self.peers, f, indent=2)
        os.replace(tmp, self.path)

    def find(self, peer_id):
        for p in self.peers:
            if p.get("peerId") == peer_id:
                return p
        return None

    def upsert(self, peer):
        peer = normalize_peer(peer)
        existing = self.find(peer["peerId"])
        if existing is not None:
            idx = self.peers.index(existing)
            self.peers[idx] = peer
        else:
            self.peers.append(peer)
        self.save()

    def remove(self, peer_id):
        self.peers = [p for p in self.peers if p.get("peerId") != peer_id]
        self.save()
