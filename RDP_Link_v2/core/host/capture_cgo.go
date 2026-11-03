//go:build cgo

package host

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/jpeg"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/openh264"
)

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
	img, err = screenshot.CaptureDisplay(s.agent.CurrentDisplay())
	return img, func() {}, err
}

// displayCount reports how many monitors can be captured.
func displayCount() int { return screenshot.NumActiveDisplays() }

// displaySize returns a monitor's pixel dimensions.
func displaySize(i int) (int, int) {
	b := screenshot.GetDisplayBounds(i)
	return b.Dx(), b.Dy()
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
	for range ticker.C {
		var frame *image.RGBA
		var err error
		if a.TestPattern {
			frame = testPatternFrame(time.Now())
		} else {
			frame, err = screenshot.CaptureDisplay(a.CurrentDisplay())
		}
		if err != nil {
			if err.Error() != lastErr {
				lastErr = err.Error()
				fmt.Println("capture:", err)
			}
			continue
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
