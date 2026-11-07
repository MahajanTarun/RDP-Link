//go:build cgo

package host

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/jpeg"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/openh264"
)

var (
	errCaptureTimeout = errors.New("screen capture timed out")
	errCaptureBusy    = errors.New("screen capture already in progress")
)

type captureResult struct {
	img *image.RGBA
	err error
}

type screenshotSource struct {
	agent  *Agent
	closed chan struct{}
}

func (s *screenshotSource) ID() string { return fmt.Sprint(s.agent.CurrentDisplay()) }
func (s *screenshotSource) Close() error {
	if s.closed != nil {
		close(s.closed)
		s.closed = nil
	}
	return nil
}
func (s *screenshotSource) Read() (img image.Image, release func(), err error) {
	// The HTTP frame loop owns the ScreenCaptureKit watchdog. The WebRTC
	// track simply follows the path the loop has selected, so a blocked
	// SCK call cannot stall both pipelines.
	if s.agent.useLegacyCapture() {
		img, err = captureLegacy(s.agent.CurrentDisplay())
	} else {
		img, err = screenshot.CaptureDisplay(s.agent.CurrentDisplay())
	}
	return img, func() {}, err
}

// displayCount reports how many monitors can be captured.
func displayCount() int { return screenshot.NumActiveDisplays() }

// displaySize returns a monitor's pixel dimensions.
func displaySize(i int) (int, int) {
	b := screenshot.GetDisplayBounds(i)
	return b.Dx(), b.Dy()
}

// useLegacyCapture reports whether the synchronous fallback is active.
func (a *Agent) useLegacyCapture() bool {
	if a.ForceLegacyCapture {
		return true
	}
	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	return a.captureLegacy
}

func (a *Agent) markLegacyCapture(reason string) {
	a.captureMu.Lock()
	already := a.captureLegacy
	a.captureLegacy = true
	a.captureMu.Unlock()
	if !already {
		fmt.Println("capture:", reason)
	}
}

// grabFrame captures one frame with a watchdog. ScreenCaptureKit waits on a
// dispatch semaphore forever when HALSystem::InitializeDevices stalls on some
// macOS builds. If that happens, this switches the process to the synchronous
// CoreGraphics path instead of freezing the HTTP frame loop.
func (a *Agent) grabFrame() (*image.RGBA, error) {
	if a.useLegacyCapture() {
		return captureLegacy(a.CurrentDisplay())
	}

	a.captureMu.Lock()
	if a.captureLegacy {
		a.captureMu.Unlock()
		return captureLegacy(a.CurrentDisplay())
	}
	if a.captureBusy {
		a.captureMu.Unlock()
		return nil, errCaptureBusy
	}
	a.captureBusy = true
	a.captureMu.Unlock()

	done := make(chan captureResult, 1)
	go func() {
		img, err := screenshot.CaptureDisplay(a.CurrentDisplay())
		done <- captureResult{img: img, err: err}
	}()

	var result captureResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		a.captureMu.Lock()
		a.captureBusy = false
		a.captureMu.Unlock()
		if hasLegacyCapture {
			a.markLegacyCapture("ScreenCaptureKit timed out; using CoreGraphics capture")
			return captureLegacy(a.CurrentDisplay())
		}
		return nil, errCaptureTimeout
	case <-a.done:
		return nil, errCaptureTimeout
	}

	a.captureMu.Lock()
	a.captureBusy = false
	a.captureMu.Unlock()

	if result.err != nil {
		if hasLegacyCapture {
			a.markLegacyCapture(fmt.Sprintf(
				"ScreenCaptureKit failed (%v); using CoreGraphics capture", result.err))
			return captureLegacy(a.CurrentDisplay())
		}
		return nil, result.err
	}
	return result.img, nil
}

// ProbeCapture checks the screen capture path with a deadline. It is used by
// --doctor so a stalled capture reports a failure instead of hanging.
func ProbeCapture(display int, timeout time.Duration) error {
	// On macOS the ScreenCaptureKit path can stall indefinitely, so prefer
	// the synchronous CoreGraphics fallback when it is available. It is the
	// same path the HTTP frame loop falls back to.
	done := make(chan error, 1)
	go func() {
		_, err := screenshot.CaptureDisplay(display)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if hasLegacyCapture {
			if _, lerr := captureLegacy(display); lerr == nil {
				return nil
			} else {
				return lerr
			}
		}
		return err
	case <-time.After(timeout):
		if hasLegacyCapture {
			if _, lerr := captureLegacy(display); lerr == nil {
				return nil
			} else {
				return lerr
			}
		}
		return errCaptureTimeout
	}
}

// StartCapture starts two pipelines:
//
//   - an HTTP frame loop that grabs the screen directly and encodes JPEG, which
//     is what the desktop viewer, browser viewer, and Android app consume;
//   - a WebRTC video track for peer-to-peer sessions.
//
// The HTTP loop does not depend on the WebRTC encoder, so screen sharing keeps
// working even when the H.264 encoder cannot be initialised.
func (a *Agent) StartCapture(display int) error {
	a.SetDisplay(display)
	go a.httpFrameLoop()

	params, err := openh264.NewParams()
	if err != nil {
		return err
	}
	selector := mediadevices.NewCodecSelector(mediadevices.WithVideoEncoders(&params))
	source := &screenshotSource{agent: a, closed: make(chan struct{})}
	track := mediadevices.NewVideoTrack(source, selector).(*mediadevices.VideoTrack)
	a.vt = track
	return nil
}

// httpFrameLoop captures at a steady rate and keeps the latest JPEG frame.
func (a *Agent) httpFrameLoop() {
	ticker := time.NewTicker(90 * time.Millisecond)
	defer ticker.Stop()
	var lastErr string
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
		}
		var frame *image.RGBA
		var err error
		if a.TestPattern {
			frame = testPatternFrame(time.Now())
		} else {
			frame, err = a.grabFrame()
		}
		if err != nil {
			if err.Error() != lastErr {
				lastErr = err.Error()
				fmt.Println("capture:", err)
			}
			// Keep the stream alive while the real capture recovers. Once a
			// real frame has arrived, serve the last one instead of replacing
			// it with a test pattern.
			a.mu.Lock()
			hasFrame := a.frameSeq > 0
			a.mu.Unlock()
			if hasFrame {
				continue
			}
			frame = testPatternFrame(time.Now())
		} else {
			lastErr = ""
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: 72}); err != nil {
			continue
		}
		h := fnv.New64a()
		h.Write(buf.Bytes())
		a.mu.Lock()
		a.frameJPG = buf.Bytes()
		a.raw = frame
		a.frameSeq++
		a.frameHash = h.Sum64()
		a.mu.Unlock()
	}
}
