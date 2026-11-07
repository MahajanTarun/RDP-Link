//go:build cgo

package input

import (
	"fmt"

	"github.com/go-vgo/robotgo"
)

var specialKeys = map[string]string{
	"enter":     "enter",
	"return":    "enter",
	"backspace": "backspace",
	"delete":    "delete",
	"escape":    "escape",
	"esc":       "escape",
	"tab":       "tab",
	"home":      "home",
	"end":       "end",
	"pageup":    "pageup",
	"pagedown":  "pagedown",
	"up":        "up",
	"down":      "down",
	"left":      "left",
	"right":     "right",
	"space":     "space",
}

func Inject(ev map[string]any) error {
	if err := ensurePermission(); err != nil {
		return err
	}
	t, _ := ev["type"].(string)
	switch t {
	case "move":
		x, _ := ev["x"].(float64)
		y, _ := ev["y"].(float64)
		robotgo.Move(int(x), int(y))
	case "click", "down", "up":
		x, _ := ev["x"].(float64)
		y, _ := ev["y"].(float64)
		robotgo.Move(int(x), int(y))
		button := "left"
		if b, ok := ev["button"].(float64); ok && b == 3 {
			button = "right"
		}
		switch t {
		case "down":
			// Keep the button held so a drag can move the pointer before
			// the matching "up" event.
			if err := robotgo.MouseDown(button); err != nil {
				return err
			}
		case "up":
			if err := robotgo.MouseUp(button); err != nil {
				return err
			}
		default:
			if err := robotgo.Click(button, false); err != nil {
				return err
			}
		}
	case "wheel":
		delta, _ := ev["delta"].(float64)
		direction := "up"
		if delta < 0 {
			direction = "down"
		}
		if err := robotgo.ScrollDir(3, direction); err != nil {
			return err
		}
	case "text":
		if s, ok := ev["text"].(string); ok && s != "" {
			robotgo.TypeStr(s)
		}
	case "key":
		s, _ := ev["key"].(string)
		if s == "" {
			return nil
		}
		if mapped, ok := specialKeys[s]; ok {
			return robotgo.KeyTap(mapped)
		}
		robotgo.TypeStr(s)
	default:
		return fmt.Errorf("unknown event %s", t)
	}
	return nil
}
