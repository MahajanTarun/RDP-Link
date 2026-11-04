// Package mobile exposes Go core to Android/iOS via gomobile.
package mobile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/eminentailabs/office-rdp/core/identity"
	"github.com/eminentailabs/office-rdp/core/peerauth"
	"github.com/eminentailabs/office-rdp/core/relay"
)

// Relay is a running outbound tunnel to the rendezvous server. Keeping the
// tunnel open is what makes an Android host reachable across NAT instead of
// only on the local network.
type Relay struct {
	done chan struct{}
	once sync.Once
}

// StartRelay connects to the rendezvous in the background and forwards
// requests to the local host server on localPort. It returns immediately;
// Stop releases the connection.
func StartRelay(rendezvous, dataDir string, localPort int, insecure bool) (*Relay, error) {
	if rendezvous == "" {
		return nil, fmt.Errorf("rendezvous is empty")
	}
	id, err := identity.LoadOrCreate(filepath.Join(dataDir, "identity.json"), "")
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	client := &relay.Client{
		Rendezvous: rendezvous,
		PeerID:     peerauth.PeerID(id.PublicKey),
		PrivateKey: id.PrivateKey,
		LocalPort:  localPort,
		Insecure:   insecure,
		Done:       done,
	}
	r := &Relay{done: done}
	go client.Run()
	return r, nil
}

// Stop closes the tunnel and stops reconnecting.
func (r *Relay) Stop() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.done) })
}

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
	nonce := peerauth.Nonce()
	if nonce == "" {
		return "", fmt.Errorf("cannot generate a nonce")
	}
	headers := map[string]string{
		peerauth.HeaderPeer:      peerauth.PeerID(id.PublicKey),
		peerauth.HeaderTimestamp: timestamp,
		peerauth.HeaderNonce:     nonce,
		peerauth.HeaderPublicKey: peerauth.PublicKeyB64(id.PublicKey),
		peerauth.HeaderSignature: peerauth.Sign(id.PrivateKey, method, uri, timestamp, nonce, []byte(body)),
	}
	out, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
