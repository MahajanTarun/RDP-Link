package transport

import (
	"testing"
)

func TestNewPeer(t *testing.T) {
	p, err := NewPeer(Config{STUNServers: []string{"stun:stun.l.google.com:19302"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	dc, err := p.AddDataChannel("test")
	if err != nil {
		t.Fatal(err)
	}
	if dc == nil {
		t.Fatal("nil dc")
	}
}

func TestCreateOffer(t *testing.T) {
	p, _ := NewPeer(Config{})
	defer p.Close()
	offer, err := p.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	if offer.Type.String() != "offer" {
		t.Fatalf("got %s", offer.Type)
	}
}
