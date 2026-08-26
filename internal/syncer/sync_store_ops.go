package syncer

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// trimTrailingSlash drops one trailing separator, leaving a bare "/" alone.
// It is deliberately not strings.TrimRight: the cli helper this was moved
// beside strips exactly one slash, and "//" must stay "/" rather than become
// the empty string.
func trimTrailingSlash(p string) string {
	if len(p) > 1 && p[len(p)-1] == '/' {
		return p[:len(p)-1]
	}
	return p
}

// EditableLocalConfig loads the workspace-local config for mutation,
// substituting a defaulted one when the store has none yet.
func EditableLocalConfig(cfg *Config) (*LocalConfig, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("local paths unresolved")
	}
	localCfg, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return nil, err
	}
	if !ok {
		localCfg = &LocalConfig{Propagation: DefaultPropagationPolicy()}
	}
	return localCfg, nil
}

// SetLocalSchedule mutates LocalConfig scheduler settings, persists, and
// keeps cfg in sync.
func SetLocalSchedule(cfg *Config, pushInterval, pullInterval int, pushMode, pullMode RunMode, dryRun bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	schedule, err := (ScheduleSettings{
		Interval:     pushInterval,
		PullInterval: pullInterval,
		PushMode:     pushMode,
		PullMode:     pullMode,
	}).Normalize()
	if err != nil {
		return err
	}
	local, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		local = &LocalConfig{Propagation: DefaultPropagationPolicy()}
	}
	schedule.ApplyToLocalConfig(local)
	if !dryRun {
		if err := SaveLocalConfig(cfg.LocalPaths, local); err != nil {
			return err
		}
	}
	cfg.Interval = schedule.Interval
	cfg.PullInterval = schedule.PullInterval
	cfg.PushMode = schedule.PushMode
	cfg.PullMode = schedule.PullMode
	return nil
}

// SetLocalOwner records the scheduler owner in the workspace-local config and
// keeps the resolved config in sync. Dry runs update only the in-memory config
// so later output can describe the setup they would install.
func SetLocalOwner(cfg *Config, owner string, dryRun bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("owner must not be empty")
	}
	local, err := EditableLocalConfig(cfg)
	if err != nil {
		return err
	}
	local.Owner = owner
	if !dryRun {
		if err := SaveLocalConfig(cfg.LocalPaths, local); err != nil {
			return err
		}
	}
	cfg.Owner = owner
	return nil
}

// SetLocalPaused mutates the local config's Paused field, persists, and
// keeps cfg in sync so callers see the new value without re-running
// ResolveConfig.
func SetLocalPaused(cfg *Config, paused bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	local, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		// Should not happen — ResolveConfig migrates first. Defensive fallback.
		local = &LocalConfig{Propagation: DefaultPropagationPolicy()}
	}
	local.Paused = paused
	if err := SaveLocalConfig(cfg.LocalPaths, local); err != nil {
		return err
	}
	cfg.Paused = paused
	return nil
}

// SetLocalTarget writes the target into the workspace-local config so it takes
// effect immediately. Local targets also update the legacy MirrorPath field so
// older binaries reading this workspace resolve the same mirror.
func SetLocalTarget(cfg *Config, target Target) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved — bug in ResolveConfig")
	}
	localCfg, _, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return fmt.Errorf("load local config: %w", err)
	}
	localCfg.Target = target.String()
	if target.Kind == TargetLocal {
		localCfg.MirrorPath = target.Path
	}
	if err := SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("save local config: %w", err)
	}
	return nil
}

// InitResult describes the store `dot sync init` just healed, or — when DryRun
// is set — the one it would have healed.
type InitResult struct {
	// DryRun reports that nothing below was created. The engine returns the
	// flag and the paths; cli owns the wording it renders them with.
	DryRun bool

	// LegacyStoreDir names the pre-rename store the run would migrate to
	// StoreDir before doing anything else. Set only under DryRun, and only when
	// one is actually pending: a real run performs the rename and reports it on
	// stderr from resolveConfig, so there is nothing left to preview.
	LegacyStoreDir string

	StoreDir    string
	Workspace   string
	Mirror      string
	Propagation PropagationPolicy
	FilterMode  FilterMode
	InboxDir    string
	ConfigFile  string
	IncludeFile string
	IgnoreFile  string

	// WorkspaceIgnore is the one WORKSPACE-level file EnsureLocalLayout
	// touches: it ends with appendGitignoreBlock. Everything else it creates
	// lives under StoreDir.
	WorkspaceIgnore string
}

// InitStore heals the per-workspace store and creates the intake staging dir.
//
// Under dryRun it computes the same fully-populated result from the resolved
// paths and creates nothing. Creating the store is what this command is for, but
// a preview of that creation is still a preview (D-03), and since Bootstrap now
// takes the read-only resolver under --dry-run the tree this would heal may not
// exist at all.
//
// Without dryRun, Bootstrap has already triggered LoadOrMigrateLocalConfig, so
// the .dotfiles/<profile>/ tree exists by the time this runs. Heal anything
// missing (the operator may have deleted files) and create inbox/gdrive.
func InitStore(cfg *Config, dryRun bool) (*InitResult, error) {
	paths := cfg.LocalPaths
	if paths == nil {
		return nil, fmt.Errorf("local paths unresolved — bug in ResolveConfig")
	}
	var legacyStore string
	if dryRun {
		// Bootstrap hands a dry run the READ-ONLY resolver, which keeps the
		// pre-rename fallback. That is right for a read-only command and wrong
		// for this one: a real `dot sync init` migrates first and then operates
		// under .dotfiles/sync, so previewing the pre-migration paths describes
		// a world the run will have left behind. Re-resolve past the fallback
		// and name the rename as work this command would do. The two resolvers
		// agree when nothing is pending, so this is a no-op on a migrated
		// workspace.
		legacyStore = pendingLegacyStore(cfg.LocalPath)
		paths = ResolveLocalPathsPostMigration(cfg.LocalPath, cfg.Profile)
	}
	inboxGdrive := trimTrailingSlash(cfg.LocalPath) + "/inbox/gdrive"
	if !dryRun {
		if err := EnsureLocalLayout(paths); err != nil {
			return nil, fmt.Errorf("ensure layout: %w", err)
		}
		if err := os.MkdirAll(inboxGdrive, 0755); err != nil {
			return nil, fmt.Errorf("create inbox/gdrive: %w", err)
		}
	}
	return &InitResult{
		DryRun:         dryRun,
		LegacyStoreDir: legacyStore,
		StoreDir:       paths.StoreDir,
		Workspace:      trimTrailingSlash(cfg.LocalPath),
		Mirror:         trimTrailingSlash(cfg.MirrorPath),
		Propagation:    cfg.Propagation,
		FilterMode:     cfg.FilterMode,
		InboxDir:       inboxGdrive,
		ConfigFile:     paths.ConfigFile,
		IncludeFile:    paths.IncludeFile,
		IgnoreFile:     paths.IgnoreFile,

		WorkspaceIgnore: paths.WorkspaceIgnore,
	}, nil
}

// InboxReport summarizes the mirror intake staging area.
type InboxReport struct {
	StagingRoot string
	RunDirs     int
	Files       int
	Imports     int
	Tombstones  []Tombstone
}

// InboxSummary counts what is staged and tracked under the profile store.
func InboxSummary(cfg *Config) (*InboxReport, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("local paths unresolved")
	}
	stagingRoot := trimTrailingSlash(cfg.LocalPath) + "/inbox/gdrive"
	runDirs, _ := os.ReadDir(stagingRoot)
	report := &InboxReport{StagingRoot: stagingRoot}
	for _, e := range runDirs {
		if !e.IsDir() {
			continue
		}
		report.RunDirs++
		_ = filepath.WalkDir(filepath.Join(stagingRoot, e.Name()), func(_ string, d fs.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				report.Files++
			}
			return nil
		})
	}

	imports, err := LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	if err != nil {
		return nil, fmt.Errorf("loading imports: %w", err)
	}
	tomb, err := LoadTombstones(cfg.LocalPaths.TombstonesFile)
	if err != nil {
		return nil, fmt.Errorf("loading tombstones: %w", err)
	}
	report.Imports = len(imports)
	report.Tombstones = tomb
	return report, nil
}

// InboxForget drops a path from imports.manifest so the next intake re-stages
// it. It reports whether an entry was actually present.
func InboxForget(cfg *Config, raw string) (bool, error) {
	if cfg.LocalPaths == nil {
		return false, fmt.Errorf("local paths unresolved")
	}
	rel := strings.TrimSpace(raw)
	if rel == "" {
		return false, fmt.Errorf("relpath cannot be empty")
	}
	return ForgetImport(cfg.LocalPaths, rel)
}

// InboxManifestCounts reports how many imports and tombstones a clear would
// discard. Load failures collapse to zero, as they did before the move: an
// unreadable manifest is reported as "already empty" and the clear is a no-op.
func InboxManifestCounts(cfg *Config) (int, int, error) {
	if cfg.LocalPaths == nil {
		return 0, 0, fmt.Errorf("local paths unresolved")
	}
	imports, _ := LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	tomb, _ := LoadTombstones(cfg.LocalPaths.TombstonesFile)
	return len(imports), len(tomb), nil
}

// SharedCount reports how many manual shared-exclude entries are configured.
func SharedCount(cfg *Config) (int, error) {
	localCfg, err := EditableLocalConfig(cfg)
	if err != nil {
		return 0, err
	}
	return len(localCfg.SharedExcludes), nil
}

// SharedAdd appends the given paths to the manual shared-excludes list,
// returning only the ones that were not already present.
func SharedAdd(cfg *Config, args []string) ([]string, error) {
	mirror := trimTrailingSlash(cfg.MirrorPath)
	added := make([]string, 0, len(args))
	localCfg, err := EditableLocalConfig(cfg)
	if err != nil {
		return nil, err
	}
	current := append([]string(nil), localCfg.SharedExcludes...)

	for _, raw := range args {
		rel, err := RelativizeForMirror(raw, mirror)
		if err != nil {
			return nil, err
		}
		if !containsSharedPath(current, rel) {
			current = append(current, rel)
			added = append(added, rel)
		}
	}

	dedupedSorted := dedupSortedStrings(current)
	localCfg.SharedExcludes = dedupedSorted
	if err := SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return nil, fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = dedupedSorted
	return added, nil
}

// SharedRemove drops the given paths from the manual shared-excludes list,
// returning only the ones that were actually present.
func SharedRemove(cfg *Config, args []string) ([]string, error) {
	mirror := trimTrailingSlash(cfg.MirrorPath)
	removed := make([]string, 0, len(args))
	localCfg, err := EditableLocalConfig(cfg)
	if err != nil {
		return nil, err
	}
	current := append([]string(nil), localCfg.SharedExcludes...)

	for _, raw := range args {
		rel, err := RelativizeForMirror(raw, mirror)
		if err != nil {
			return nil, err
		}
		next := current[:0]
		gone := false
		for _, e := range current {
			if e == rel {
				gone = true
				continue
			}
			next = append(next, e)
		}
		current = next
		if gone {
			removed = append(removed, rel)
		}
	}

	localCfg.SharedExcludes = current
	if err := SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return nil, fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = current
	return removed, nil
}

// SharedClear empties the manual shared-excludes list.
func SharedClear(cfg *Config) error {
	localCfg, err := EditableLocalConfig(cfg)
	if err != nil {
		return err
	}
	localCfg.SharedExcludes = nil
	if err := SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = nil
	return nil
}

// RelativizeForMirror normalizes a user-supplied path so it lives under
// mirror as a relative path. Absolute paths must be inside mirror.
// Trailing slashes and "./" prefixes are stripped. Empty results,
// "..", and parent escapes are rejected.
func RelativizeForMirror(raw, mirror string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(cleaned) {
		mirrorAbs, err := filepath.Abs(mirror)
		if err != nil {
			return "", fmt.Errorf("resolving mirror %q: %w", mirror, err)
		}
		rel, err := filepath.Rel(mirrorAbs, cleaned)
		if err != nil {
			return "", fmt.Errorf("relativizing %q against %q: %w", cleaned, mirror, err)
		}
		cleaned = rel
	}
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimSuffix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("path resolves to mirror root, refusing to exclude everything")
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path %q escapes mirror root", raw)
		}
	}
	return cleaned, nil
}

// dedupSortedStrings returns a stable, sorted copy of in with duplicates
// removed.
func dedupSortedStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// containsSharedPath reports whether the shared-excludes list already holds
// this relative path.
func containsSharedPath(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// OwnerOptions selects how `dot sync owner` rewrites the profile's writer.
// Exactly one of Clear, SetSelf or SetTo is meaningful; Clear wins, then
// SetSelf, matching the pre-move precedence.
type OwnerOptions struct {
	Config  *Config
	Clear   bool
	SetSelf bool
	SetTo   string
}

// SetOwner records which machine may push this profile and returns the owner
// as written; an empty string means the restriction was removed.
func SetOwner(opts OwnerOptions) (string, error) {
	cfg := opts.Config
	paths := cfg.LocalPaths
	local, ok, err := LoadLocalConfig(paths)
	if err != nil {
		return "", err
	}
	if !ok || local == nil {
		return "", fmt.Errorf("profile %q has no config yet; run dot sync init first", cfg.Profile)
	}
	switch {
	case opts.Clear:
		local.Owner = ""
	case opts.SetSelf:
		local.Owner = PreferredMachineName()
		if local.Owner == "" {
			return "", fmt.Errorf("cannot determine this machine's name")
		}
	default:
		local.Owner = opts.SetTo
	}
	if err := SaveLocalConfig(paths, local); err != nil {
		return "", err
	}
	return local.Owner, nil
}

// RsyncOutcome names what EnsureRsync did about a missing rsync.
type RsyncOutcome int

const (
	RsyncPresent         RsyncOutcome = iota // already on PATH; Version is set
	RsyncWouldInstall                        // dry-run: an install would have been offered
	RsyncInstallDeclined                     // the operator said no
	RsyncInstalled                           // installed during this run; Version is set
)

// RsyncResult is what EnsureRsync found or did.
type RsyncResult struct {
	Outcome RsyncOutcome
	Version string
}

// EnsureRsync verifies rsync is available, offering to install it when it is
// not. out receives the installer's own output (SEAM-01); the four outcomes
// are worded by the caller.
func EnsureRsync(ctx context.Context, runner *exec.Runner, out io.Writer, dryRun bool, confirm ConfirmFunc) (*RsyncResult, error) {
	ver, ok := CheckRsync(runner)
	if ok {
		return &RsyncResult{Outcome: RsyncPresent, Version: ver}, nil
	}
	if dryRun {
		return &RsyncResult{Outcome: RsyncWouldInstall}, nil
	}
	confirmed, err := askSync(confirm, ConfirmRequest{Kind: ConfirmInstallRsync})
	if err != nil {
		return nil, err
	}
	if !confirmed {
		return &RsyncResult{Outcome: RsyncInstallDeclined}, nil
	}
	if err := InstallRsync(ctx, runner, out); err != nil {
		return nil, fmt.Errorf("installing rsync: %w", err)
	}
	ver, ok = CheckRsync(runner)
	if !ok {
		return nil, fmt.Errorf("rsync not found in PATH after install")
	}
	return &RsyncResult{Outcome: RsyncInstalled, Version: ver}, nil
}

// ResumeResult is what `dot sync resume` changed.
type ResumeResult struct {
	WasPaused        bool
	SchedulerOff     bool // no interval configured, so nothing to re-arm
	SchedulerResumed bool
	SchedulerErr     error
}

// SyncResume clears the paused gate and reattaches an installed scheduler.
func SyncResume(ctx context.Context, cfg *Config, runner *exec.Runner) (*ResumeResult, error) {
	res := &ResumeResult{WasPaused: cfg.Paused}
	if cfg.Paused {
		if err := SetLocalPaused(cfg, false); err != nil {
			return nil, fmt.Errorf("saving local config: %w", err)
		}
	}
	if cfg.Interval == 0 && cfg.PullInterval == 0 {
		res.SchedulerOff = true
		return res, nil
	}
	sched, _, err := ResolveScheduler(cfg, runner)
	if err != nil {
		// The state save succeeded; the scheduler is best-effort.
		return res, nil
	}
	if sched.State(ctx) != SchedulerNotInstalled {
		if err := sched.Resume(ctx); err != nil {
			res.SchedulerErr = err
		} else {
			res.SchedulerResumed = true
		}
	}
	return res, nil
}

// PauseResult is what `dot sync pause` changed.
type PauseResult struct {
	WasPaused        bool
	SchedulerStopped bool
	SchedulerErr     error
}

// SyncPause sets the paused gate and stops a running scheduler, so we do not
// waste invocations hitting the paused gate every Interval seconds.
func SyncPause(ctx context.Context, cfg *Config, runner *exec.Runner) (*PauseResult, error) {
	res := &PauseResult{WasPaused: cfg.Paused}
	if !cfg.Paused {
		if err := SetLocalPaused(cfg, true); err != nil {
			return nil, fmt.Errorf("saving local config: %w", err)
		}
	}
	sched, _, err := ResolveScheduler(cfg, runner)
	if err != nil {
		return res, nil
	}
	if sched.State(ctx) == SchedulerRunning {
		if err := sched.Pause(ctx); err != nil {
			res.SchedulerErr = err
		} else {
			res.SchedulerStopped = true
		}
	}
	return res, nil
}
