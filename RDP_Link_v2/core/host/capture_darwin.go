//go:build cgo && darwin

package host

/*
#cgo CFLAGS: -mmacosx-version-min=14.0 -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -mmacosx-version-min=14.0
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

import (
	"errors"
	"image"
	"unsafe"

	"github.com/kbinani/screenshot"
)

// hasLegacyCapture is true when captureLegacy has a real implementation.
const hasLegacyCapture = true

// activeDisplayIDs returns every active display, primary first.
func activeDisplayIDs() []C.CGDirectDisplayID {
	var count C.uint32_t
	if C.CGGetActiveDisplayList(0, nil, &count) != C.kCGErrorSuccess || count == 0 {
		return nil
	}
	ids := make([]C.CGDirectDisplayID, int(count))
	if C.CGGetActiveDisplayList(count,
		(*C.CGDirectDisplayID)(unsafe.Pointer(&ids[0])), nil) != C.kCGErrorSuccess {
		return nil
	}
	return ids
}

// legacyDisplayID maps the same monitor indexes as the screenshot package.
func legacyDisplayID(index int) C.CGDirectDisplayID {
	main := C.CGMainDisplayID()
	if index <= 0 {
		return main
	}
	n := 0
	for _, id := range activeDisplayIDs() {
		if id == main {
			continue
		}
		n++
		if n == index {
			return id
		}
	}
	return main
}

// captureLegacy captures a display synchronously with CoreGraphics. It is the
// fallback for the ScreenCaptureKit path used by github.com/kbinani/screenshot.
// The bitmap is rendered at the display's logical size so viewer coordinates
// map 1:1 to the input coordinates used by robotgo.
func captureLegacy(display int) (*image.RGBA, error) {
	id := legacyDisplayID(display)
	cgImage := C.CGDisplayCreateImage(id)
	if uintptr(cgImage) == 0 {
		return nil, errors.New("CoreGraphics capture failed (screen recording permission?)")
	}
	defer C.CGImageRelease(cgImage)

	width := int(C.CGImageGetWidth(cgImage))
	height := int(C.CGImageGetHeight(cgImage))
	if b := screenshot.GetDisplayBounds(display); b.Dx() > 0 && b.Dy() > 0 {
		width, height = b.Dx(), b.Dy()
	}
	if width <= 0 || height <= 0 {
		return nil, errors.New("CoreGraphics returned an empty image")
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	colorSpace := C.CGColorSpaceCreateWithName(C.kCGColorSpaceSRGB)
	if uintptr(colorSpace) == 0 {
		return nil, errors.New("cannot create colour space")
	}
	defer C.CGColorSpaceRelease(colorSpace)

	ctx := C.CGBitmapContextCreate(
		unsafe.Pointer(&img.Pix[0]),
		C.size_t(width),
		C.size_t(height),
		8,
		C.size_t(img.Stride),
		colorSpace,
		C.kCGImageAlphaNoneSkipFirst,
	)
	if uintptr(ctx) == 0 {
		return nil, errors.New("cannot create bitmap context")
	}
	defer C.CGContextRelease(ctx)
	C.CGContextDrawImage(ctx,
		C.CGRectMake(0, 0, C.CGFloat(width), C.CGFloat(height)), cgImage)

	// CGBitmapContext writes BGRA in little-endian memory. Swap to RGBA and
	// force an opaque alpha channel, matching the screenshot package.
	for y := 0; y < height; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			i := x * 4
			row[i], row[i+1], row[i+2], row[i+3] = row[i+1], row[i+2], row[i+3], 255
		}
	}
	return img, nil
}
