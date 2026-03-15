package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

const backupDir = ".local/share/dotfiles/backup"

// EnsureFile writes content to path if it differs from current content.
// Returns true if the file was written.
func EnsureFile(runner *exec.Runner, path string, content []byte, perm os.FileMode) (bool, error) {
	existing, err := runner.ReadFile(path)
	if err == nil && hashBytes(existing) == hashBytes(content) {
		return false, nil
	}

	// Backup existing file
	if err == nil {
		if backupErr := backup(runner, path); backupErr != nil {
			runner.Logger.Warn("backup failed", "path", path, "err", backupErr)
		}
	}

	// Ensure parent directory
	dir := filepath.Dir(path)
	if err := runner.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating directory %q: %w", dir, err)
	}

	if err := runner.WriteFile(path, content, perm); err != nil {
		return false, fmt.Errorf("writing %q: %w", path, err)
	}
	return true, nil
}

// NeedsUpdate checks if a file needs to be written.
func NeedsUpdate(runner *exec.Runner, path string, content []byte) bool {
	existing, err := runner.ReadFile(path)
	if err != nil {
		return true
	}
	return hashBytes(existing) != hashBytes(content)
}

// backup copies an existing file to the backup directory.
func backup(runner *exec.Runner, path string) error {
	home, _ := os.UserHomeDir()
	bdir := filepath.Join(home, backupDir)
	if err := runner.MkdirAll(bdir, 0755); err != nil {
		return err
	}

	base := filepath.Base(path)
	timestamp := time.Now().Format("20060102-150405")
	dest := filepath.Join(bdir, fmt.Sprintf("%s.%s", base, timestamp))

	data, err := runner.ReadFile(path)
	if err != nil {
		return err
	}
	return runner.WriteFile(dest, data, 0644)
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
