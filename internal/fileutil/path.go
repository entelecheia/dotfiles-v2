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

// ExpandHome replaces a leading "~/" with the current user's home directory.
// Returns the input unchanged if it doesn't start with "~/".
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
