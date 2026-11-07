package input

import "errors"

// ErrPermissionDenied is returned when the operating system has not granted
// the permission needed to inject input (macOS Accessibility, for example).
var ErrPermissionDenied = errors.New("accessibility permission required")
