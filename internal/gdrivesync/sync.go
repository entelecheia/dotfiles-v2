package gdrivesync

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	ExcludesFile   string   // materialized static exclude list (under .dotfiles/gdrive-sync/)
	IgnoreFile     string   // user-supplied ignore patterns (under .dotfiles/gdrive-sync/)
	ConfigDir      string   // workspace-local store dir (.dotfiles/gdrive-sync/) — dynamic files land here
	SharedExcludes []string // operator-curated shared paths (relative to MirrorPath)
	LogFile        string
	LockDir        string
	RsyncPath      string // resolved rsync binary; empty if not installed
	MaxDelete      int
	Interval       int               // push scheduler cadence (seconds)
	PullInterval   int               // pull+intake scheduler cadence (0 = no unit)
	Propagation    PropagationPolicy // default {true,true,false}
	Paused         bool              // mirrors LocalConfig.Paused; the auth source for sync gating
	Verbose        bool

	// LocalPaths exposes the resolved per-workspace layout for
	// callers (status, init, manifest readers) that need granular
	// access beyond what the convenience fields above expose.
	LocalPaths *LocalPaths
}

// ResolveConfig builds the runtime Config by reading the per-workspace
// local store (.dotfiles/gdrive-sync/), migrating from the legacy
// global state on first call. Trailing slashes are normalized for
// rsync semantics.
//
// The global UserGdriveSyncState is consulted only as a migration
// source (and for LocalPath, the entry point that locates the
// workspace). Once .dotfiles/gdrive-sync/config.yaml exists, the
// global block is no longer read.
func ResolveConfig(state *config.UserState) (*Config, error) {
	return resolveConfig(state, true)
}

// ResolveConfigReadOnly resolves the same runtime values without creating
// the local store, migrating global config, or healing .gitignore. Use it for
// status/list commands that must not mutate the workspace.
func ResolveConfigReadOnly(state *config.UserState) (*Config, error) {
	return resolveConfig(state, false)
}

func resolveConfig(state *config.UserState, migrate bool) (*Config, error) {
	systemPaths, err := ResolvePaths()
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

	localPaths := ResolveLocalPaths(localPath)

	var localCfg *LocalConfig
	if migrate {
		localCfg, err = LoadOrMigrateLocalConfig(state, localPaths)
		if err != nil {
			return nil, err
		}
	} else {
		if cfg, ok, err := LoadLocalConfig(localPaths); err != nil {
			return nil, err
		} else if ok {
			localCfg = cfg
		} else {
			localCfg = localConfigFromGlobal(state)
		}
	}

	mirrorPath := localCfg.MirrorPath
	if mirrorPath == "" {
		mirrorPath = gs.MirrorPath
	}
	if mirrorPath == "" {
		mirrorPath = filepath.Join(home, defaultMirrorRel)
	}
	mirrorPath = expandHome(mirrorPath, home)
	if !strings.HasSuffix(mirrorPath, "/") {
		mirrorPath += "/"
	}

	maxDelete := localCfg.MaxDelete
	if maxDelete <= 0 {
		maxDelete = defaultMaxDelete
	}

	interval := localCfg.Interval
	switch {
	case interval <= 0:
		interval = defaultInterval
	case interval < intervalMin:
		interval = intervalMin
	case interval > intervalMax:
		interval = intervalMax
	}

	pullInterval := localCfg.PullInterval
	if pullInterval > 0 {
		switch {
		case pullInterval < intervalMin:
			pullInterval = intervalMin
		case pullInterval > intervalMax:
			pullInterval = intervalMax
		}
	}

	policy := localCfg.Propagation
	if err := policy.Validate(); err != nil {
		// Defensive: heal a corrupt on-disk policy back to defaults.
		policy = DefaultPropagationPolicy()
	}

	rsyncPath, _ := osexec.LookPath("rsync")

	return &Config{
		LocalPath:      localPath,
		MirrorPath:     mirrorPath,
		ExcludesFile:   localPaths.ExcludeFile,
		IgnoreFile:     localPaths.IgnoreFile,
		ConfigDir:      localPaths.StoreDir,
		SharedExcludes: append([]string(nil), localCfg.SharedExcludes...),
		LogFile:        localPaths.LogFile,
		LockDir:        systemPaths.LockDir,
		RsyncPath:      rsyncPath,
		MaxDelete:      maxDelete,
		Interval:       interval,
		PullInterval:   pullInterval,
		Propagation:    policy,
		Paused:         localCfg.Paused,
		LocalPaths:     localPaths,
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
	args := commonArgs([]string{cfg.ExcludesFile, cfg.IgnoreFile, dynExcludesFile}, cfg.Verbose)
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
// Translates cfg.Propagation into rsync flags (--existing /
// --ignore-existing for create/update toggles; --delete-after with
// --max-delete cap for delete) and always excludes the workspace's
// staging dirs so they never bounce back to mirror.
// dynExcludesFile is the per-run shared/manual exclude file; "" skips it.
func pushArgs(cfg *Config, conflict *ConflictDir, dynExcludesFile string, dryRun bool) []string {
	args := commonArgs([]string{cfg.ExcludesFile, cfg.IgnoreFile, dynExcludesFile}, cfg.Verbose)
	args = append(args, pushAlwaysExcludes()...)
	args = append(args, propagationFlags(cfg.Propagation, cfg.MaxDelete)...)
	args = append(args,
		"--backup",
		"--backup-dir="+conflict.PushBackupRel(),
	)
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, cfg.LocalPath, cfg.MirrorPath)
	return args
}

// pushAlwaysExcludes returns the workspace-anchored excludes that push
// must enforce regardless of operator config: the per-workspace store
// (`.dotfiles/`) and the GDrive intake staging area (`inbox/gdrive/`).
// Anchored with leading `/` so deeper subtrees with the same names are
// untouched.
func pushAlwaysExcludes() []string {
	return []string{
		"--exclude=/.dotfiles/",
		"--exclude=/inbox/gdrive/",
	}
}

// propagationFlags translates a PropagationPolicy into the rsync flags
// that enforce it. Default policy `{true, true, false}` returns nil
// (rsync's natural behavior copies new + modified, no delete).
func propagationFlags(p PropagationPolicy, maxDelete int) []string {
	var flags []string
	if !p.Create {
		// `--existing` makes rsync skip files absent in destination,
		// effectively scoping it to updates of files mirror already has.
		flags = append(flags, "--existing")
	}
	if !p.Update {
		// `--ignore-existing` skips files that already exist in dest,
		// scoping to creates only.
		flags = append(flags, "--ignore-existing")
	}
	if p.Delete {
		flags = append(flags,
			"--delete-after",
			"--max-delete="+strconv.Itoa(maxDelete),
		)
	}
	return flags
}

// migrateArgs builds the rsync argv for the one-shot migration pull.
// No --update, no --delete: pure additive bring-everything-in.
// dynExcludesFile is the per-run shared/manual exclude file; "" skips it.
func migrateArgs(cfg *Config, dynExcludesFile string, dryRun bool) []string {
	args := commonArgs([]string{cfg.ExcludesFile, cfg.IgnoreFile, dynExcludesFile}, cfg.Verbose)
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

// Pull restores/updates only baseline-tracked Drive payloads. Baseline-unknown
// mirror files are left for Intake to stage into inbox/gdrive.
func Pull(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	_, err := PullTracked(ctx, runner, cfg, PullOptions{DryRun: dryRun})
	return err
}

// Push local → mirror under cfg.Propagation. It first runs a tracked pull guard
// so Drive edits to baseline-managed payloads are not overwritten by stale local
// content. The policy maps to rsync flags (see propagationFlags); an all-false
// policy is refused before any rsync invocation. The workspace's per-workspace
// store (`.dotfiles/`) and intake staging area (`inbox/gdrive/`) are always
// excluded so they never bounce back to mirror, regardless of operator excludes.
//
// On a successful non-dry-run push, the baseline manifest is refreshed as the
// Git-shared Drive payload index so other machines can restore accepted
// artifacts from the mirror.
func Push(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := cfg.Propagation.Validate(); err != nil {
		return fmt.Errorf("push refused: %w", err)
	}
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	pre, err := PullTracked(ctx, runner, cfg, PullOptions{DryRun: dryRun})
	if err != nil {
		return fmt.Errorf("tracked pull guard: %w", err)
	}
	if len(pre.Conflicts) > 0 {
		return fmt.Errorf("push refused: %d tracked Drive conflict(s) need manual review under %s",
			len(pre.Conflicts), filepath.Join(strings.TrimRight(cfg.LocalPath, "/"), conflictsDirName))
	}
	dyn, err := prepareDynamicExcludes(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()
	args := pushArgs(cfg, conflict, dyn, dryRun)
	fmt.Printf("  Push: %s → %s (%s)\n", cfg.LocalPath, cfg.MirrorPath, cfg.Propagation)
	if err := runRsync(ctx, runner, cfg, args); err != nil {
		return err
	}
	if !dryRun && cfg.LocalPaths != nil {
		if err := RefreshBaseline(cfg, FingerprintStrict); err != nil {
			return fmt.Errorf("baseline refresh: %w", err)
		}
		if err := UpdateLocalState(cfg.LocalPaths, func(s *LocalState) {
			s.LastPush = time.Now().UTC()
		}); err != nil {
			return fmt.Errorf("state update: %w", err)
		}
	}
	return nil
}

// Sync is now a thin alias for Push — the historical bidirectional Pull
// + Push behavior was retired in favor of push-first semantics with a
// separate Intake step (see `dot gdrive-sync intake`). Kept as an entry
// point so callers (and the existing scheduler unit) keep working.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	return Push(ctx, runner, cfg, dryRun)
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
