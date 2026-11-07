//go:build cgo && darwin

package input

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
*/
import "C"

// PermissionError reports whether this process may inject synthetic input.
// macOS requires the user to grant Accessibility to the app bundle.
func PermissionError() error {
	if C.AXIsProcessTrusted() == 0 {
		return ErrPermissionDenied
	}
	return nil
}

func ensurePermission() error { return PermissionError() }
