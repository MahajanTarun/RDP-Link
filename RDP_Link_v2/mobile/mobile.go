// Package mobile exposes Go core to Android/iOS via gomobile.
package mobile

import (
	"encoding/json"

	"github.com/eminentailabs/office-rdp/core/identity"
	"github.com/eminentailabs/office-rdp/core/peerauth"
)

// PeerID returns the device peer id, creating identity if needed.
func PeerID(dataDir string) (string, error) {
	id, err := identity.LoadOrCreate(dataDir+"/identity.json", "")
	if err != nil {
		return "", err
	}
	return id.Formatted(), nil
}

// SignRequest signs a request with the device identity key and returns the
// signature headers as a JSON object:
//
//	{"X-Rdp-Peer":"...","X-Rdp-Timestamp":"...","X-Rdp-Public-Key":"...","X-Rdp-Signature":"..."}
//
// The rendezvous only accepts requests that carry a valid signature, so the
// Android host can register itself and open a tunnel without a shared
// password.
func SignRequest(dataDir, method, rawURL, body string) (string, error) {
	id, err := identity.LoadOrCreate(dataDir+"/identity.json", "")
	if err != nil {
		return "", err
	}
	uri, err := peerauth.RequestURI(rawURL)
	if err != nil {
		return "", err
	}
	timestamp := peerauth.Timestamp()
	headers := map[string]string{
		peerauth.HeaderPeer:      peerauth.PeerID(id.PublicKey),
		peerauth.HeaderTimestamp: timestamp,
		peerauth.HeaderPublicKey: peerauth.PublicKeyB64(id.PublicKey),
		peerauth.HeaderSignature: peerauth.Sign(id.PrivateKey, method, uri, timestamp, []byte(body)),
	}
	out, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
