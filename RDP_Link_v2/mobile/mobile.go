// Package mobile exposes Go core to Android/iOS via gomobile.
package mobile

import (
	"github.com/eminentailabs/office-rdp/core/identity"
)

// PeerID returns the device peer id, creating identity if needed.
func PeerID(dataDir string) (string, error) {
	id, err := identity.LoadOrCreate(dataDir+"/identity.json", "")
	if err != nil {
		return "", err
	}
	return id.Formatted(), nil
}
