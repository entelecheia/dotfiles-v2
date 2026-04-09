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
	FilterFile      string // static patterns (node_modules, etc.)
	SkipFile        string // dynamic skip list (permission errors, symlinks)
	BisyncFilter    string // combined filter for bisync (static + skip)
	LogFile         string
	LaunchdPlist    string
	SystemdService  string
	SystemdTimer    string
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
		BisyncFilter:   filepath.Join(rcloneDir, "workspace-bisync-filter.txt"),
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

// ── rclone args ───────────────────────────────────────────────────────────

// driveArgs returns Google Drive-specific flags.
func driveArgs() []string {
	return []string{
		"--drive-skip-dangling-shortcuts",
		"--drive-skip-gdocs",
		"--drive-pacer-min-sleep", "10ms",
	}
}

// commonCopyArgs returns args for rclone copy --update mode.
func commonCopyArgs(cfg *Config, paths *Paths) []string {
	args := []string{
		"--filter-from", cfg.FilterFile,
		"--fast-list",
		"--tpslimit", "10",
		"--low-level-retries", "10",
		"--log-file", cfg.LogFile,
		"-v",
	}
	args = append(args, driveArgs()...)
	// Exclude known-bad files
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

// ── copy-based sync ───────────────────────────────────────────────────────

// Sync performs bidirectional sync using rclone copy --update.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, dryRun bool) error {
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	baseArgs := commonCopyArgs(cfg, paths)
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

	// Download (remote → local) — retries=5 for transient errors
	if err := run(cfg.RemotePath, cfg.LocalPath, "Download", nil); err != nil {
		fmt.Printf("  ⚠ Download errors (non-fatal): %v\n", err)
	}

	// Update skip list between phases
	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d path(s) added to skip list\n", added)
		}
	}

	// Upload (local → remote) — retries=1 (permission errors are permanent)
	if err := run(cfg.LocalPath, cfg.RemotePath, "Upload", []string{"--retries", "1"}); err != nil {
		fmt.Printf("  ⚠ Upload errors (non-fatal): %v\n", err)
	}

	// Final skip list update
	if paths != nil && !dryRun {
		if added, _ := UpdateSkipList(cfg.LogFile, paths.SkipFile); added > 0 {
			fmt.Printf("  + %d path(s) added to skip list\n", added)
		}
	}

	return nil
}

// ── bisync ────────────────────────────────────────────────────────────────

// Bisync runs rclone bisync with combined filter file.
// Uses --filters-file (bisync-specific, MD5-tracked) for safe filter management.
func Bisync(ctx context.Context, runner *exec.Runner, cfg *Config, paths *Paths, resync, dryRun bool) error {
	logDir := filepath.Dir(cfg.LogFile)
	if err := runner.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("creating log dir: %w", err)
	}

	// Generate combined filter file (static patterns + skip list)
	if err := GenerateBisyncFilter(paths); err != nil {
		return fmt.Errorf("generating bisync filter: %w", err)
	}

	args := []string{
		"bisync",
		cfg.LocalPath,
		cfg.RemotePath,
		"--filters-file", paths.BisyncFilter,
		"--fast-list",
		"--tpslimit", "10",
		"--retries", "3",
		"--low-level-retries", "10",
		"--conflict-resolve", "newer",
		"--conflict-loser", "num",
		"--resilient",
		"--recover",
		"--max-lock", "2m",
		"--max-delete", "50",
		"--no-update-modtime",
		"--ignore-listing-checksum",
		"--check-sync", "false",
		"--log-file", cfg.LogFile,
		"-v",
	}
	args = append(args, driveArgs()...)

	if resync {
		args = append(args, "--resync", "--resync-mode", "path1")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}

	if cfg.Verbose {
		err := runner.RunAttached(ctx, "rclone", args...)
		return classifyBisyncError(err, cfg.LogFile, resync)
	}

	_, err := runner.Run(ctx, "rclone", args...)
	return classifyBisyncError(err, cfg.LogFile, resync)
}

// classifyBisyncError wraps bisync errors with actionable messages.
func classifyBisyncError(err error, logFile string, resync bool) error {
	if err == nil {
		return nil
	}
	if resync {
		return err
	}

	logContent := ""
	if data, lerr := os.ReadFile(logFile); lerr == nil {
		logContent = string(data)
	}

	if strings.Contains(logContent, "filters file has changed") ||
		strings.Contains(logContent, "md5") {
		return fmt.Errorf("filter changed — run 'dot sync --bisync --resync' to update baseline")
	}
	if strings.Contains(logContent, "cannot find prior") {
		return fmt.Errorf("no baseline — run 'dot sync --bisync --resync' to create one")
	}
	if strings.Contains(logContent, "out of sync") {
		return fmt.Errorf("paths out of sync — run 'dot sync --bisync --resync' to recover")
	}
	return err
}

// GenerateBisyncFilter creates the combined bisync filter file from
// the static filter and skip list. bisync tracks this file's MD5 and
// requires --resync when it changes, which correctly handles skip list updates.
func GenerateBisyncFilter(paths *Paths) error {
	var sb strings.Builder

	sb.WriteString("# Combined bisync filter (auto-generated)\n")
	sb.WriteString("# DO NOT EDIT — regenerated from workspace-filter.txt + workspace-skip.txt\n\n")

	// Read static filter
	if data, err := os.ReadFile(paths.FilterFile); err == nil {
		sb.WriteString("# === Static patterns ===\n")
		sb.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			sb.WriteString("\n")
		}
	}

	// Read skip list
	if data, err := os.ReadFile(paths.SkipFile); err == nil {
		lines := strings.Split(string(data), "\n")
		hasEntries := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "- ") {
				if !hasEntries {
					sb.WriteString("\n# === Skip list (permission errors, symlinks, shortcuts) ===\n")
					hasEntries = true
				}
				sb.WriteString(strings.TrimSpace(line) + "\n")
			}
		}
	}

	return os.WriteFile(paths.BisyncFilter, []byte(sb.String()), 0644)
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
func UpdateSkipList(logFile, skipFile string) (int, error) {
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

// ClearSkipList removes the skip list and bisync filter files.
func ClearSkipList(paths *Paths) error {
	for _, f := range []string{paths.SkipFile, paths.BisyncFilter} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// IsDarwin reports whether we're on macOS.
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}
