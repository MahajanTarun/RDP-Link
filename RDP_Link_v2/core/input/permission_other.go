//go:build cgo && !darwin

package input

// PermissionError is always nil on platforms that do not gate input
// injection behind a user-granted permission.
func PermissionError() error { return nil }

func ensurePermission() error { return nil }
