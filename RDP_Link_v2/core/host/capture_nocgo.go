//go:build !cgo

package host

func (a *Agent) StartCapture(display int) error {
	// full screen capture requires cgo (openh264 + robotgo)
	return nil
}
