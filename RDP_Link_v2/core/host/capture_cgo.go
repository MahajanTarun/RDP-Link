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
	display int
	closed  chan struct{}
}

func (s *screenshotSource) ID() string { return fmt.Sprint(s.display) }
func (s *screenshotSource) Close() error {
	if s.closed != nil {
		close(s.closed)
		s.closed = nil
	}
	return nil
}
func (s *screenshotSource) Read() (img image.Image, release func(), err error) {
	img, err = screenshot.CaptureDisplay(s.display)
	return img, func() {}, err
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
	go a.httpFrameLoop(display)

	params, err := openh264.NewParams()
	if err != nil {
		return err
	}
	selector := mediadevices.NewCodecSelector(mediadevices.WithVideoEncoders(&params))
	source := &screenshotSource{display: display, closed: make(chan struct{})}
	track := mediadevices.NewVideoTrack(source, selector).(*mediadevices.VideoTrack)
	a.vt = track
	return nil
}

// httpFrameLoop captures at a steady rate and keeps the latest JPEG frame.
func (a *Agent) httpFrameLoop(display int) {
	ticker := time.NewTicker(90 * time.Millisecond)
	defer ticker.Stop()
	var lastErr string
	for range ticker.C {
		img, err := screenshot.CaptureDisplay(display)
		if err != nil {
			if err.Error() != lastErr {
				lastErr = err.Error()
				fmt.Println("capture:", err)
			}
			continue
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 72}); err != nil {
			continue
		}
		h := fnv.New64a()
		h.Write(buf.Bytes())
		a.mu.Lock()
		a.frameJPG = buf.Bytes()
		a.raw = img
		a.frameSeq++
		a.frameHash = h.Sum64()
		a.mu.Unlock()
	}
}
