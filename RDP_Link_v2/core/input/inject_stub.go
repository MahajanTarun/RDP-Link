//go:build !cgo

package input

import "fmt"

func Inject(ev map[string]any) error {
	return fmt.Errorf("input injection unavailable (built without cgo)")
}

// PermissionError reports why injection is unavailable in a cgo-less build.
func PermissionError() error {
	return fmt.Errorf("input injection unavailable (built without cgo)")
}
