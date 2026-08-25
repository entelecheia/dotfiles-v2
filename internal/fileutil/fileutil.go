package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

const backupDir = ".local/share/dotfiles/backup"

var (
	backupNow       = time.Now
	writeBackupFile = func(runner *exec.Runner, path string, data []byte, perm os.FileMode) error {
		return runner.WriteFile(path, data, perm)
	}
	writeReservedBackup = func(file *os.File, data []byte) (int, error) { return file.Write(data) }
	closeReservedBackup = func(file *os.File) error { return file.Close() }
	chmodBackupPath     = func(runner *exec.Runner, path string, mode os.FileMode) error { return runner.Chmod(path, mode) }
)

// EnsureFile writes content to path if it differs from current content.
// Returns true if the file was written.
func EnsureFile(runner *exec.Runner, home, path string, content []byte, perm os.FileMode) (bool, error) {
	existing, err := runner.ReadFile(path)
	if err == nil && hashBytes(existing) == hashBytes(content) {
		return false, nil
	}

	// Backup existing file
	if err == nil {
		if backupErr := backup(runner, home, path); backupErr != nil {
			return false, fmt.Errorf("backing up %q: %w", path, backupErr)
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

// EnsureFileAtomic is EnsureFile with a temp-and-rename write, for files that
// another process rewrites concurrently: a reader must never observe a torn
// file. Rename replaces a symlink at path instead of writing through it, so
// use it only on paths owned as regular files.
func EnsureFileAtomic(runner *exec.Runner, home, path string, content []byte, perm os.FileMode) (bool, error) {
	existing, err := runner.ReadFile(path)
	if err == nil && hashBytes(existing) == hashBytes(content) {
		return false, nil
	}

	if err == nil {
		if backupErr := backup(runner, home, path); backupErr != nil {
			return false, fmt.Errorf("backing up %q: %w", path, backupErr)
		}
	}

	dir := filepath.Dir(path)
	if err := runner.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("creating directory %q: %w", dir, err)
	}

	if err := runner.WriteFileAtomic(path, content, perm); err != nil {
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

// backup copies an existing file to the backup directory under home.
//
// The root is the home the CALLER resolved, not the one the process happens
// to run under: reading it from the environment sent every backup taken
// during a run pointed at another home into the invoking user's own tree
// (BUG-15). An empty home is refused rather than joined, since a relative
// join would write into whatever directory the operator was standing in.
func backup(runner *exec.Runner, home, path string) error {
	if home == "" {
		return fmt.Errorf("backing up %q: no home directory given", path)
	}
	bdir := filepath.Join(home, backupDir)
	if err := runner.MkdirAll(filepath.Dir(bdir), 0o755); err != nil {
		return err
	}
	if err := runner.MkdirAll(bdir, 0o700); err != nil {
		return err
	}
	if !runner.DryRun {
		info, err := os.Lstat(bdir)
		if err != nil {
			return fmt.Errorf("inspecting backup directory %q: %w", bdir, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup path %q must be a real directory", bdir)
		}
	}
	if err := runner.Chmod(bdir, 0o700); err != nil {
		return fmt.Errorf("restricting backup directory %q: %w", bdir, err)
	}

	base := filepath.Base(path)
	data, err := runner.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = writeBackupCopy(runner, bdir, base, data)
	return err
}

// writeBackupCopy preserves the existing seconds-format backup spelling.
func writeBackupCopy(runner *exec.Runner, bdir, base string, data []byte) (string, error) {
	name := fmt.Sprintf("%s.%s", base, backupNow().Format("20060102-150405"))
	if runner.DryRun {
		dest := filepath.Join(bdir, name)
		if err := writeBackupFile(runner, dest, data, 0o600); err != nil {
			return "", err
		}
		return dest, nil
	}

	for suffix := 0; ; suffix++ {
		candidate := name
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", name, suffix)
		}
		dest := filepath.Join(bdir, candidate)
		file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("reserving %s: %w", dest, err)
		}

		written, writeErr := writeReservedBackup(file, data)
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			_ = file.Close()
			_ = os.Remove(dest)
			return "", fmt.Errorf("writing %s: %w", dest, writeErr)
		}
		if err := closeReservedBackup(file); err != nil {
			_ = os.Remove(dest)
			return "", fmt.Errorf("closing %s: %w", dest, err)
		}
		return dest, nil
	}
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
