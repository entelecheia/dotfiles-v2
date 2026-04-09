package sync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// timeNowUTC returns the current time in UTC (extracted for testability).
var timeNowUTC = func() time.Time { return time.Now().UTC() }

// Paths holds well-known file locations for sync artifacts.
type Paths struct {
	FilterFile     string
	LogFile        string
	LaunchdPlist   string
	SystemdService string
	SystemdTimer   string
	BisyncCache    string
}

// ResolvePaths returns standard sync artifact paths.
func ResolvePaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}

	// bisync cache: macOS uses ~/Library/Caches, Linux uses ~/.cache
	cacheDir := filepath.Join(home, ".cache", "rclone", "bisync")
	if runtime.GOOS == "darwin" {
		cacheDir = filepath.Join(home, "Library", "Caches", "rclone", "bisync")
	}

	return &Paths{
		FilterFile:     filepath.Join(home, ".config", "rclone", "workspace-filter.txt"),
		LogFile:        filepath.Join(home, ".local", "log", "rclone-bisync.log"),
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", "com.rclone.workspace-bisync.plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", "rclone-bisync.service"),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", "rclone-bisync.timer"),
		BisyncCache:    cacheDir,
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
	// Do NOT EvalSymlinks — hangs on unresponsive Google Drive FUSE mounts.

	remote := state.Modules.Sync.Remote
	if remote == "" {
		remote = "gdrive"
	}
	remotePath := state.Modules.Sync.Path
	if remotePath == "" {
		remotePath = "work"
	}

	interval := state.Modules.Sync.Interval
	if interval <= 0 {
		interval = 300
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

// bisyncArgs returns the standard rclone bisync arguments.
func bisyncArgs(cfg *Config) []string {
	return []string{
		"bisync",
		cfg.LocalPath,
		cfg.RemotePath,
		"--filter-from", cfg.FilterFile,
		"--conflict-resolve", "newer",
		"--conflict-loser", "num",
		"--resilient",
		"--recover",
		"--max-lock", "15m",
		"--tpslimit", "10",
		"--retries", "5",
		"--fast-list",
		"--drive-skip-dangling-shortcuts",
		"--log-file", cfg.LogFile,
		"-v",
	}
}

// Bisync runs rclone bisync with standard flags.
func Bisync(ctx context.Context, runner *exec.Runner, cfg *Config, resync, dryRun bool) error {
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	args := bisyncArgs(cfg)

	if resync {
		// --no-update-modtime: avoids insufficientFilePermissions on shared files
		// --ignore-errors: allows completion despite non-fatal errors
		args = append(args, "--resync", "--resync-mode", "path1", "--ignore-errors", "--no-update-modtime")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}

	result, err := runner.Run(ctx, "rclone", args...)
	if err != nil && !resync {
		// Check stderr and log for recoverable errors
		combined := ""
		if result != nil {
			combined = result.Stderr
		}
		if logContent, lerr := os.ReadFile(cfg.LogFile); lerr == nil {
			combined += string(logContent)
		}

		if strings.Contains(combined, "cannot find prior") {
			return fmt.Errorf("no sync baseline found — run 'dot sync reset' to create one")
		}
		if strings.Contains(combined, "out of sync") {
			return fmt.Errorf("paths are out of sync — baseline needs refresh")
		}
	}
	return err
}

// HasBaseline checks if bisync baseline listing files exist.
func HasBaseline(paths *Paths, cfg *Config) bool {
	prefix := baselinePrefix(cfg)
	p1 := filepath.Join(paths.BisyncCache, prefix+".path1.lst")
	p2 := filepath.Join(paths.BisyncCache, prefix+".path2.lst")
	_, err1 := os.Stat(p1)
	_, err2 := os.Stat(p2)
	return err1 == nil && err2 == nil
}

// CreateBaseline generates bisync baseline listing files without --resync.
// This avoids the Google Drive permission/quota errors that plague --resync
// on workspaces with shared files.
//
// Uses rclone lsf to get file info, then formats it into bisync v1 listing
// format. This is safe because it only reads — no writes, no SetModTime.
func CreateBaseline(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths) error {
	prefix := baselinePrefix(cfg)
	cacheDir := paths.BisyncCache

	if err := runner.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("creating bisync cache dir: %w", err)
	}

	p1 := filepath.Join(cacheDir, prefix+".path1.lst")
	p2 := filepath.Join(cacheDir, prefix+".path2.lst")
	lockFile := filepath.Join(cacheDir, prefix+".lck")

	// Step 1: generate local listing in bisync v1 format
	fmt.Println("  Generating local file listing...")
	localCount, err := generateListing(ctx, runner, cfg.LocalPath, cfg.FilterFile, p1, nil)
	if err != nil {
		return fmt.Errorf("listing local path: %w", err)
	}
	fmt.Printf("  ✓ Local: %d files\n", localCount)

	// Step 2: generate remote listing in bisync v1 format
	fmt.Println("  Generating remote file listing...")
	extraArgs := []string{"--fast-list", "--drive-skip-dangling-shortcuts", "--tpslimit", "10", "--retries", "5"}
	remoteCount, err := generateListing(ctx, runner, cfg.RemotePath, cfg.FilterFile, p2, extraArgs)
	if err != nil {
		return fmt.Errorf("listing remote path: %w", err)
	}
	fmt.Printf("  ✓ Remote: %d files\n", remoteCount)

	// Step 3: remove stale lock file
	if runner.FileExists(lockFile) {
		_ = runner.Remove(lockFile)
	}

	fmt.Printf("  ✓ Baseline created (%d local, %d remote)\n", localCount, remoteCount)
	return nil
}

// generateListing creates a bisync v1 listing file from rclone lsf output.
func generateListing(ctx context.Context, runner *exec.Runner, remotePath, filterFile, outPath string, extraArgs []string) (int, error) {
	// rclone lsf -R --format "spt" --separator ";" --files-only
	// gives: size;path;ISO8601_time
	args := []string{
		"lsf", "-R",
		"--format", "spt",
		"--separator", ";",
		"--files-only",
		"--time-format", "2006-01-02T15:04:05.000000000-0700",
		"--filter-from", filterFile,
		remotePath,
	}
	args = append(args, extraArgs...)

	result, err := runner.Run(ctx, "rclone", args...)
	if err != nil {
		return 0, err
	}

	// Convert to bisync v1 format:
	// # bisync listing v1 from <timestamp>
	// - <size> - - <time> "<path>"
	now := fmt.Sprintf("# bisync listing v1 from %s\n",
		strings.ReplaceAll(fmt.Sprintf("%s", timeNowUTC().Format("2006-01-02T15:04:05.000000000-0700")), "+", "+"))

	var sb strings.Builder
	sb.WriteString(now)

	count := 0
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ";", 3)
		if len(parts) != 3 {
			continue
		}
		size := strings.TrimSpace(parts[0])
		path := parts[1]
		modtime := parts[2]

		// bisync v1 format: - <size_padded> - - <modtime_ISO8601> "<path>"
		sb.WriteString(fmt.Sprintf("- %s - - %s %q\n", padSize(size), modtime, path))
		count++
	}

	if err := runner.WriteFile(outPath, []byte(sb.String()), 0644); err != nil {
		return 0, err
	}
	return count, nil
}

// padSize right-aligns a size string to 9 characters.
func padSize(s string) string {
	if len(s) >= 9 {
		return s
	}
	return strings.Repeat(" ", 9-len(s)) + s
}

// RemoveBaseline removes bisync baseline files and lock, forcing re-creation.
func RemoveBaseline(paths *Paths, cfg *Config) error {
	prefix := baselinePrefix(cfg)
	cacheDir := paths.BisyncCache

	for _, suffix := range []string{
		".path1.lst", ".path1.lst-new", ".path1.lst-old",
		".path2.lst", ".path2.lst-new", ".path2.lst-old",
		".lck",
	} {
		p := filepath.Join(cacheDir, prefix+suffix)
		if _, err := os.Stat(p); err == nil {
			os.Remove(p)
		}
	}
	return nil
}

// baselinePrefix returns the bisync cache file prefix for the given config.
// rclone uses a specific naming convention: path separators become underscores,
// colons are removed.
func baselinePrefix(cfg *Config) string {
	p1 := strings.ReplaceAll(cfg.LocalPath, "/", "_")
	p1 = strings.TrimPrefix(p1, "_")

	p2 := strings.ReplaceAll(cfg.RemotePath, ":", "_")
	p2 = strings.ReplaceAll(p2, "/", "_")

	return p1 + ".." + p2
}
