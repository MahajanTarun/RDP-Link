//go:build cgo && !darwin

package host

import (
	"errors"
	"image"
)

// hasLegacyCapture is false on platforms where the primary capture path does
// not need a macOS-specific fallback.
const hasLegacyCapture = false

func captureLegacy(display int) (*image.RGBA, error) {
	return nil, errors.New("legacy capture is only available on macOS")
}
