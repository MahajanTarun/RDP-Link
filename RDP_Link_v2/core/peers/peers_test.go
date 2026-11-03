package peers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalize(t *testing.T) {
	p := Normalize(Peer{PeerID: "123"})
	if p.TargetKind != "peerId" {
		t.Fatal("targetKind default")
	}
	if p.Pairing.State != "pending" {
		t.Fatal("pairing default")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")
	s := New(path)
	s.Upsert(Peer{PeerID: "111", Alias: "a"})
	s.Upsert(Peer{PeerID: "222", Alias: "b"})
	if len(s.Peers) != 2 {
		t.Fatalf("len %d", len(s.Peers))
	}
	// reload
	s2 := New(path)
	if len(s2.Peers) != 2 {
		t.Fatalf("reload len %d", len(s2.Peers))
	}
	s2.Remove("111")
	if len(s2.Peers) != 1 {
		t.Fatal("remove failed")
	}
	// file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
