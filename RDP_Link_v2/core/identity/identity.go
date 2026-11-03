// Package identity manages the device Ed25519 keypair and the derived
// 9-digit Peer ID ("XXX XXX XXX") used for AnyDesk-style addressing.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Identity is the persisted device identity.
type Identity struct {
	PrivateKey ed25519.PrivateKey `json:"-"`
	PublicKey  ed25519.PublicKey  `json:"-"`
	PeerID     string             `json:"peerId"`
	Alias      string             `json:"alias"`
	// raw persisted form
	PrivateB64 string `json:"privateKey"` // #nosec G117 -- persisted with 0600 perms
	PublicB64  string `json:"publicKey"`
}

// DefaultPath returns the OS-appropriate identity file location.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".config", "office-rdp")
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support")); err == nil {
		base = filepath.Join(home, "Library", "Application Support", "Office RDP")
	}
	return filepath.Join(base, "identity.json")
}

// LoadOrCreate loads an existing identity or creates a new one.
func LoadOrCreate(path, alias string) (*Identity, error) {
	if path == "" {
		path = DefaultPath()
	}
	// #nosec G304 -- path is the app's own identity file, not remote input.
	if data, err := os.ReadFile(path); err == nil {
		var id Identity
		if err := json.Unmarshal(data, &id); err == nil {
			if id.PrivateB64 != "" {
				priv, err := base64.StdEncoding.DecodeString(id.PrivateB64)
				if err == nil && len(priv) == ed25519.PrivateKeySize {
					id.PrivateKey = ed25519.PrivateKey(priv)
					pub, _ := base64.StdEncoding.DecodeString(id.PublicB64)
					id.PublicKey = ed25519.PublicKey(pub)
					if id.PeerID == "" {
						id.PeerID = DerivePeerID(id.PublicKey)
					}
					if alias != "" {
						id.Alias = alias
					}
					return &id, nil
				}
			}
		}
	}
	// create new
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := &Identity{
		PrivateKey: priv,
		PublicKey:  pub,
		PrivateB64: base64.StdEncoding.EncodeToString(priv),
		PublicB64:  base64.StdEncoding.EncodeToString(pub),
		PeerID:     DerivePeerID(pub),
		Alias:      alias,
	}
	if alias == "" {
		hn, _ := os.Hostname()
		id.Alias = sanitizeAlias(hn)
	}
	if err := id.Save(path); err != nil {
		return nil, err
	}
	return id, nil
}

// DerivePeerID derives a 9-digit numeric ID from the public key fingerprint.
func DerivePeerID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	// take first 4 bytes as uint32, mod 1e9, zero-pad to 9 digits
	n := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	v := n % 1_000_000_000
	return fmt.Sprintf("%09d", v)
}

// Formatted returns "XXX XXX XXX".
func (id *Identity) Formatted() string {
	s := id.PeerID
	if len(s) != 9 {
		return s
	}
	return s[0:3] + " " + s[3:6] + " " + s[6:9]
}

// Fingerprint returns hex SHA256 of public key (first 16 hex).
func (id *Identity) Fingerprint() string {
	sum := sha256.Sum256(id.PublicKey)
	return hex.EncodeToString(sum[:])[:16]
}

// Save persists identity to path with 0600 perms.
func (id *Identity) Save(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(id, "", "  ") // #nosec G117 -- persisted with 0600 perms
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sanitizeAlias(hn string) string {
	hn = strings.TrimSpace(hn)
	hn = strings.ToLower(hn)
	// keep alphanumerics and hyphens
	var b strings.Builder
	for _, r := range hn {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.String() == "" {
		return "office-host"
	}
	return b.String()
}

// PublicKeyHex helper.
func PublicKeyHex(pub ed25519.PublicKey) string {
	return hex.EncodeToString(pub)
}
