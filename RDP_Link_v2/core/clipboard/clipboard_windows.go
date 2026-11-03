//go:build windows && cgo

package clipboard

import (
	"strings"

	"github.com/go-vgo/robotgo"
)

// Read returns the Windows clipboard.
//
// The Win32 clipboard is used directly. Spawning powershell.exe for every
// read cost hundreds of milliseconds of CPU and flashed a console window.
func Read() string {
	text, err := robotgo.ReadAll()
	if err != nil {
		return ""
	}
	return text
}

// Write puts text on the Windows clipboard.
func Write(text string) error {
	return robotgo.WriteAll(text)
}

func Normalize(s string) string {
	return strings.TrimRight(s, "\n")
}
