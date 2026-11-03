// Package peers manages paired peers and known IDs (peers.json).
package peers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Peer is a stored peer entry.
type Peer struct {
	PeerID      string         `json:"peerId"`
	Alias       string         `json:"alias"`
	DisplayName string         `json:"displayName"`
	TargetKind  string         `json:"targetKind"` // peerId|mdns|ip|legacyRdp
	RawTarget   string         `json:"rawTarget"`
	Pairing     PairingState   `json:"pairing"`
	Caps        map[string]any `json:"caps"`
	Perms       map[string]any `json:"perms"`
	LastSeen    string         `json:"lastSeen"`
}

type PairingState struct {
	State  string `json:"state"` // paired|pending|blocked
	CertFP string `json:"certFp"`
}

// Store persists peers.json.
type Store struct {
	Path  string
	Peers []Peer
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".config", "office-rdp")
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support")); err == nil {
		base = filepath.Join(home, "Library", "Application Support", "Office RDP")
	}
	return filepath.Join(base, "peers.json")
}

func New(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	s := &Store{Path: path, Peers: []Peer{}}
	s.Load()
	return s
}

func (s *Store) Load() {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		s.Peers = []Peer{}
		return
	}
	var raw []Peer
	if err := json.Unmarshal(data, &raw); err != nil {
		s.Peers = []Peer{}
		return
	}
	out := make([]Peer, 0, len(raw))
	for _, p := range raw {
		out = append(out, Normalize(p))
	}
	s.Peers = out
}

func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Peers, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func Normalize(p Peer) Peer {
	if p.TargetKind == "" {
		p.TargetKind = "peerId"
	}
	if p.Pairing.State == "" {
		p.Pairing.State = "pending"
	}
	if p.Caps == nil {
		p.Caps = map[string]any{}
	}
	if p.Perms == nil {
		p.Perms = map[string]any{}
	}
	if p.LastSeen == "" {
		p.LastSeen = time.Now().UTC().Format(time.RFC3339)
	}
	return p
}

func (s *Store) Find(peerID string) *Peer {
	for i := range s.Peers {
		if s.Peers[i].PeerID == peerID {
			return &s.Peers[i]
		}
	}
	return nil
}

func (s *Store) Upsert(p Peer) {
	p = Normalize(p)
	for i := range s.Peers {
		if s.Peers[i].PeerID == p.PeerID {
			s.Peers[i] = p
			_ = s.Save()
			return
		}
	}
	s.Peers = append(s.Peers, p)
	_ = s.Save()
}

func (s *Store) Remove(peerID string) {
	out := s.Peers[:0]
	for _, p := range s.Peers {
		if p.PeerID != peerID {
			out = append(out, p)
		}
	}
	s.Peers = out
	_ = s.Save()
}
