package protocol

import (
	"encoding/json"
	"testing"
)

func TestHelloRoundTrip(t *testing.T) {
	h := Hello{Type: "hello", PeerID: "123 456 789", Alias: "host", Version: ProtocolVersion}
	b := MustJSON(h)
	if ParseType(b) != "hello" {
		t.Fatal("type")
	}
	var out Hello
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.PeerID != h.PeerID {
		t.Fatal("peerid")
	}
}

func TestOfferJSON(t *testing.T) {
	o := Offer{Type: "offer", From: "a", To: "b", SDP: "v=0"}
	b := MustJSON(o)
	var out Offer
	json.Unmarshal(b, &out)
	if out.SDP != "v=0" {
		t.Fatal("sdp")
	}
}
