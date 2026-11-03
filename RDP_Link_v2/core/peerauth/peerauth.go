// Package peerauth signs and verifies rendezvous requests with the device's
// Ed25519 identity key.
//
// The peer ID is derived from the public key, so a request that carries a
// valid signature can only claim the peer ID that belongs to that key. The
// rendezvous server therefore does not need a shared secret: knowing a peer
// ID is not enough to impersonate it, enumerate the registry or take over a
// tunnel.
package peerauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/eminentailabs/office-rdp/core/identity"
)

const (
	// HeaderPeer carries the normalised (digits only) peer ID.
	HeaderPeer = "X-Rdp-Peer"
	// HeaderTimestamp carries the Unix timestamp the signature covers.
	HeaderTimestamp = "X-Rdp-Timestamp"
	// HeaderPublicKey carries the base64 Ed25519 public key.
	HeaderPublicKey = "X-Rdp-Public-Key"
	// HeaderSignature carries the base64 Ed25519 signature.
	HeaderSignature = "X-Rdp-Signature"

	// MaxSkew is how far a signed request's timestamp may drift.
	MaxSkew = 5 * time.Minute
)

// Canonical builds the string that gets signed. It binds the method, the
// request URI (path and query), the timestamp and a hash of the body, so a
// signature cannot be replayed for a different endpoint or payload.
func Canonical(method, uri, timestamp string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(method)),
		uri,
		timestamp,
		hex.EncodeToString(sum[:]),
	}, "\n")
}

// Sign returns the base64 signature for a request.
func Sign(priv ed25519.PrivateKey, method, uri, timestamp string, body []byte) string {
	if len(priv) != ed25519.PrivateKeySize {
		return ""
	}
	sig := ed25519.Sign(priv, []byte(Canonical(method, uri, timestamp, body)))
	return base64.StdEncoding.EncodeToString(sig)
}

// Verify checks a base64 signature against a public key.
func Verify(pub ed25519.PublicKey, method, uri, timestamp string, body []byte, signature string) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, []byte(Canonical(method, uri, timestamp, body)), sig)
}

// PublicKeyB64 encodes a public key for the wire.
func PublicKeyB64(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// ParsePublicKey decodes a wire public key.
func ParsePublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("peerauth: bad public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("peerauth: public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// PeerID returns the peer ID that belongs to a public key.
func PeerID(pub ed25519.PublicKey) string { return identity.DerivePeerID(pub) }

// Timestamp returns the current Unix timestamp used in signed requests.
func Timestamp() string { return strconv.FormatInt(time.Now().Unix(), 10) }

// CheckTimestamp reports whether a timestamp string is inside the allowed
// skew window.
func CheckTimestamp(value string) bool {
	secs, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return false
	}
	drift := time.Since(time.Unix(secs, 0))
	return drift <= MaxSkew && drift >= -MaxSkew
}

// Headers holds the signature headers for one request.
type Headers struct {
	Peer      string
	Timestamp string
	PublicKey string
	Signature string
}

// Apply sets the signature headers on a request header map.
func Apply(h interface{ Set(string, string) }, priv ed25519.PrivateKey,
	method, uri, timestamp string, body []byte) {
	h.Set(HeaderPeer, PeerID(priv.Public().(ed25519.PublicKey)))
	h.Set(HeaderTimestamp, timestamp)
	h.Set(HeaderPublicKey, PublicKeyB64(priv.Public().(ed25519.PublicKey)))
	h.Set(HeaderSignature, Sign(priv, method, uri, timestamp, body))
}

// RequestURI returns the canonical request URI (path and query) that a
// signature covers. The client and the server must derive it the same way.
func RequestURI(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return u.RequestURI(), nil
}

// Send performs a signed HTTP request against the rendezvous.
func Send(client *http.Client, method, rawURL string, body []byte,
	priv ed25519.PrivateKey, contentType string) (*http.Response, error) {
	uri, err := RequestURI(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	Apply(req.Header, priv, method, uri, Timestamp(), body)
	return client.Do(req)
}
