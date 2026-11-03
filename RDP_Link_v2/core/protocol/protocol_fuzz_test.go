package protocol

import (
	"testing"
)

func FuzzParseType(f *testing.F) {
	f.Add(`{"type":"hello","peerId":"1"}`)
	f.Add(`{"type":`)
	f.Add(``)
	f.Fuzz(func(t *testing.T, data string) {
		_ = ParseType([]byte(data))
	})
}

func FuzzMustJSON(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add("peer-id:12345")
	f.Fuzz(func(t *testing.T, text string) {
		hello := Hello{Type: "hello", PeerID: text}
		out := ParseType(MustJSON(hello))
		if out != "hello" {
			t.Fatalf("ParseType failed for %q", text)
		}
	})
}
