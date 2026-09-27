package config

import (
	"os"
	"path/filepath"
)

// ConfigHome resolves the XDG configuration root through StateDir, the single
// environment authority. Explicit and other-user homes never inherit ambient
// XDG configuration. Relative roots are preserved so callers can reject them.
func ConfigHome(home string, explicit bool) string {
	actual, _ := os.UserHomeDir()
	if !explicit && filepath.Clean(home) == filepath.Clean(actual) {
		return filepath.Dir(StateDir())
	}
	return filepath.Dir(StateDirForHome(home))
}
