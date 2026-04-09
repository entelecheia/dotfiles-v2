package sync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Paths holds well-known file locations for sync artifacts.
type Paths struct {
	FilterFile     string
	LogFile        string
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
	return &Paths{
		FilterFile:     filepath.Join(home, ".config", "rclone", "workspace-filter.txt"),
		LogFile:        filepath.Join(home, ".local", "log", "rclone-bisync.log"),
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

// commonArgs returns rclone flags shared by all sync operations.
func commonArgs(cfg *Config) []string {
	args := []string{
		"--filter-from", cfg.FilterFile,
		"--fast-list",
		"--drive-skip-dangling-shortcuts",
		"--drive-skip-gdocs",
		"--tpslimit", "10",
		"--retries", "5",
		"--low-level-retries", "10",
		"--log-file", cfg.LogFile,
		"-v",
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}
	return args
}

// Sync performs bidirectional sync using rclone copy --update.
// This is more robust than bisync for Google Drive workspaces with
// shared files, Google Docs, and permission restrictions.
//
// Strategy:
//  1. rclone copy remote → local --update (download newer/missing files)
//  2. rclone copy local → remote --update (upload newer/missing files)
//
// --update ensures newer files win. Permission errors on individual
// files are logged but don't abort the entire sync.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	args := commonArgs(cfg)
	args = append(args, "--update")
	if dryRun {
		args = append(args, "--dry-run")
	}

	run := func(src, dst string, label string) error {
		fmt.Printf("  %s: %s → %s\n", label, src, dst)
		cmdArgs := append([]string{"copy", src, dst}, args...)
		if cfg.Verbose {
			return runner.RunAttached(ctx, "rclone", cmdArgs...)
		}
		_, err := runner.Run(ctx, "rclone", cmdArgs...)
		return err
	}

	// Step 1: download (remote → local)
	if err := run(cfg.RemotePath, cfg.LocalPath, "Download"); err != nil {
		fmt.Printf("  ⚠ Download errors (non-fatal): %v\n", err)
	}

	// Step 2: upload (local → remote)
	if err := run(cfg.LocalPath, cfg.RemotePath, "Upload"); err != nil {
		fmt.Printf("  ⚠ Upload errors (non-fatal): %v\n", err)
	}

	return nil
}

// IsDarwin reports whether we're on macOS.
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}
