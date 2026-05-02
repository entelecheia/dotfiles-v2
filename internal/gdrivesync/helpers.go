package gdrivesync

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Paths holds well-known file locations for gdrive-sync artifacts.
type Paths struct {
	ConfigDir      string // ~/.config/dotfiles (or $XDG_CONFIG_HOME/dotfiles)
	ExcludesFile   string // <ConfigDir>/gdrive-sync-excludes.conf (materialized from embed)
	LogFile        string // ~/.local/log/dotfiles-gdrive-sync.log
	LockDir        string // ~/Library/Caches/dotfiles/gdrive-sync.lock (macOS) or equivalent
	LaunchdPlist   string // ~/Library/LaunchAgents/com.dotfiles.gdrive-sync.plist (macOS)
	SystemdService string // ~/.config/systemd/user/dotfiles-gdrive-sync.service (Linux)
	SystemdTimer   string // ~/.config/systemd/user/dotfiles-gdrive-sync.timer (Linux)
}

// ResolvePaths returns the standard gdrive-sync artifact paths for the
// current user. ConfigDir respects XDG_CONFIG_HOME; LockDir uses
// os.UserCacheDir so it stays out of the workspace tree.
func ResolvePaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}
	configDir := filepath.Join(home, ".config", "dotfiles")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configDir = filepath.Join(xdg, "dotfiles")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// fall back to /tmp on platforms without UserCacheDir
		cacheDir = "/tmp"
	}
	return &Paths{
		ConfigDir:      configDir,
		ExcludesFile:   filepath.Join(configDir, excludesDiskName),
		LogFile:        filepath.Join(home, ".local", "log", "dotfiles-gdrive-sync.log"),
		LockDir:        filepath.Join(cacheDir, "dotfiles", "gdrive-sync.lock"),
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.gdrive-sync.plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", "dotfiles-gdrive-sync.service"),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", "dotfiles-gdrive-sync.timer"),
	}, nil
}

// AcquireLock creates a POSIX-safe lock directory with a PID file inside.
// Returns a release function that removes the lock dir.
//
// On EEXIST, reads <lockDir>/lock.pid and probes the process with signal 0.
// If the PID is dead (ESRCH), removes the stale lock and retries once.
// Otherwise reports the lock is held by another running sync.
func AcquireLock(lockDir string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockDir), 0755); err != nil {
		return nil, fmt.Errorf("preparing lock parent: %w", err)
	}
	if err := os.Mkdir(lockDir, 0755); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating lock: %w", err)
		}
		if !lockIsStale(lockDir) {
			return nil, fmt.Errorf("another sync is running (lock: %s)", lockDir)
		}
		_ = os.RemoveAll(lockDir)
		if err := os.Mkdir(lockDir, 0755); err != nil {
			return nil, fmt.Errorf("recreating lock after stale cleanup: %w", err)
		}
	}
	if err := writeLockPID(lockDir); err != nil {
		_ = os.RemoveAll(lockDir)
		return nil, err
	}
	return func() { _ = os.RemoveAll(lockDir) }, nil
}

// lockIsStale returns true when the PID file inside lockDir points at a
// process that no longer exists. A missing or unparseable PID file is
// treated as stale (defensive: better to break a held-but-corrupted lock
// than block forever).
func lockIsStale(lockDir string) bool {
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	// Signal 0 probes existence without delivering; ESRCH = dead.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return true
	}
	return false
}

func writeLockPID(lockDir string) error {
	pidFile := filepath.Join(lockDir, "lock.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("writing lock pid: %w", err)
	}
	return nil
}

// ensureLogDir creates the parent directory of logFile if needed.
func ensureLogDir(logFile string) error {
	return os.MkdirAll(filepath.Dir(logFile), 0755)
}

// AppendLog writes one line to the log file describing a sync operation.
func AppendLog(logFile, op string, exitCode int) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s op=%s exit=%d\n", time.Now().Format("2006-01-02 15:04:05"), op, exitCode)
}

// RotateLog trims logFile if it exceeds maxLines, keeping the last keepLines.
func RotateLog(logFile string, maxLines, keepLines int) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= maxLines {
		return
	}
	if keepLines > len(lines) {
		keepLines = len(lines)
	}
	kept := lines[len(lines)-keepLines:]
	_ = os.WriteFile(logFile, []byte(strings.Join(kept, "\n")+"\n"), 0644)
}

// TailLog returns the last n lines of logFile as a single string.
func TailLog(logFile string, n int) (string, error) {
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// newTimestamp returns a filesystem-safe RFC3339 timestamp suitable for
// timestamped backup/conflict directories. Replaces ':' with '-' so it
// works on filesystems (e.g. SMB shares) that disallow colons in names.
func newTimestamp() string {
	return strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-")
}

// newSubSecondTimestamp is like newTimestamp but adds microsecond
// resolution. Used for intake run-dirs where two runs in the same
// wall-clock second is plausible (a successful intake completing fast
// enough that a follow-up run finds new content), and same-dir
// collisions would silently merge their staged files.
func newSubSecondTimestamp() string {
	t := time.Now().UTC()
	// Format: 2026-05-02T10-00-00.123456Z. Both ':' (none here) and the
	// fractional separator '.' are filesystem-safe across darwin/linux/
	// SMB; no replace needed.
	return t.Format("2006-01-02T15-04-05.000000Z")
}
