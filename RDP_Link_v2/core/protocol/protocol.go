// Package protocol defines signalling messages and data-channel framing.
package protocol

import "encoding/json"

const ProtocolVersion = "1"

type Hello struct {
	Type        string         `json:"type"` // "hello"
	PeerID      string         `json:"peerId"`
	Alias       string         `json:"alias"`
	Version     string         `json:"version"`
	Caps        map[string]any `json:"caps"`
	Policy      map[string]any `json:"policy"`
	Fingerprint string         `json:"fingerprint"`
}

type Offer struct {
	Type          string   `json:"type"` // "offer"
	From          string   `json:"from"`
	To            string   `json:"to"`
	SDP           string   `json:"sdp"`
	Password      string   `json:"password,omitempty"`
	ICECandidates []string `json:"iceCandidates"`
}

type Answer struct {
	Type          string   `json:"type"` // "answer"
	From          string   `json:"from"`
	To            string   `json:"to"`
	SDP           string   `json:"sdp"`
	ICECandidates []string `json:"iceCandidates"`
}

type ICECandidate struct {
	Type      string `json:"type"` // "ice"
	From      string `json:"from"`
	To        string `json:"to"`
	Candidate string `json:"candidate"`
}

type PairRequest struct {
	Type           string         `json:"type"` // "pairRequest"
	From           string         `json:"from"`
	To             string         `json:"to"`
	Code           string         `json:"code,omitempty"`
	RequestedPerms map[string]any `json:"requestedPerms,omitempty"`
}

type PairGrant struct {
	Type   string         `json:"type"` // "pairGrant"
	From   string         `json:"from"`
	To     string         `json:"to"`
	CertFP string         `json:"certFp"`
	Perms  map[string]any `json:"perms"`
	Expiry string         `json:"expiry,omitempty"`
}

type Presence struct {
	Type   string `json:"type"` // "presence"
	PeerID string `json:"peerId"`
	Status string `json:"status"` // online|away|busy|offline
}

type Control struct {
	Type    string         `json:"type"`   // "control"
	Action  string         `json:"action"` // requestInput|releaseInput|toggle...
	Payload map[string]any `json:"payload,omitempty"`
}

type ChatMessage struct {
	Type string `json:"type"` // "chat"
	From string `json:"from"`
	Text string `json:"text"`
}

type FileChunk struct {
	Type       string `json:"type"` // "fileChunk"
	TransferID string `json:"transferId"`
	Offset     int64  `json:"offset"`
	Bytes      []byte `json:"bytes"`
	EOF        bool   `json:"eof"`
}

// Envelope is a generic typed message used on the signalling channel.
type Envelope struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

// ParseType extracts the "type" field.
func ParseType(b []byte) string {
	var m struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Type
}

func MustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
