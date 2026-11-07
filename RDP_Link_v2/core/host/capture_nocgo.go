//go:build !cgo

package host

import (
	"errors"
	"time"
)

func (a *Agent) StartCapture(display int) error {
	a.SetDisplay(display)
	// full screen capture requires cgo (openh264 + robotgo)
	return nil
}

func displayCount() int { return 1 }

func displaySize(i int) (int, int) { return 0, 0 }

// ProbeCapture is a no-op failure when the build has no cgo capture support.
func ProbeCapture(display int, timeout time.Duration) error {
	return errors.New("screen capture requires a cgo build")
}
