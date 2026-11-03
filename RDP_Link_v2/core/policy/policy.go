// Package policy evaluates per-session permissions.
package policy

type SessionPolicy struct {
	Input      string `json:"input"` // allow|ask|deny
	Clipboard  bool   `json:"clipboard"`
	Files      string `json:"files"` // allow|ask|deny
	Camera     bool   `json:"camera"`
	Mic        bool   `json:"mic"`
	Unattended bool   `json:"unattended"`
}

func Default() SessionPolicy {
	return SessionPolicy{Input: "ask", Clipboard: true, Files: "ask", Camera: false, Mic: false, Unattended: false}
}

func (p SessionPolicy) AllowInput() bool { return p.Input == "allow" }
func (p SessionPolicy) AllowFiles() bool { return p.Files == "allow" }
