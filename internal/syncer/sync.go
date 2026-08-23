package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// defaultMirrorPath is the mirror used when neither the local config nor the
// global state set one. It prefers a detected cloud root (Dropbox, then
// Google Drive) so the mirror follows the same cloud the backup root uses;
// absent any cloud root it falls back to ~/gdrive-workspace/work.
func defaultMirrorPath(home string) string {
	if cloud := appsettings.DetectCloudCandidate(home); cloud != "" {
		// cloud is "<root>/secrets/dotfiles-backup"; the mirror lives at
		// "<root>/work", i.e. two levels up from the secrets marker.
		return filepath.Join(filepath.Dir(filepath.Dir(cloud)), "work")
	}
	return filepath.Join(home, defaultMirrorRel)
}

// Defaults applied when the user state has not specified a value.
const (
	defaultLocalRel    = "workspace/work"
	defaultMirrorRel   = "gdrive-workspace/work"
	defaultMaxDelete   = 1000
	logRotateMaxLines  = 2000
	logRotateKeepLines = 1000
)

// Config holds resolved sync parameters. Populated by ResolveConfig.
type Config struct {
	Profile string // store name under .dotfiles/ ("sync" = cloud mirror)
	// IncludeSubmodules keeps submodule working trees in the payload instead of
	// excluding them (mirror excludes; peer includes).
	IncludeSubmodules bool
	Owner             string // hostname allowed to push this profile; empty = unrestricted
	LocalPath         string // workspace tree, with trailing slash
	MirrorPath        string // local mirror tree with trailing slash; empty for ssh targets
	Target            Target // parsed destination (local dir or ssh host:path)
	MirrorIsDefault   bool   // target came from defaultMirrorPath, not explicit config
	FilterMode        FilterMode
	IncludeFile       string   // editable include list (under .dotfiles/sync/)
	IncludePatterns   []string // parsed include list used by Go filters + rsync args
	ExcludesFile      string   // materialized static exclude list (under .dotfiles/sync/)
	IgnoreFile        string   // user-supplied ignore patterns (under .dotfiles/sync/)
	AllowFile         string   // explicit secrets opt-in patterns (under .dotfiles/sync/)
	AllowPatterns     []string // parsed allow.txt — re-included ahead of the secrets layer
	ConfigDir         string   // workspace-local store dir (.dotfiles/sync/) — dynamic files land here
	SharedExcludes    []string // operator-curated shared paths (relative to MirrorPath)
	LogFile           string
	LockDir           string
	RsyncPath         string // resolved rsync binary; empty if not installed
	// RemoteRsyncPath is passed as --rsync-path for ssh targets whose default
	// rsync cannot serve a modern client (macOS 26 openrsync). Empty = default OK.
	RemoteRsyncPath string
	MaxDelete       int
	Interval        int               // push scheduler cadence (seconds)
	PullInterval    int               // pull scheduler cadence (0 = no unit)
	PushMode        RunMode           // automatic push mode (clean|force)
	PullMode        RunMode           // automatic pull mode (clean|force)
	Propagation     PropagationPolicy // default {true,true,false}
	Paused          bool              // mirrors LocalConfig.Paused; the auth source for sync gating
	Verbose         bool

	// Tombstones are relpaths deleted locally since the last push, computed
	// once per run by ComputeTombstones before the pull. Runtime-only, set by
	// the caller the same way RemoteRsyncPath is. prepareRuntimeFilters turns
	// them into the pull's protective exclude layer; PropagateDeletes applies
	// them to the target.
	Tombstones []string

	// NamesNormalized avoids a second full workspace scan when a CLI caller
	// already ran the marker-gated NFD preflight under the shared lock.
	NamesNormalized bool

	// Out receives sync progress output. Runtime-only, set by the caller the
	// same way RemoteRsyncPath is; nil means process stdout.
	Out io.Writer

	// Home is the raw --home override this config was resolved for; empty
	// means the process home. Every path an engine entry derives from "the
	// user's home" must come from HomeDir() rather than os.UserHomeDir(),
	// or one command operates on two different homes at once (BUG-07).
	Home string

	// LocalPaths exposes the resolved per-workspace layout for
	// callers (status, init, manifest readers) that need granular
	// access beyond what the convenience fields above expose.
	LocalPaths *LocalPaths
}

// outOrStdout normalizes a nil writer to os.Stdout, so the package states
// its nil rule once.
func outOrStdout(w io.Writer) io.Writer {
	if w == nil {
		return os.Stdout
	}
	return w
}

// out returns the writer progress output goes to: c.Out when set,
// os.Stdout when c or c.Out is nil.
func (c *Config) out() io.Writer {
	if c == nil {
		return os.Stdout
	}
	return outOrStdout(c.Out)
}

// ResolveConfig builds the runtime Config by reading the per-workspace
// local store (.dotfiles/gdrive-sync/), migrating from the legacy
// global state on first call. Trailing slashes are normalized for
// rsync semantics.
//
// The global UserGsyncState is consulted only as a migration
// source (and for LocalPath, the entry point that locates the
// workspace). Once .dotfiles/gdrive-sync/config.yaml exists, the
// global block is no longer read.
func ResolveConfig(state *config.UserState) (*Config, error) {
	return resolveConfig(state, true, "", DefaultProfile)
}

// ResolveConfigForProfile resolves one named sync profile. Each profile has its
// own store under <workspace>/.dotfiles/<profile>/, so its target, filters,
// baseline, lock and scheduler unit are independent of every other profile.
func ResolveConfigForProfile(state *config.UserState, profile string) (*Config, error) {
	return resolveConfig(state, true, "", profile)
}

// ResolveConfigReadOnly resolves the same runtime values without creating
// the local store, migrating global config, or healing .gitignore. Use it for
// status/list commands that must not mutate the workspace.
func ResolveConfigReadOnly(state *config.UserState) (*Config, error) {
	return resolveConfig(state, false, "", DefaultProfile)
}

// ResolveConfigForHomeProfile is ResolveConfigForProfile with an explicit home
// directory instead of os.UserHomeDir(). Commands that honor --home must use it
// so they operate on the target user's workspace rather than the invoking
// user's. An empty home falls back to the current user's.
func ResolveConfigForHomeProfile(state *config.UserState, home, profile string) (*Config, error) {
	return resolveConfig(state, true, home, profile)
}

// ResolveConfigReadOnlyForHomeProfile is ResolveConfigForHomeProfile without
// creating the local store, migrating global config, or healing .gitignore.
// It is what a status or preview run under --home resolves through. An empty
// home falls back to the current user's.
func ResolveConfigReadOnlyForHomeProfile(state *config.UserState, home, profile string) (*Config, error) {
	return resolveConfig(state, false, home, profile)
}

// ResolveConfigReadOnlyForHome is like ResolveConfigReadOnly but resolves all
// home-relative paths (local/mirror defaults, `~` expansion, artifact paths)
// against an explicit home directory instead of os.UserHomeDir(). Commands
// that honor --home must use this so they operate on the target user's mirror
// rather than the invoking user's. An empty home falls back to the current
// user's home.
func ResolveConfigReadOnlyForHome(state *config.UserState, home string) (*Config, error) {
	return resolveConfig(state, false, home, DefaultProfile)
}

func resolveConfig(state *config.UserState, migrate bool, home, profile string) (*Config, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	profile = NormalizeProfile(profile)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	systemPaths, err := ResolvePathsForHomeProfile(home, profile)
	if err != nil {
		return nil, err
	}

	gs := state.Modules.Gsync

	localPath := gs.LocalPath
	if localPath == "" {
		localPath = filepath.Join(home, defaultLocalRel)
	}
	localPath = expandHome(localPath, home)
	if !strings.HasSuffix(localPath, "/") {
		localPath += "/"
	}

	// The gdrive-sync -> sync rename only ever applied to the default store.
	if migrate && profile == DefaultProfile {
		if migrated, err := MigrateLegacyStore(localPath); err != nil {
			return nil, err
		} else if migrated {
			fmt.Fprintln(os.Stderr, "note: migrated workspace store .dotfiles/gdrive-sync -> .dotfiles/sync")
		}
	}

	localPaths := ResolveLocalPathsForProfile(localPath, profile)

	var localCfg *LocalConfig
	if migrate && profile == DefaultProfile {
		localCfg, err = LoadOrMigrateLocalConfig(state, localPaths)
		if err != nil {
			return nil, err
		}
	} else if profile != DefaultProfile {
		// A non-default profile has no global twin to migrate from: an unset
		// profile is genuinely empty, not "inherit the mirror settings".
		//
		// It still needs the surrounding layout. commonArgs passes
		// --exclude-from for exclude.txt and ignore.txt unconditionally, and
		// rsync exits 11 ("file IO error") when it cannot open one - so a fresh
		// profile whose store lacks those files fails every transfer. Measured on
		// the first real peer run.
		if migrate {
			if err := EnsureLocalLayout(localPaths); err != nil {
				return nil, err
			}
		}
		if cfg, ok, err := LoadLocalConfig(localPaths); err != nil {
			return nil, err
		} else if ok {
			localCfg = cfg
		} else {
			localCfg = &LocalConfig{}
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

	// Resolve the destination: the canonical `target` spec wins, then the
	// legacy mirror_path (local + global), then the detected cloud default.
	var target Target
	mirrorIsDefault := false
	if spec := strings.TrimSpace(localCfg.Target); spec != "" {
		t, err := ParseTarget(spec)
		if err != nil {
			return nil, fmt.Errorf("config target: %w", err)
		}
		target = t
	} else {
		mirrorPath := localCfg.MirrorPath
		if mirrorPath == "" {
			mirrorPath = gs.MirrorPath
		}
		mirrorIsDefault = mirrorPath == ""
		if mirrorIsDefault {
			mirrorPath = defaultMirrorPath(home)
		}
		target = Target{Kind: TargetLocal, Path: mirrorPath}
	}
	mirrorPath := ""
	if target.Kind == TargetLocal {
		p := expandHome(target.Path, home)
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		target.Path = p
		mirrorPath = p
	}

	maxDelete := EffectiveMaxDelete(localCfg.MaxDelete)

	schedule := ScheduleSettingsFromLocalConfig(localCfg).NormalizeLenient(nil)

	policy := localCfg.Propagation
	if err := policy.Validate(); err != nil {
		// Defensive: heal a corrupt on-disk policy back to defaults.
		policy = DefaultPropagationPolicy()
	}
	filterMode := normalizeFilterMode(localCfg.FilterMode)
	includePatterns, err := loadPatternFileOrDefault(localPaths.IncludeFile, LoadDefaultIncludePatterns)
	if err != nil {
		return nil, fmt.Errorf("loading include patterns: %w", err)
	}
	allowPatterns, err := loadPatternFileOrDefault(localPaths.AllowFile, func() ([]string, error) { return nil, nil })
	if err != nil {
		return nil, fmt.Errorf("loading allow patterns: %w", err)
	}

	rsyncPath, _ := osexec.LookPath("rsync")

	return &Config{
		Profile:           profile,
		Owner:             localCfg.Owner,
		IncludeSubmodules: localCfg.IncludeSubmodules,
		LocalPath:         localPath,
		MirrorPath:        mirrorPath,
		Target:            target,
		MirrorIsDefault:   mirrorIsDefault,
		FilterMode:        filterMode,
		IncludeFile:       localPaths.IncludeFile,
		IncludePatterns:   includePatterns,
		ExcludesFile:      localPaths.ExcludeFile,
		IgnoreFile:        localPaths.IgnoreFile,
		AllowFile:         localPaths.AllowFile,
		AllowPatterns:     allowPatterns,
		ConfigDir:         localPaths.StoreDir,
		SharedExcludes:    append([]string(nil), localCfg.SharedExcludes...),
		LogFile:           localPaths.LogFile,
		LockDir:           systemPaths.LockDir,
		RsyncPath:         rsyncPath,
		MaxDelete:         maxDelete,
		Interval:          schedule.Interval,
		PullInterval:      schedule.PullInterval,
		PushMode:          schedule.PushMode,
		PullMode:          schedule.PullMode,
		Propagation:       policy,
		Paused:            localCfg.Paused,
		LocalPaths:        localPaths,
	}, nil
}

// EffectiveMaxDelete resolves the persisted zero sentinel to the safety
// default used by runtime configuration.
func EffectiveMaxDelete(value int) int {
	if value <= 0 {
		return defaultMaxDelete
	}
	return value
}

func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// ── arg builders (extracted for testability) ────────────────────────────

// pullArgs builds the rsync argv for the pull (target → local) pass.
// Uses --update (workspace-authoritative) so workspace-only files are
// never deleted. Non-peer profiles snapshot overwrites into the conflict dir;
// peer conflicts are handled by their scoped reconciliation pass.
func pullArgs(cfg *Config, conflict *ConflictDir, rf runtimeFilters, dryRun bool) []string {
	// Put the code-owned deny layer before editable allow/include rules. Rsync
	// uses the first matching filter, so appending these after commonArgs would
	// let an allow.txt entry re-admit a cache or generated log.
	args := append([]string{}, peerVolatileExcludeArgs(cfg)...)
	args = append(args, commonArgs(cfg, rf)...)
	args = append(args, "--update")
	if !peerNormalTransfer(cfg) {
		args = append(args,
			"--backup",
			"--backup-dir="+conflict.PullBackupRel(),
		)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, rsyncTransportArgs(cfg)...)
	args = append(args, cfg.Target.RsyncDest(), cfg.LocalPath)
	return args
}

// pushArgs builds the rsync argv for the push (local → target) pass.
// Translates cfg.Propagation into rsync flags (--existing /
// --ignore-existing for create/update toggles; --delete-after with
// --max-delete cap for delete) and always excludes the workspace's
// staging dirs so they never bounce back to mirror.
func pushArgs(cfg *Config, conflict *ConflictDir, rf runtimeFilters, dryRun bool) []string {
	// See pullArgs: the immutable peer deny layer must precede editable
	// includes, otherwise an operator could opt a volatile tree back in.
	args := append([]string{}, peerVolatileExcludeArgs(cfg)...)
	args = append(args, commonArgs(cfg, rf)...)
	prop := cfg.Propagation
	if cfg.Target.IsSSH() && cfg.Profile == PeerProfile {
		// Blanket --delete removes every target path absent locally. Against a
		// mirror that is the intended meaning; against a peer it is most of the
		// other machine's tree, including everything it created that has not
		// been pulled yet. PropagateDeletes handles removal there, scoped to
		// baseline-recorded paths, so the push stays additive.
		prop.Delete = false
	}
	args = append(args, propagationFlags(prop, cfg.MaxDelete)...)
	// Skip directories that would be empty on the target after filtering, so
	// gitignored leaves do not leave behind shells of folder structure.
	args = append(args, "--prune-empty-dirs")
	if !peerNormalTransfer(cfg) {
		args = append(args,
			"--backup",
			"--backup-dir="+conflict.PushBackupRel(),
		)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, rsyncTransportArgs(cfg)...)
	args = append(args, cfg.LocalPath, cfg.Target.RsyncDest())
	return args
}

// peerNormalTransfer identifies the ordinary SSH peer create/update pass.
// It is already scoped by a baseline-aware PeerPlan, so a blanket backup would
// quarantine every expected update. Losing remote payloads are handled by the
// separate, explicitly scoped conflict pass.
func peerNormalTransfer(cfg *Config) bool {
	return cfg != nil && cfg.Profile == PeerProfile && cfg.Target.IsSSH()
}

// peerVolatileExcludeArgs is code-owned rather than part of editable
// exclude.txt. These generated caches/logs must not enter a peer payload when
// an operator broadens the editable filter policy. graphify-out is deliberately
// absent because it is a shareable analysis artifact.
func peerVolatileExcludeArgs(cfg *Config) []string {
	if cfg == nil || cfg.Profile != PeerProfile {
		return nil
	}
	return []string{
		"--exclude=.cache",
		"--exclude=.cache/",
		"--exclude=/.maru/cache",
		"--exclude=/.maru/cache/",
		"--exclude=/.maru/desk-pipeline/logs",
		"--exclude=/.maru/desk-pipeline/logs/",
		"--exclude=/.maru/desk-pipeline/*.out",
		"--exclude=/.maru/runs",
		"--exclude=/.maru/runs/",
		"--exclude=test-results",
		"--exclude=test-results/",
		"--exclude=playwright-report",
		"--exclude=playwright-report/",
		"--exclude=.astro",
		"--exclude=.astro/",
		"--exclude=*.tsbuildinfo",
		"--exclude=/scratchpad/temp",
		"--exclude=/scratchpad/temp/",
		"--exclude=.metadata_never_index",
	}
}

// rsyncTransportArgs returns transport flags for the configured target
// (SSH remotes need `-e ssh`; local directories need nothing).
func rsyncTransportArgs(cfg *Config) []string {
	if !cfg.Target.IsSSH() {
		return nil
	}
	args := []string{"-e", "ssh"}
	// RemoteRsyncPath is empty when the peer's default rsync is already usable.
	if cfg.RemoteRsyncPath != "" {
		args = append(args, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	return args
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

// prepareRuntimeFilters materializes the per-run filter files: the
// operator's shared-folder excludes, the submodule exclude layer, and the
// tracked ∪ baseline include layer. Every file is always written (even
// empty) for predictable layering.
func prepareRuntimeFilters(cfg *Config) (runtimeFilters, error) {
	var rf runtimeFilters
	entries, err := ScanShared(strings.TrimRight(cfg.MirrorPath, "/"), cfg.SharedExcludes)
	if err != nil {
		return rf, fmt.Errorf("scanning shared entries: %w", err)
	}
	rf.SharedDyn, err = MaterializeRuntimeExcludesFile(cfg.ConfigDir, entries)
	if err != nil {
		return rf, err
	}
	if cfg.LocalPaths == nil {
		return rf, fmt.Errorf("local paths unresolved")
	}
	local := strings.TrimRight(cfg.LocalPath, "/")
	// An empty submodule list materializes an empty exclude file, which is how a
	// peer profile carries submodule working trees: same layering, no special case
	// in commonArgs.
	submodules := gitSubmodulePaths(local)
	if cfg.IncludeSubmodules {
		submodules = nil
	}
	rf.SubmodulesDyn, err = MaterializeSubmodulesDynFile(cfg.LocalPaths, submodules)
	if err != nil {
		return rf, err
	}
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return rf, fmt.Errorf("loading baseline: %w", err)
	}
	// When submodules travel, their tracked files must be in the include layer
	// too. Measured failure: 27 tracked build artifacts inside two submodules
	// (committed .pyc, a pnpm index) matched an exclude pattern, never shipped,
	// and arrived on the peer as 27 deletions - git reported a dirty tree that
	// no one had touched.
	tracked := gitTrackedForSync(local)
	if cfg.IncludeSubmodules {
		for rel := range gitTrackedInSubmodules(local, submodulePathsForScan(local)) {
			tracked[rel] = true
		}
	}
	rels := unionTrackedWithBaseline(tracked, baseline)
	rf.TrackedDyn, err = MaterializeTrackedIncludesFile(cfg.LocalPaths, rels)
	if err != nil {
		return rf, err
	}
	// Deleted paths are still baseline keys, so unionTrackedWithBaseline just
	// re-admitted every one of them to the include layer. The tombstone
	// excludes sit ahead of it in commonArgs and take them back out.
	if len(cfg.Tombstones) > 0 {
		rf.TombstonesDyn, err = MaterializeTombstoneExcludesFile(cfg.ConfigDir, cfg.Tombstones)
		if err != nil {
			return rf, err
		}
	}
	return rf, nil
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
				"Point gsync.mirror_path at a folder under My Drive instead",
			cfg.MirrorPath,
		)
	}
	return nil
}

// ── pull / push / sync ──────────────────────────────────────────────────

// Push local → mirror under cfg.Propagation. The policy maps to rsync flags
// (see propagationFlags); an all-false policy is refused before any rsync
// invocation. The workspace's per-workspace store (`.dotfiles/`) and intake
// staging area (`inbox/gdrive/`) are always excluded so they never bounce back
// to mirror, regardless of operator excludes.
//
// On a successful non-dry-run push, the baseline manifest is refreshed as the
// Git-shared Drive payload index so other machines can restore accepted
// artifacts from the mirror.
func Push(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := cfg.Propagation.Validate(); err != nil {
		return fmt.Errorf("push refused: %w", err)
	}
	// CLI callers normalize under the shared workspace lock before planning.
	// Keep the same marker-gated check here so library callers cannot bypass
	// automatic NFD normalization on a real push.
	namesNormalized := cfg.NamesNormalized
	// The flag is a one-call handoff from a CLI plan. A Config can be reused by
	// library callers, and a later download may introduce another NFC name.
	cfg.NamesNormalized = false
	if !dryRun && !namesNormalized {
		if err := NormalizeWorkspaceNamesBeforePush(cfg); err != nil {
			return fmt.Errorf("normalizing workspace names: %w", err)
		}
	}
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return err
	}
	rf, err := prepareRuntimeFilters(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()
	args := pushArgs(cfg, conflict, rf, dryRun)
	fmt.Fprintf(cfg.out(), "  Push: %s → %s (%s)\n", cfg.LocalPath, cfg.Target.RsyncDest(), cfg.Propagation)
	// A partial transfer must still finalize. Returning here would leave the
	// baseline stale, so every file that DID transfer comes back on the next
	// run as "mirror-only file is not in baseline", and the conflict set grows
	// until a clean push refuses outright - the exact failure this avoids.
	//
	// Finalizing is safe for a local target because RefreshBaseline walks the
	// MIRROR: a file that failed to transfer is absent there and gets retried.
	// An SSH baseline instead walks the local tree, so refreshing after a partial
	// push would falsely record unsent paths as peer-seen and could later turn a
	// local delete into deletion of independent peer work. Keep that baseline
	// unchanged on partial SSH pushes.
	var partial error
	if err := runRsync(ctx, runner, cfg, args); err != nil {
		if !IsPartialTransfer(err) {
			return err
		}
		partial = err
	}
	if partial != nil && cfg.Target.IsSSH() {
		return partial
	}
	if !dryRun && cfg.LocalPaths != nil {
		// Fast (stat-only) fingerprints: a strict pass would read every
		// payload file on the mirror, which forces cloud-placeholder
		// filesystems (Dropbox online-only files) to download the entire
		// tree. FingerprintsCompatible falls back to size+mtime when a
		// manifest entry carries no sha, so fast entries stay comparable.
		fullPeerPush := cfg.Target.IsSSH() && cfg.Profile == PeerProfile &&
			cfg.Propagation.Create && cfg.Propagation.Update
		partialPeerPolicy := cfg.Target.IsSSH() && cfg.Profile == PeerProfile && !fullPeerPush
		if !partialPeerPolicy {
			if err := RefreshBaseline(cfg, FingerprintFast); err != nil {
				return fmt.Errorf("baseline refresh: %w", err)
			}
			if fullPeerPush {
				if err := markPeerBaselineTarget(cfg); err != nil {
					return err
				}
			}
		}
		if err := UpdateLocalState(cfg.LocalPaths, func(s *LocalState) {
			s.LastPush = time.Now().UTC()
		}); err != nil {
			return fmt.Errorf("state update: %w", err)
		}
	}
	return partial
}

// Sync is now a thin alias for Push — the historical bidirectional Pull
// + Push behavior was retired in favor of previewed push and a separate Intake
// step (see `dot sync intake`). Kept as an entry point so callers keep
// working.
func Sync(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	return Push(ctx, runner, cfg, dryRun)
}

// FetchResult reports what Fetch did per requested path.
type FetchResult struct {
	Fetched []string // relpaths handed to rsync
	Missing []string // relpaths absent on the target (local targets only)
}

// Fetch restores specific files or directories from the target into the
// workspace — the on-demand entry point for a program, hook, or another
// machine that needs the binaries backing a particular path without running
// a full pull.
//
// Semantics per path: `--update --backup` — existing newer local files are
// never overwritten, overwrites are backed up under
// .sync-conflicts/<ts>/from-workspace/, nothing is ever deleted. The exclude
// layers (submodules, secrets unless allowed, junk, shared) still apply, so
// a fetch can never import .git or non-allowed secrets. Scoping uses an
// include chain anchored at the transfer root (parent dirs + `/rel/**`)
// instead of `--relative`, which Apple's openrsync does not honor — the
// anchored filter layers stay aligned with the workspace root this way.
//
// Paths missing on a local target are reported in Missing and skipped
// rather than failing the run. SSH targets cannot be pre-checked; rsync
// reports missing sources itself.
func Fetch(ctx context.Context, runner *exec.Runner, cfg *Config, rels []string, dryRun bool) (*FetchResult, error) {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return nil, err
	}
	rf, err := prepareRuntimeFilters(cfg)
	if err != nil {
		return nil, err
	}
	res := &FetchResult{}
	mirrorRoot := strings.TrimRight(cfg.MirrorPath, "/")
	var entries []fetchEntry
	for _, rel := range rels {
		norm := normalizeRel(rel)
		if norm == "" {
			continue
		}
		e := fetchEntry{rel: norm}
		if !cfg.Target.IsSSH() {
			info, err := os.Lstat(filepath.Join(mirrorRoot, norm))
			if err != nil {
				res.Missing = append(res.Missing, norm)
				continue
			}
			e.isDir = info.IsDir()
			e.known = true
		}
		entries = append(entries, e)
		res.Fetched = append(res.Fetched, norm)
	}
	if len(entries) == 0 {
		return res, nil
	}

	conflict := NewConflictDir()
	args := []string{
		"-a",
		"--human-readable",
		"--stats",
		"--no-links",
		"--update",
		"--backup",
		"--backup-dir=" + conflict.PullBackupRel(),
	}
	args = append(args, alwaysExcludeArgs()...)
	if rf.SubmodulesDyn != "" {
		args = append(args, "--exclude-from="+rf.SubmodulesDyn)
	}
	args = append(args, secretsFilterArgs(cfg.AllowPatterns)...)
	for _, f := range []string{cfg.ExcludesFile, cfg.IgnoreFile, rf.SharedDyn} {
		if f != "" {
			args = append(args, "--exclude-from="+f)
		}
	}
	args = append(args, fetchScopeArgs(entries)...)
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, rsyncTransportArgs(cfg)...)
	args = append(args, cfg.Target.RsyncDest(), cfg.LocalPath)
	fmt.Fprintf(cfg.out(), "  Fetch: %d path(s) %s → %s\n", len(res.Fetched), cfg.Target.RsyncDest(), cfg.LocalPath)
	if err := runRsync(ctx, runner, cfg, args); err != nil {
		return res, err
	}
	return res, nil
}

// fetchEntry is one requested fetch path with its target-side shape.
type fetchEntry struct {
	rel   string
	isDir bool
	known bool // isDir is reliable (local target only)
}

// fetchScopeArgs builds the scope layer for Fetch: after the exclude layers
// (so junk/secrets still win), include each requested path plus its literal
// parent dirs, then drop everything else. Anchored at the transfer root so
// the anchored exclude layers stay aligned; no `--relative`, which Apple's
// openrsync does not honor. For unknown shapes (ssh) both dir and file
// forms are emitted.
func fetchScopeArgs(entries []fetchEntry) []string {
	var args []string
	seenDirs := map[string]bool{}
	for _, e := range entries {
		segs := strings.Split(e.rel, "/")
		prefix := ""
		for _, seg := range segs[:len(segs)-1] {
			prefix += "/" + seg
			if !seenDirs[prefix] {
				seenDirs[prefix] = true
				args = append(args, "--include="+prefix+"/")
			}
		}
		if e.isDir || !e.known {
			args = append(args, "--include=/"+e.rel+"/", "--include=/"+e.rel+"/**")
		}
		if !e.isDir || !e.known {
			args = append(args, "--include=/"+e.rel)
		}
	}
	args = append(args, "--exclude=*")
	return args
}

// PullDirect runs a plain rsync pull (target → workspace, --update, with
// conflict backups). This is the pull path for SSH targets, where the
// baseline-driven PullTracked cannot walk the remote tree. Workspace-only
// files are never deleted; remote-newer files overwrite local with a backup
// under .sync-conflicts/.
func PullDirect(ctx context.Context, runner *exec.Runner, cfg *Config, dryRun bool) error {
	if err := ensureLogDir(cfg.LogFile); err != nil {
		return err
	}
	rf, err := prepareRuntimeFilters(cfg)
	if err != nil {
		return err
	}
	conflict := NewConflictDir()
	args := pullArgs(cfg, conflict, rf, dryRun)
	fmt.Fprintf(cfg.out(), "  Pull: %s → %s\n", cfg.Target.RsyncDest(), cfg.LocalPath)
	if err := runRsync(ctx, runner, cfg, args); err != nil {
		return err
	}
	if !dryRun && cfg.LocalPaths != nil {
		if err := UpdateLocalState(cfg.LocalPaths, func(s *LocalState) {
			s.LastPull = time.Now().UTC()
		}); err != nil {
			return fmt.Errorf("state update: %w", err)
		}
	}
	return nil
}

func runRsync(ctx context.Context, runner *exec.Runner, cfg *Config, args []string) error {
	var err error
	if cfg.Verbose {
		err = runner.RunAttached(ctx, "rsync", args...)
	} else {
		_, err = runner.Run(ctx, "rsync", args...)
	}
	return classifyRsyncError(err)
}

// PartialTransferError reports an rsync run that moved data but not all of it.
type PartialTransferError struct {
	Code int
	Err  error
}

func (e *PartialTransferError) Error() string {
	return fmt.Sprintf("rsync completed with partial results (exit %d); some files were skipped: %v", e.Code, e.Err)
}
func (e *PartialTransferError) Unwrap() error { return e.Err }

// IsPartialTransfer reports whether err is a partial-transfer outcome.
func IsPartialTransfer(err error) bool {
	var p *PartialTransferError
	return errors.As(err, &p)
}

// classifyRsyncError separates "moved nothing, something is broken" from
// "moved almost everything, a few files were skipped".
//
// rsync exit 23 is "partial transfer due to error" and 24 is "some files
// vanished before transfer" - both are routine on a live tree (a file deleted
// mid-run yields 24). Treating them as fatal is what made a real transfer abort
// after the first pass and silently skip the second one entirely, while
// reporting success. Callers that care can branch with IsPartialTransfer; the
// default is still a non-nil error so nothing is swept under the rug.
// ClassifyRsyncError is the exported form for callers outside this package that
// run their own rsync (the peer host-path pass).
func ClassifyRsyncError(err error) error { return classifyRsyncError(err) }

func classifyRsyncError(err error) error {
	if err == nil {
		return nil
	}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		switch ee.ExitCode() {
		case 23, 24:
			return &PartialTransferError{Code: ee.ExitCode(), Err: err}
		}
	}
	return err
}
