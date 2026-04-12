package rclone

import (
	"bufio"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	stdsync "sync"
	"strings"
	"syscall"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// skipListMu serializes writes to the skip list file.
var skipListMu stdsync.Mutex

// Paths holds well-known file locations for sync artifacts.
type Paths struct {
	FilterFile     string
	SkipFile       string
	LogFile        string
	MountPoint     string
	LaunchdPlist   string
	SystemdService string
	SystemdTimer   string
}

// ResolvePaths returns standard sync artifact paths.
func ResolvePaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}
	rcloneDir := filepath.Join(home, ".config", "rclone")
	return &Paths{
		FilterFile:     filepath.Join(rcloneDir, "workspace-filter.txt"),
		SkipFile:       filepath.Join(rcloneDir, "workspace-skip.txt"),
		LogFile:        filepath.Join(home, ".local", "log", "rclone-bisync.log"),
		MountPoint:     filepath.Join(home, "gdrive-mount"),
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", "com.rclone.workspace-bisync.plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", "rclone-bisync.service"),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", "rclone-bisync.timer"),
	}, nil
}

// Config holds resolved sync parameters.
type Config struct {
	LocalPath  string
	RemotePath string
	FilterFile string
	LogFile    string
	RclonePath string
	Interval   int
	Verbose    bool
}

// ResolveConfig merges UserState fields with defaults.
func ResolveConfig(state *config.UserState) (*Config, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}

	localPath := state.Modules.Workspace.Path
	if localPath == "" {
		home, _ := os.UserHomeDir()
		localPath = filepath.Join(home, "ai-workspace")
	}
	if strings.HasPrefix(localPath, "~/") {
		home, _ := os.UserHomeDir()
		localPath = filepath.Join(home, localPath[2:])
	}

	remote := state.Modules.Sync.Remote
	if remote == "" {
		remote = "gdrive"
	}
	remotePath := state.Modules.Sync.Path
	if remotePath == "" {
		remotePath = "work"
	}

	// Clamp Interval to a sensible range (60s minimum, 24h maximum).
	interval := state.Modules.Sync.Interval
	if interval <= 0 {
		interval = 300
	} else if interval < 60 {
		interval = 60
	} else if interval > 86400 {
		interval = 86400
	}

	rclonePath, _ := osexec.LookPath("rclone")

	return &Config{
		LocalPath:  localPath,
		RemotePath: remote + ":" + remotePath,
		FilterFile: paths.FilterFile,
		LogFile:    paths.LogFile,
		RclonePath: rclonePath,
		Interval:   interval,
	}, nil
}

// ── rclone args ───────────────────────────────────────────────────────────

// driveArgs returns Google Drive-specific flags.
// --drive-skip-shared-with-me avoids files shared TO the user (read-only)
// which cause insufficientFilePermissions on upload.
func driveArgs() []string {
	return []string{
		"--drive-skip-dangling-shortcuts",
		"--drive-skip-gdocs",
		"--drive-skip-shared-with-me",
		"--drive-pacer-min-sleep", "10ms",
	}
}

// copyArgs returns standard args for rclone copy --update.
func copyArgs(cfg *Config, paths *Paths) []string {
	args := []string{
		"--filter-from", cfg.FilterFile,
		"--update",
		"--fast-list",
		"--tpslimit", "10",
		"--retries", "3",
		"--low-level-retries", "10",
		"--log-file", cfg.LogFile,
		"-v",
	}
	args = append(args, driveArgs()...)
	if paths != nil && paths.SkipFile != "" {
		if _, err := os.Stat(paths.SkipFile); err == nil {
			args = append(args, "--exclude-from", paths.SkipFile)
		}
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}
	return args
}

// ── pull / push / sync ────────────────────────────────────────────────────

// Pull downloads newer files from remote to local (rclone copy remote → local --update).
// Safe: never writes to remote, avoids all upload permission errors.
func Pull(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, dryRun bool) error {
	if err := ensureLogDir(runner, cfg.LogFile); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.FilterFile); err != nil {
		return fmt.Errorf("filter file missing: %s — run 'dot clone setup'", cfg.FilterFile)
	}

	args := append([]string{"copy", cfg.RemotePath, cfg.LocalPath}, copyArgs(cfg, paths)...)
	if dryRun {
		args = append(args, "--dry-run")
	}

	fmt.Printf("  Pull: %s → %s\n", cfg.RemotePath, cfg.LocalPath)
	runErr := runRclone(ctx, runner, cfg, args)

	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d path(s) added to skip list\n", added)
		}
	}
	if runErr != nil {
		return fmt.Errorf("pull: %w", runErr)
	}
	return nil
}

// Push uploads newer files from local to remote (rclone copy local → remote --update).
// Uses retries=1 because permission errors on shared files are permanent.
func Push(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, dryRun bool) error {
	if err := ensureLogDir(runner, cfg.LogFile); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.FilterFile); err != nil {
		return fmt.Errorf("filter file missing: %s — run 'dot clone setup'", cfg.FilterFile)
	}

	args := append([]string{"copy", cfg.LocalPath, cfg.RemotePath}, copyArgs(cfg, paths)...)
	args = append(args, "--retries", "1")
	if dryRun {
		args = append(args, "--dry-run")
	}

	fmt.Printf("  Push: %s → %s\n", cfg.LocalPath, cfg.RemotePath)
	runErr := runRclone(ctx, runner, cfg, args)

	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d path(s) added to skip list\n", added)
		}
	}
	if runErr != nil {
		return fmt.Errorf("push: %w", runErr)
	}
	return nil
}

// Sync runs Pull then Push (bidirectional).
// Continues to Push even if Pull fails, but returns the first error.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, dryRun bool) error {
	pullErr := Pull(ctx, runner, cfg, paths, dryRun)
	if pullErr != nil {
		fmt.Printf("  ⚠ %v — continuing to push\n", pullErr)
	}
	pushErr := Push(ctx, runner, cfg, paths, dryRun)
	if pullErr != nil {
		return pullErr
	}
	return pushErr
}

// Mount mounts the remote as a FUSE filesystem at paths.MountPoint.
// Runs in foreground (blocking) — use daemon mode or scheduler for persistence.
func Mount(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, daemon bool) error {
	if err := runner.MkdirAll(paths.MountPoint, 0755); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	args := []string{
		"mount",
		cfg.RemotePath,
		paths.MountPoint,
		"--vfs-cache-mode", "writes",
		"--vfs-cache-max-age", "24h",
		"--dir-cache-time", "30s",
		"--poll-interval", "15s",
	}
	args = append(args, driveArgs()...)
	if daemon {
		args = append(args, "--daemon")
	}

	fmt.Printf("Mounting %s at %s\n", cfg.RemotePath, paths.MountPoint)
	return runner.RunAttached(ctx, "rclone", args...)
}

// Unmount unmounts the FUSE filesystem.
func Unmount(ctx context.Context, runner *exec.Runner, paths *Paths) error {
	if runtime.GOOS == "darwin" {
		_, err := runner.Run(ctx, "umount", paths.MountPoint)
		return err
	}
	_, err := runner.Run(ctx, "fusermount", "-u", paths.MountPoint)
	return err
}

// IsMounted reports whether the mount point is currently a mount.
// Compares the device ID of the path with its parent — a mount boundary
// means different device IDs.
func IsMounted(mountPoint string) bool {
	info, err := os.Stat(mountPoint)
	if err != nil {
		return false
	}
	parentInfo, err := os.Stat(filepath.Dir(mountPoint))
	if err != nil {
		return false
	}
	mountStat, ok1 := info.Sys().(*syscall.Stat_t)
	parentStat, ok2 := parentInfo.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return mountStat.Dev != parentStat.Dev
}

// ── helpers ───────────────────────────────────────────────────────────────

func ensureLogDir(runner *exec.Runner, logFile string) error {
	if err := runner.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}
	return nil
}

func runRclone(ctx context.Context, runner *exec.Runner, cfg *Config, args []string) error {
	if cfg.Verbose {
		return runner.RunAttached(ctx, "rclone", args...)
	}
	_, err := runner.Run(ctx, "rclone", args...)
	return err
}

// ── skip list management ──────────────────────────────────────────────────

var (
	permErrorRegex    = regexp.MustCompile(`ERROR : (.+): Failed to (?:copy|update|set).*insufficientFilePermissions`)
	symlinkErrorRegex = regexp.MustCompile(`NOTICE: (.+): Can't follow symlink`)
	shortcutRegex     = regexp.MustCompile(`NOTICE: Dangling shortcut "(.+)" detected`)
	googleDocRegex    = regexp.MustCompile(`ERROR : (.+): Failed to copy: can't update google document`)
)

const skipFileHeader = "# Auto-generated by dot sync — files skipped due to sync errors\n# Clear with: dot sync skip clear\n"

// UpdateSkipList parses the log for sync errors and adds new paths to the skip list.
// Safe for concurrent callers via package-level mutex.
func UpdateSkipList(logFile, skipFile string) (int, error) {
	skipListMu.Lock()
	defer skipListMu.Unlock()

	newPaths := parseSyncErrors(logFile)
	if len(newPaths) == 0 {
		return 0, nil
	}

	existing := make(map[string]bool)
	if data, err := os.ReadFile(skipFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				existing[line[2:]] = true
			}
		}
	}

	added := 0
	for _, p := range newPaths {
		if !existing[p] {
			existing[p] = true
			added++
		}
	}

	if added == 0 {
		return 0, nil
	}

	var paths []string
	for p := range existing {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var sb strings.Builder
	sb.WriteString(skipFileHeader)
	for _, p := range paths {
		sb.WriteString("- " + p + "\n")
	}

	if err := os.MkdirAll(filepath.Dir(skipFile), 0755); err != nil {
		return 0, fmt.Errorf("creating skip file dir: %w", err)
	}
	if err := os.WriteFile(skipFile, []byte(sb.String()), 0644); err != nil {
		return 0, fmt.Errorf("writing skip file: %w", err)
	}
	return added, nil
}

func parseSyncErrors(logFile string) []string {
	f, err := os.Open(logFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]bool)
	var paths []string

	addPath := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := permErrorRegex.FindStringSubmatch(line); len(m) >= 2 {
			addPath(m[1])
		} else if m := symlinkErrorRegex.FindStringSubmatch(line); len(m) >= 2 {
			addPath(m[1])
		} else if m := shortcutRegex.FindStringSubmatch(line); len(m) >= 2 {
			addPath(m[1])
		} else if m := googleDocRegex.FindStringSubmatch(line); len(m) >= 2 {
			addPath(m[1])
		}
	}
	return paths
}

// LoadSkipList returns the list of skipped file paths.
func LoadSkipList(skipFile string) ([]string, error) {
	data, err := os.ReadFile(skipFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			paths = append(paths, line[2:])
		}
	}
	return paths, nil
}

// ClearSkipList removes the skip list file.
func ClearSkipList(paths *Paths) error {
	if err := os.Remove(paths.SkipFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsDarwin reports whether we're on macOS.
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}
