package rsync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Paths holds well-known file locations for rsync sync artifacts.
type Paths struct {
	ExtensionsFile string
	LogFile        string
	LockDir        string
	LaunchdPlist   string
	SystemdService string
	SystemdTimer   string
}

// ResolvePaths returns standard rsync sync artifact paths.
func ResolvePaths() (*Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home: %w", err)
	}
	dotfilesDir := filepath.Join(home, ".config", "dotfiles")
	return &Paths{
		ExtensionsFile: filepath.Join(dotfilesDir, "binary-extensions.conf"),
		LogFile:        filepath.Join(home, ".local", "log", "dotfiles-sync.log"),
		LockDir:        "/tmp/dotfiles-sync.lock",
		LaunchdPlist:   filepath.Join(home, "Library", "LaunchAgents", "com.dotfiles.workspace-sync.plist"),
		SystemdService: filepath.Join(home, ".config", "systemd", "user", "dotfiles-sync.service"),
		SystemdTimer:   filepath.Join(home, ".config", "systemd", "user", "dotfiles-sync.timer"),
	}, nil
}

// Config holds resolved rsync sync parameters.
type Config struct {
	LocalPath      string
	RemoteHost     string // user@host
	RemotePath     string // remote workspace path
	ExtensionsFile string
	LogFile        string
	LockDir        string
	RsyncPath      string
	Interval       int
	Verbose        bool
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
		localPath = filepath.Join(home, "workspace", "work")
	}
	if strings.HasPrefix(localPath, "~/") {
		home, _ := os.UserHomeDir()
		localPath = filepath.Join(home, localPath[2:])
	}
	// Ensure trailing slash for rsync
	if !strings.HasSuffix(localPath, "/") {
		localPath += "/"
	}

	remoteHost := state.Modules.Rsync.RemoteHost
	remotePath := state.Modules.Rsync.RemotePath
	if remotePath == "" {
		remotePath = "~/workspace/work/"
	}
	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	interval := state.Modules.Rsync.Interval
	if interval <= 0 {
		interval = 300
	} else if interval < 60 {
		interval = 60
	} else if interval > 86400 {
		interval = 86400
	}

	rsyncPath, _ := osexec.LookPath("rsync")

	return &Config{
		LocalPath:      localPath,
		RemoteHost:     remoteHost,
		RemotePath:     remotePath,
		ExtensionsFile: paths.ExtensionsFile,
		LogFile:        paths.LogFile,
		LockDir:        paths.LockDir,
		RsyncPath:      rsyncPath,
		Interval:       interval,
	}, nil
}

// ── rsync args ───────────────────────────────────────────────────────────

// commonArgs returns shared rsync flags for binary-only sync.
func commonArgs(cfg *Config) []string {
	return []string{
		"-az", "--partial",
		"--include-from=" + cfg.ExtensionsFile,
		// Directory excludes MUST come before --include=*/ to take effect.
		// rsync evaluates rules first-match-wins: if --include=*/ comes first,
		// .git/ and node_modules/ are included as directories before excludes fire.
		"--exclude=.git", "--exclude=.git/**",
		"--exclude=node_modules",
		"--exclude=.omc", "--exclude=.omx",
		"--exclude=.tmp.drive*",
		"--include=*/",
		"--exclude=*",
	}
}

// remoteSpec returns user@host:path for rsync.
func remoteSpec(cfg *Config) string {
	return cfg.RemoteHost + ":" + cfg.RemotePath
}

// ── lock ─────────────────────────────────────────────────────────────────

// AcquireLock creates a POSIX-safe lock directory.
// Returns a release function, or error if another sync is running.
func AcquireLock(lockDir string) (func(), error) {
	if err := os.Mkdir(lockDir, 0755); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another sync is running (lock: %s)", lockDir)
		}
		return nil, fmt.Errorf("creating lock: %w", err)
	}
	return func() { _ = os.Remove(lockDir) }, nil
}

// ── pull / push / sync ──────────────────────────────────────────────────

// Pull downloads newer binary files from remote (rsync --update).
// Safe: only copies files newer on remote, never overwrites newer local files.
func Pull(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}

	args := append(commonArgs(cfg), "--update")
	if dryRun {
		args = append(args, "--dry-run")
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}
	args = append(args, remoteSpec(cfg), cfg.LocalPath)

	fmt.Printf("  Pull: %s → %s\n", remoteSpec(cfg), cfg.LocalPath)
	return runRsync(ctx, runner, cfg, args)
}

// Push uploads binary files from local to remote (rsync --delete-after).
// Mac is authoritative: after full transfer, remote files not on Mac are deleted.
func Push(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}

	args := append(commonArgs(cfg), "--delete-after")
	if dryRun {
		args = append(args, "--dry-run")
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}
	args = append(args, cfg.LocalPath, remoteSpec(cfg))

	fmt.Printf("  Push: %s → %s\n", cfg.LocalPath, remoteSpec(cfg))
	return runRsync(ctx, runner, cfg, args)
}

// Sync runs Pull then Push (pull-then-push strategy).
// Pull first ensures remote-created files are safe before push's --delete-after.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	pullErr := Pull(ctx, runner, cfg, dryRun)
	if pullErr != nil {
		fmt.Printf("  ⚠ pull: %v — continuing to push\n", pullErr)
	}
	pushErr := Push(ctx, runner, cfg, dryRun)
	if pullErr != nil {
		return pullErr
	}
	return pushErr
}

// ── helpers ──────────────────────────────────────────────────────────────

func ensureLogDir(logFile string) error {
	return os.MkdirAll(filepath.Dir(logFile), 0755)
}

func runRsync(ctx context.Context, runner *exec.Runner, cfg *Config, args []string) error {
	if cfg.Verbose {
		return runner.RunAttached(ctx, "rsync", args...)
	}
	_, err := runner.Run(ctx, "rsync", args...)
	return err
}

// AppendLog writes a one-line sync result to the log file.
func AppendLog(logFile string, pullExit, pushExit int) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s pull=%d push=%d\n",
		time.Now().Format("2006-01-02 15:04:05"),
		pullExit, pushExit)
}

// RotateLog trims the log file if it exceeds maxLines, keeping the last keepLines.
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
