package pairing

import (
	"testing"
	"time"
)

func TestNewCode(t *testing.T) {
	c := NewCode(time.Minute)
	if len(c.Value) != 6 {
		t.Fatalf("code len %d", len(c.Value))
	}
	if !c.Valid() {
		t.Fatal("expected valid")
	}
	if !c.Matches(c.Value) {
		t.Fatal("match failed")
	}
	if c.Matches("000000") && c.Value != "000000" {
		t.Fatal("false match")
	}
}
