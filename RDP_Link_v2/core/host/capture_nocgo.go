//go:build !cgo

package host

func (a *Agent) StartCapture(display int) error {
	a.SetDisplay(display)
	// full screen capture requires cgo (openh264 + robotgo)
	return nil
}

func displayCount() int { return 1 }

func displaySize(i int) (int, int) { return 0, 0 }
