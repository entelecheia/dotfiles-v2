package syncer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// Paths holds well-known file locations for sync artifacts.
type Paths struct {
	ConfigDir      string // ~/.config/dotfiles (or $XDG_CONFIG_HOME/dotfiles)
	ExcludesFile   string // <ConfigDir> legacy global excludes (superseded by the workspace store)
	LogFile        string // ~/.local/log/dotfiles-sync.log
	LockDir        string // ~/Library/Caches/dotfiles/sync.lock (macOS) or equivalent
	LaunchdPlist   string // ~/Library/LaunchAgents/com.dotfiles.sync.plist (macOS, push)
	SystemdService string // ~/.config/systemd/user/dotfiles-sync.service (Linux, push)
	SystemdTimer   string // ~/.config/systemd/user/dotfiles-sync.timer (Linux, push)
}

// PlistFor returns the launchd plist path for the given kind. The push
// variant is the historical LaunchdPlist value; the intake variant is
// derived by name in the same directory.
func (p *Paths) PlistFor(kind SchedulerKind) string {
	if kind == SchedulerKindPush {
		return p.LaunchdPlist
	}
	dir := filepath.Dir(p.LaunchdPlist)
	return filepath.Join(dir, kind.LaunchdLabel()+".plist")
}

// SystemdServiceFor returns the systemd service unit path for the kind.
func (p *Paths) SystemdServiceFor(kind SchedulerKind) string {
	if kind == SchedulerKindPush {
		return p.SystemdService
	}
	dir := filepath.Dir(p.SystemdService)
	return filepath.Join(dir, kind.SystemdServiceName())
}

// SystemdTimerFor returns the systemd timer unit path for the kind.
func (p *Paths) SystemdTimerFor(kind SchedulerKind) string {
	if kind == SchedulerKindPush {
		return p.SystemdTimer
	}
	dir := filepath.Dir(p.SystemdTimer)
	return filepath.Join(dir, kind.SystemdTimerName())
}

// ResolvePaths returns the standard gsync artifact paths for the
// current user. ConfigDir respects XDG_CONFIG_HOME; LockDir uses
// os.UserCacheDir so it stays out of the workspace tree.
func ResolvePaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// fall back to /tmp on platforms without UserCacheDir
		cacheDir = "/tmp"
	}
	return pathsFor(home, cacheDir), nil
}

// ResolvePathsForHome resolves gsync artifact paths against an explicit home
// directory. Commands that honor --home use it so per-user artifact and lock
// paths follow the target home rather than the invoking user's. An empty home
// falls back to ResolvePaths (current user).
func ResolvePathsForHome(home string) (*Paths, error) {
	if home == "" {
		return ResolvePaths()
	}
	return pathsFor(home, cacheDirForHome(home)), nil
}

// ResolvePathsForProfile builds the artifact layout for one sync profile.
// Scheduler units are per-profile, but the peer profile deliberately shares
// the default lock: peer and cloud runs can both mutate the same workspace
// tree, so they must never overlap. Other custom profiles keep independent
// locks.
//
// The default profile is unchanged by construction - "sync.lock" and
// "com.dotfiles.sync.plist" are exactly what the templated names produce when
// the profile is "sync".
func ResolvePathsForProfile(profile string) (*Paths, error) {
	p, err := ResolvePaths()
	if err != nil {
		return nil, err
	}
	return withProfile(p, profile), nil
}

// ResolvePathsForHomeProfile is ResolvePathsForProfile with an explicit home.
func ResolvePathsForHomeProfile(home, profile string) (*Paths, error) {
	p, err := ResolvePathsForHome(home)
	if err != nil {
		return nil, err
	}
	return withProfile(p, profile), nil
}

func withProfile(p *Paths, profile string) *Paths {
	prof := NormalizeProfile(profile)
	if prof == DefaultProfile {
		return p
	}
	out := *p
	if prof != PeerProfile {
		out.LockDir = filepath.Join(filepath.Dir(p.LockDir), prof+".lock")
	}
	out.LaunchdPlist = filepath.Join(filepath.Dir(p.LaunchdPlist), "com.dotfiles."+prof+".plist")
	out.SystemdService = filepath.Join(filepath.Dir(p.SystemdService), "dotfiles-"+prof+".service")
	out.SystemdTimer = filepath.Join(filepath.Dir(p.SystemdTimer), "dotfiles-"+prof+".timer")
	return &out
}

// pathsFor builds the artifact layout for a given home + cache dir.
func pathsFor(home, cacheDir string) *Paths {
	configDir := filepath.Join(home, ".config", "dotfiles")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configDir = filepath.Join(xdg, "dotfiles")
	}
	return &Paths{
		ConfigDir:      configDir,
		ExcludesFile:   filepath.Join(configDir, excludesDiskName),
		LogFile:        filepath.Join(home, ".local", "log", "dotfiles-sync.log"),
		LockDir:        filepath.Join(cacheDir, "dotfiles", "sync.lock"),
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", systemdServiceName),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", systemdTimerName),
	}
}

// cacheDirForHome mirrors os.UserCacheDir's layout for an explicit home so a
// --home target's lock lives under that home, not the invoking user's cache.
func cacheDirForHome(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches")
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return xdg
	}
	return filepath.Join(home, ".cache")
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// AcquireLock creates a POSIX-safe lock directory with a PID file inside.
// Returns a release function that removes the lock dir.
//
// On EEXIST, reads <lockDir>/lock.pid and probes the process with signal 0.
// If the PID is dead (ESRCH), removes the stale lock and retries once.
// Otherwise reports the lock is held by another running sync.
// The zero-value LockOptions is deliberate: it keeps the refusal wording and
// the one-hour pid-less horizon a sync lock has always had, so nothing an
// operator has already seen changes shape.
func AcquireLock(lockDir string) (func(), error) {
	return fileutil.AcquirePIDLock(lockDir, fileutil.LockOptions{})
}

// lockIsStale returns true when the PID file inside lockDir points at a
// process that no longer exists. A missing or unparseable PID file is
// treated as stale (defensive: better to break a held-but-corrupted lock
// than block forever).
func lockIsStale(lockDir string) bool {
	return fileutil.PIDLockIsStale(lockDir)
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
	return fileutil.TailLog(logFile, n)
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
