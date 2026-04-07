package sync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
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
}

// ResolveConfig merges UserState fields with defaults.
func ResolveConfig(state *config.UserState) (*Config, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}

	// Local path: sync state -> workspace path -> default
	localPath := state.Modules.Workspace.Path
	if localPath == "" {
		home, _ := os.UserHomeDir()
		localPath = filepath.Join(home, "ai-workspace")
	}
	if strings.HasPrefix(localPath, "~/") {
		home, _ := os.UserHomeDir()
		localPath = filepath.Join(home, localPath[2:])
	}
	// Resolve symlinks (Google Drive FUSE)
	if resolved, err := filepath.EvalSymlinks(localPath); err == nil {
		localPath = resolved
	}

	// Remote: sync.remote -> "gdrive", sync.path -> "work"
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

// Bisync runs rclone bisync with standard flags.
func Bisync(ctx context.Context, runner *exec.Runner, cfg *Config, resync, dryRun bool) error {
	// Ensure log directory exists
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	args := []string{
		"bisync",
		cfg.LocalPath,
		cfg.RemotePath,
		"--filter-from", cfg.FilterFile,
		"--conflict-resolve", "newer",
		"--conflict-loser", "num",
		"--resilient",
		"--recover",
		"--max-lock", "5m",
		"--tpslimit", "10",
		"--log-file", cfg.LogFile,
		"--log-level", "INFO",
		"-v",
	}

	if resync {
		args = append(args, "--resync", "--resync-mode", "path1")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}

	_, err := runner.Run(ctx, "rclone", args...)
	return err
}
