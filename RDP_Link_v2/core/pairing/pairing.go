// Package pairing implements one-time pairing codes and cert pinning helpers.
package pairing

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// Code is a 6-digit one-time pairing code.
type Code struct {
	Value  string
	Expiry time.Time
}

// NewCode generates a 6-digit code valid for ttl.
func NewCode(ttl time.Duration) Code {
	var b [4]byte
	rand.Read(b[:])
	n := binary.BigEndian.Uint32(b[:]) % 1_000_000
	return Code{Value: fmt.Sprintf("%06d", n), Expiry: time.Now().Add(ttl)}
}

// Valid reports whether the code is unexpired.
func (c Code) Valid() bool {
	return time.Now().Before(c.Expiry)
}

// Matches checks value and expiry.
func (c Code) Matches(v string) bool {
	return c.Valid() && c.Value == v
}
