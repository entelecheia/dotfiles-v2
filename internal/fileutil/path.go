package fileutil

import (
	"os"
	"path/filepath"
	"strings"
)

// Exists reports whether path is present on disk (file or directory).
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir reports whether path exists and is a directory.
// Returns false for symlinks that don't resolve, missing paths, and files.
func IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// ExpandHomeFor replaces a leading "~" or "~/" with home. Returns the input
// unchanged if it doesn't start with either form.
//
// A run that targets a home other than the process's own (--home) must use
// this rather than ExpandHome: reading the process environment is how vault
// detection came to stat the invoking user's tree no matter which home the
// run was pointed at (BUG-20).
func ExpandHomeFor(path, home string) string {
	// Keep bare "~" consistent with the home-aware AI settings expanders.
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// ExpandHome expands a leading "~/" against the process home. Correct for
// code that acts as the invoking user (the interactive wizard); everything
// resolving a path for another home wants ExpandHomeFor.
func ExpandHome(path string) string {
	home, _ := os.UserHomeDir()
	return ExpandHomeFor(path, home)
}
