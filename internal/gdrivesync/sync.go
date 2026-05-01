package gdrivesync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Defaults applied when the user state has not specified a value.
const (
	defaultLocalRel    = "workspace/work"
	defaultMirrorRel   = "gdrive-workspace/work"
	defaultMaxDelete   = 1000
	defaultInterval    = 300 // 5 min — matches existing rsync scheduler default
	intervalMin        = 60
	intervalMax        = 86400
	logRotateMaxLines  = 2000
	logRotateKeepLines = 1000
)

// Config holds resolved gdrive-sync parameters. Populated by ResolveConfig.
type Config struct {
	LocalPath      string   // workspace tree, with trailing slash
	MirrorPath     string   // gdrive tree, with trailing slash
	ExcludesFile   string   // materialized static exclude list (embedded baseline)
	ConfigDir      string   // dotfiles config dir, used for dynamic excludes file
	SharedExcludes []string // operator-curated shared paths (relative to MirrorPath)
	LogFile        string
	LockDir        string
	RsyncPath      string // resolved rsync binary; empty if not installed
	MaxDelete      int
	Interval       int // auto-sync interval in seconds (used by Scheduler templates)
	Verbose        bool
}

// ResolveConfig merges UserState fields with defaults and materializes
// the embedded exclude rules to disk so rsync can read them via
// --exclude-from. Trailing slashes are normalized for rsync semantics.
func ResolveConfig(state *config.UserState) (*Config, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return nil, err
	}

	home, _ := os.UserHomeDir()
	gs := state.Modules.GdriveSync

	localPath := gs.LocalPath
	if localPath == "" {
		localPath = filepath.Join(home, defaultLocalRel)
	}
	localPath = expandHome(localPath, home)
	if !strings.HasSuffix(localPath, "/") {
		localPath += "/"
	}

	mirrorPath := gs.MirrorPath
	if mirrorPath == "" {
		mirrorPath = filepath.Join(home, defaultMirrorRel)
	}
	mirrorPath = expandHome(mirrorPath, home)
	if !strings.HasSuffix(mirrorPath, "/") {
		mirrorPath += "/"
	}

	excludesFile, err := MaterializeExcludesFile(paths.ConfigDir)
	if err != nil {
		return nil, err
	}

	maxDelete := gs.MaxDelete
	if maxDelete <= 0 {
		maxDelete = defaultMaxDelete
	}

	interval := gs.Interval
	switch {
	case interval <= 0:
		interval = defaultInterval
	case interval < intervalMin:
		interval = intervalMin
	case interval > intervalMax:
		interval = intervalMax
	}

	rsyncPath, _ := osexec.LookPath("rsync")

	return &Config{
		LocalPath:      localPath,
		MirrorPath:     mirrorPath,
		ExcludesFile:   excludesFile,
		ConfigDir:      paths.ConfigDir,
		SharedExcludes: append([]string(nil), gs.SharedExcludes...),
		LogFile:        paths.LogFile,
		LockDir:        paths.LockDir,
		RsyncPath:      rsyncPath,
		MaxDelete:      maxDelete,
		Interval:       interval,
	}, nil
}

func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// ── arg builders (extracted for testability) ────────────────────────────

// pullArgs builds the rsync argv for the pull (mirror → local) pass.
// Uses --update (workspace-authoritative) so workspace-only files are
// never deleted. --backup snapshots overwrites into the conflict dir.
// dynExcludesFile is the per-run shared/manual exclude file; "" skips it.
func pullArgs(cfg *Config, conflict *ConflictDir, dynExcludesFile string, dryRun bool) []string {
	args := commonArgs([]string{cfg.ExcludesFile, dynExcludesFile}, cfg.Verbose)
	args = append(args,
		"--update",
		"--backup",
		"--backup-dir="+conflict.PullBackupRel(),
	)
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, cfg.MirrorPath, cfg.LocalPath)
	return args
}

// pushArgs builds the rsync argv for the push (local → mirror) pass.
// Uses --delete-after to propagate workspace deletions to the mirror,
// guarded by --max-delete=N to abort runaway deletions.
// dynExcludesFile is the per-run shared/manual exclude file; "" skips it.
func pushArgs(cfg *Config, conflict *ConflictDir, dynExcludesFile string, dryRun bool) []string {
	args := commonArgs([]string{cfg.ExcludesFile, dynExcludesFile}, cfg.Verbose)
	args = append(args,
		"--delete-after",
		"--max-delete="+strconv.Itoa(cfg.MaxDelete),
		"--backup",
		"--backup-dir="+conflict.PushBackupRel(),
	)
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, cfg.LocalPath, cfg.MirrorPath)
	return args
}

// migrateArgs builds the rsync argv for the one-shot migration pull.
// No --update, no --delete: pure additive bring-everything-in.
// dynExcludesFile is the per-run shared/manual exclude file; "" skips it.
func migrateArgs(cfg *Config, dynExcludesFile string, dryRun bool) []string {
	args := commonArgs([]string{cfg.ExcludesFile, dynExcludesFile}, cfg.Verbose)
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, cfg.MirrorPath, cfg.LocalPath)
	return args
}

// prepareDynamicExcludes scans the mirror for Drive shortcuts, merges
// the operator's manual list, and writes the union to a per-run file.
// Returns the file path so callers can pass it to rsync as a second
// --exclude-from. The file is always written (even empty) for
// predictable layering — see MaterializeSharedExcludesFile.
func prepareDynamicExcludes(cfg *Config) (string, error) {
	entries, err := ScanShared(strings.TrimRight(cfg.MirrorPath, "/"), cfg.SharedExcludes)
	if err != nil {
		return "", fmt.Errorf("scanning shared entries: %w", err)
	}
	return MaterializeSharedExcludesFile(cfg.ConfigDir, entries)
}

// refuseSharedDriveMirror returns a non-nil error if cfg.MirrorPath
// resolves under a Drive Desktop "Shared drives" root. Workspace-
// authoritative semantics make no sense for content the user does not
// own — pushing would attempt to delete other people's files.
func refuseSharedDriveMirror(cfg *Config) error {
	if IsSharedDriveMount(cfg.MirrorPath) {
		return fmt.Errorf(
			"refusing to sync: mirror %q resolves under a Drive 'Shared drives' root.\n"+
				"Workspace-authoritative semantics would propagate deletions into a team drive.\n"+
				"Point gdrive-sync.mirror_path at a folder under My Drive instead.",
			cfg.MirrorPath,
		)
	}
	return nil
}

// ── pull / push / sync ──────────────────────────────────────────────────

// Pull mirrors → local, --update only. Workspace deletions are NOT reverted.
// New files appearing in the mirror (e.g. via Drive client) flow into the
// workspace.
func Pull(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	dyn, err := prepareDynamicExcludes(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()
	args := pullArgs(cfg, conflict, dyn, dryRun)
	fmt.Printf("  Pull: %s → %s\n", cfg.MirrorPath, cfg.LocalPath)
	return runRsync(ctx, runner, cfg, args)
}

// Push local → mirror with --delete-after. Workspace is authoritative;
// mirror's view of "what should exist" is rebuilt from workspace each push.
// --max-delete guards against catastrophic accidents.
func Push(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	dyn, err := prepareDynamicExcludes(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()
	args := pushArgs(cfg, conflict, dyn, dryRun)
	fmt.Printf("  Push: %s → %s\n", cfg.LocalPath, cfg.MirrorPath)
	return runRsync(ctx, runner, cfg, args)
}

// Sync runs Pull then Push within a single conflict-timestamp window so
// any backups created across both passes share one parent directory.
// Pull errors are logged but do not abort the push (matches existing
// internal/rsync.Sync semantics).
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	dyn, err := prepareDynamicExcludes(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()

	pullErr := runRsync(ctx, runner, cfg, pullArgs(cfg, conflict, dyn, dryRun))
	if pullErr != nil {
		fmt.Printf("  ⚠ pull: %v — continuing to push\n", pullErr)
	} else {
		fmt.Printf("  ✓ pull: %s → %s\n", cfg.MirrorPath, cfg.LocalPath)
	}

	pushErr := runRsync(ctx, runner, cfg, pushArgs(cfg, conflict, dyn, dryRun))
	if pullErr != nil {
		return pullErr
	}
	return pushErr
}

// MigratePull is the additive one-shot pull used by the `migrate`
// subcommand to bring all existing mirror content into the workspace.
// No --update, no --delete; safe to re-run.
func MigratePull(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	dyn, err := prepareDynamicExcludes(cfg)
	if err != nil {
		return err
	}
	args := migrateArgs(cfg, dyn, dryRun)
	fmt.Printf("  Migrate pull: %s → %s\n", cfg.MirrorPath, cfg.LocalPath)
	return runRsync(ctx, runner, cfg, args)
}

func runRsync(ctx context.Context, runner *exec.Runner, cfg *Config, args []string) error {
	if cfg.Verbose {
		return runner.RunAttached(ctx, "rsync", args...)
	}
	_, err := runner.Run(ctx, "rsync", args...)
	return err
}
