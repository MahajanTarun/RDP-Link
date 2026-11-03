package host

import (
	"image"
	"image/color"
	"image/draw"
	"time"
)

// testPatternFrame renders a deterministic, slowly moving pattern. It is used
// by --test-pattern so the HTTP/JPEG/ETag and viewer paths can be exercised on
// machines without a capturable desktop (CI, Wine, headless runners).
func testPatternFrame(now time.Time) *image.RGBA {
	const width, height = 1280, 800
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	phase := uint8((now.UnixMilli() / 20) % 256)
	draw.Draw(img, img.Bounds(),
		&image.Uniform{color.RGBA{R: 16, G: 32 + phase/3, B: 64 + phase/2, A: 255}},
		image.Point{}, draw.Src)

	// A moving block makes each frame differ, so ETags change while the
	// pattern is streamed.
	x := int(now.UnixMilli()/8) % (width + 240)
	draw.Draw(img, image.Rect(x-240, height/2-90, x, height/2+90),
		&image.Uniform{color.RGBA{R: 240, G: 240, B: 245, A: 255}},
		image.Point{}, draw.Src)

	// Corner markers and a centre line help confirm orientation and scaling
	// in the viewer.
	accent := color.RGBA{R: 56, G: 189, B: 248, A: 255}
	draw.Draw(img, image.Rect(0, 0, width, 6), &image.Uniform{accent}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, height-6, width, height), &image.Uniform{accent}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(width/2-1, 0, width/2+1, height), &image.Uniform{accent}, image.Point{}, draw.Src)
	return img
}
