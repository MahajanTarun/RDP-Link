package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDerivePeerID(t *testing.T) {
	// deterministic
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i)
	}
	id := DerivePeerID(pub)
	if len(id) != 9 {
		t.Fatalf("expected 9 digits, got %s", id)
	}
	id2 := DerivePeerID(pub)
	if id != id2 {
		t.Fatal("not deterministic")
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	id, err := LoadOrCreate(path, "testhost")
	if err != nil {
		t.Fatal(err)
	}
	if id.PeerID == "" || id.Alias != "testhost" {
		t.Fatalf("bad identity: %+v", id)
	}
	// reload
	id2, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if id2.PeerID != id.PeerID {
		t.Fatal("peer id changed on reload")
	}
	// permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %v", info.Mode().Perm())
	}
}

func TestFormatted(t *testing.T) {
	id := &Identity{PeerID: "123456789"}
	if id.Formatted() != "123 456 789" {
		t.Fatalf("got %s", id.Formatted())
	}
}
