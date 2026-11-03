package host

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func auditLog(event string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "office-rdp")
	os.MkdirAll(dir, 0o700)
	// #nosec G304 -- fixed per-user audit log path.
	f, err := os.OpenFile(filepath.Join(dir, "audit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), event)
}
