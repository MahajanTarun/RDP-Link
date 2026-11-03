//go:build !cgo

package input

import "fmt"

func Inject(ev map[string]any) error {
	return fmt.Errorf("input injection unavailable (built without cgo)")
}
