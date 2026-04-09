package sync

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
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Paths holds well-known file locations for sync artifacts.
type Paths struct {
	FilterFile     string
	SkipFile       string
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
		SkipFile:       filepath.Join(home, ".config", "rclone", "workspace-skip.txt"),
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
func commonArgs(cfg *Config, paths *Paths) []string {
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
	// Exclude files that previously failed with permission errors
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

// Sync performs bidirectional sync using rclone copy --update.
//
// Strategy:
//  1. Download: remote → local (retries=5 for transient errors)
//  2. Update skip list from permission errors in log
//  3. Upload: local → remote (retries=1, skip list applied)
//  4. Update skip list again from upload errors
//
// Permission errors are permanent (shared files), so retrying wastes
// API quota. The skip list prevents future retries.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, dryRun bool) error {
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	baseArgs := commonArgs(cfg, paths)
	baseArgs = append(baseArgs, "--update")
	if dryRun {
		baseArgs = append(baseArgs, "--dry-run")
	}

	run := func(src, dst, label string, extraArgs []string) error {
		fmt.Printf("  %s: %s → %s\n", label, src, dst)
		cmdArgs := append([]string{"copy", src, dst}, baseArgs...)
		cmdArgs = append(cmdArgs, extraArgs...)
		if cfg.Verbose {
			return runner.RunAttached(ctx, "rclone", cmdArgs...)
		}
		_, err := runner.Run(ctx, "rclone", cmdArgs...)
		return err
	}

	// Step 1: download (remote → local) — retries OK for transient errors
	if err := run(cfg.RemotePath, cfg.LocalPath, "Download", nil); err != nil {
		fmt.Printf("  ⚠ Download errors (non-fatal): %v\n", err)
	}

	// Step 2: update skip list from download errors
	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d file(s) added to skip list\n", added)
		}
	}

	// Step 3: upload (local → remote) — retries=1 (permission errors are permanent)
	if err := run(cfg.LocalPath, cfg.RemotePath, "Upload", []string{"--retries", "1"}); err != nil {
		fmt.Printf("  ⚠ Upload errors (non-fatal): %v\n", err)
	}

	// Step 4: update skip list from upload errors
	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d file(s) added to skip list\n", added)
		}
	}

	return nil
}

// IsDarwin reports whether we're on macOS.
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}

// ── skip list management ──────────────────────────────────────────────────

var permErrorRegex = regexp.MustCompile(`ERROR : (.+): Failed to (?:copy|update|set).*insufficientFilePermissions`)

const skipFileHeader = "# Auto-generated by dot sync — files skipped due to permission errors\n# Clear with: dot sync skip clear\n"

// UpdateSkipList parses the log for permission errors and adds new paths to the skip list.
// Returns the number of newly added entries.
func UpdateSkipList(logFile, skipFile string) (int, error) {
	// Parse log for permission errors
	newPaths := parsePermissionErrors(logFile)
	if len(newPaths) == 0 {
		return 0, nil
	}

	// Load existing skip list
	existing := make(map[string]bool)
	if data, err := os.ReadFile(skipFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				existing[line[2:]] = true
			}
		}
	}

	// Merge new paths
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

	// Write sorted skip list
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

// parsePermissionErrors extracts file paths from permission error lines in the log.
func parsePermissionErrors(logFile string) []string {
	f, err := os.Open(logFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := make(map[string]bool)
	var paths []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		matches := permErrorRegex.FindStringSubmatch(scanner.Text())
		if len(matches) >= 2 {
			p := strings.TrimSpace(matches[1])
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
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
func ClearSkipList(skipFile string) error {
	if err := os.Remove(skipFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
