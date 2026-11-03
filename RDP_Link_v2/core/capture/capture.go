// Package capture defines platform capture backend interfaces.
package capture

import "image"

// Frame is a captured desktop frame.
type Frame struct {
	Width  int
	Height int
	Image  *image.RGBA
	TS     int64
}

// Capturer captures desktop frames.
type Capturer interface {
	Start() error
	Stop() error
	Frames() <-chan Frame
	Backend() string
}

// New returns a platform-appropriate capturer (stub).
func New() Capturer { return &stubCapturer{backend: "stub"} }

type stubCapturer struct {
	backend string
	frames  chan Frame
}

func (s *stubCapturer) Start() error { s.frames = make(chan Frame, 4); return nil }
func (s *stubCapturer) Stop() error {
	if s.frames != nil {
		close(s.frames)
	}
	return nil
}
func (s *stubCapturer) Frames() <-chan Frame { return s.frames }
func (s *stubCapturer) Backend() string      { return s.backend }
