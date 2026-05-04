package cli

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/gdrivesync"
	"github.com/entelecheia/dotfiles-v2/internal/rsync"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newGdriveSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gdrive-sync",
		Short: "Push workspace to gdrive-workspace mirror via local rsync",
		Long: `Local-only rsync mirror between ~/workspace/work and the cloud-sync
client's mirror tree (default ~/gdrive-workspace/work). No SSH; the cloud
client itself handles upload/download to/from Drive (or Dropbox, etc.).

Workspace is authoritative for new local artifacts, while
.dotfiles/gdrive-sync/baseline.manifest is the Git-shared index for
Drive-backed payloads. Push sends local creates and updates to the mirror;
pull restores or updates baseline-tracked payloads from Drive. New
Drive-origin files still stage into inbox/gdrive for manual routing.

	Getting started:
	  dot gdrive-sync setup       Check rsync and disable managed schedulers by default
	  dot gdrive-sync migrate     One-time symlink → real-dir conversion + bring-down
	  dot gdrive-sync resume      Clear the paused gate after migrate verified
	  dot gdrive-sync             Default = push plan, then confirm

	Maintenance:
	  dot gdrive-sync status      Show last pull/push/intake, conflicts, paused state, scheduler
	  dot gdrive-sync conflicts   List timestamped backup directories
	  dot gdrive-sync pause       Stop managed schedulers + set paused gate
	  dot gdrive-sync resume      Clear paused gate and re-arm installed schedulers`,
		RunE:         runGdriveSync,
		SilenceUsage: true,
	}
	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rsync progress output")
	cmd.PersistentFlags().String("mode", gdrivesync.ModeManual.String(), "execution mode for push/pull: manual, clean, or force")
	cmd.AddCommand(
		newGdriveSyncSyncCmd(),
		newGdriveSyncPullCmd(),
		newGdriveSyncPushCmd(),
		newGdriveSyncIntakeCmd(),
		newGdriveSyncInboxCmd(),
		newGdriveSyncStatusCmd(),
		newGdriveSyncMigrateCmd(),
		newGdriveSyncConflictsCmd(),
		newGdriveSyncSetupCmd(),
		newGdriveSyncResumeCmd(),
		newGdriveSyncPauseCmd(),
		newGdriveSyncSharedCmd(),
		newGdriveSyncInitCmd(),
	)
	return cmd
}

// ── init ─────────────────────────────────────────────────────────────────

func newGdriveSyncInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize <workspace>/.dotfiles/gdrive-sync/ from current state",
		Long: `One-time onboarding for the per-workspace store. Creates
<workspace>/.dotfiles/gdrive-sync/ with config.yaml, exclude.txt, ignore.txt,
manifests, log dir; appends '/.dotfiles/' to <workspace>/.gitignore so the
store is never committed; and creates <workspace>/inbox/gdrive/ if missing.

Idempotent — re-running on a populated store leaves operator edits intact and
just heals any missing pieces.`,
		RunE:         runGdriveSyncInit,
		SilenceUsage: true,
	}
}

func runGdriveSyncInit(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	paths := cfg.LocalPaths
	if paths == nil {
		return fmt.Errorf("local paths unresolved — bug in ResolveConfig")
	}

	// gdriveBootstrap already triggered LoadOrMigrateLocalConfig, so the
	// .dotfiles/gdrive-sync/ tree exists by the time we get here. Heal
	// anything missing (operator may have deleted files) and create the
	// inbox/gdrive staging dir.
	if err := gdrivesync.EnsureLocalLayout(paths); err != nil {
		return fmt.Errorf("ensure layout: %w", err)
	}
	inboxGdrive := stripTrailingSlash(cfg.LocalPath) + "/inbox/gdrive"
	if err := os.MkdirAll(inboxGdrive, 0755); err != nil {
		return fmt.Errorf("create inbox/gdrive: %w", err)
	}

	p.Header("gdrive-sync workspace initialized")
	p.KV("Store", paths.StoreDir)
	p.KV("Workspace", stripTrailingSlash(cfg.LocalPath))
	p.KV("Mirror", stripTrailingSlash(cfg.MirrorPath))
	p.KV("Propagation", cfg.Propagation.String())
	p.KV("Inbox staging", inboxGdrive)
	p.Blank()
	p.Line("Edit %s to customize behavior; %s for additional ignore patterns.", paths.ConfigFile, paths.IgnoreFile)
	p.Line("Run 'dot gdrive-sync setup' to verify rsync and keep automatic sync disabled unless intervals are passed.")
	return nil
}

// gdriveBootstrap loads state + resolved config + a runner for any
// gdrive-sync subcommand. Mirrors syncBootstrap idiom in sync_cmd.go.
func gdriveBootstrap(cmd *cobra.Command) (*config.UserState, *gdrivesync.Config, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := gdrivesync.ResolveConfig(state)
	if err != nil {
		return nil, nil, nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	return state, cfg, runner, nil
}

func gdriveBootstrapReadOnly(cmd *cobra.Command) (*config.UserState, *gdrivesync.Config, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := gdrivesync.ResolveConfigReadOnly(state)
	if err != nil {
		return nil, nil, nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	return state, cfg, runner, nil
}

// gdriveScheduler builds a Scheduler bound to the same runner+cfg used
// elsewhere in the gdrive-sync subcommands. Returns the Paths used so
// callers can introspect plist/timer locations.
func gdriveScheduler(cfg *gdrivesync.Config, runner *exec.Runner) (*gdrivesync.Scheduler, *gdrivesync.Paths, error) {
	paths, err := gdrivesync.ResolvePaths()
	if err != nil {
		return nil, nil, err
	}
	return gdrivesync.NewScheduler(runner, paths, cfg, template.NewEngine()), paths, nil
}

// gdrivePreflight validates that sync can proceed. The bypass flags let
// admin-style commands operate when sync would normally refuse:
//
//	bypassPause    — true for `migrate` (paused state is fine; migrate is the activation step)
//	bypassMigGate  — true for `migrate` (legacy symlinks are exactly what it converts)
func gdrivePreflight(p *Printer, cfg *gdrivesync.Config, runner *exec.Runner, state *config.UserState, bypassPause, bypassMigGate bool) bool {
	if !runner.CommandExists("rsync") {
		p.Line("rsync not installed. Install via: brew install rsync")
		return false
	}
	if !runner.IsDir(cfg.LocalPath) {
		p.Line("Local path missing: %s", cfg.LocalPath)
		return false
	}
	if !runner.IsDir(cfg.MirrorPath) {
		p.Line("Mirror path missing: %s", cfg.MirrorPath)
		return false
	}
	if !bypassPause && cfg.Paused {
		p.Line("gdrive-sync is paused. Run `dot gdrive-sync resume` to activate.")
		return false
	}
	if !bypassMigGate && gdrivesync.HasPendingMigration(stripTrailingSlash(cfg.LocalPath)) {
		p.Line("Legacy symlinks (.gdrive / inbox/downloads / inbox/incoming) still present.")
		p.Line("Run `dot gdrive-sync migrate` first to convert them to real directories.")
		return false
	}
	return true
}

// recordSyncResult updates the on-disk log after a sync operation. Runtime
// timestamps now live in the workspace-local gdrive-sync state file.
func recordSyncResult(state *config.UserState, cfg *gdrivesync.Config, op string, syncErr error, dryRun bool) {
	_ = state
	if dryRun {
		return
	}
	exitCode := 0
	if syncErr != nil {
		exitCode = 1
	}
	gdrivesync.AppendLog(cfg.LogFile, op, exitCode)
	gdrivesync.RotateLog(cfg.LogFile, 2000, 1000)

}

// ── sync (root default + explicit subcommand) ────────────────────────────

func newGdriveSyncSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "sync",
		Short:        "Alias for `push` (kept for back-compat; prefer `dot gdrive-sync push`)",
		RunE:         runGdriveSync,
		SilenceUsage: true,
	}
}

// runGdriveSync is the handler for both the bare `dot gdrive-sync` and
// the explicit `sync` subcommand. The historical Pull+Push semantics
// were retired; this is now a thin alias for push that prints a one-line
// deprecation hint so callers gradually migrate to the new name.
func runGdriveSync(cmd *cobra.Command, args []string) error {
	printerFrom(cmd).Line("(note: `sync` is now an alias for `push`; use `dot gdrive-sync pull` for baseline-tracked Drive payloads)")
	return runGdriveSyncPush(cmd, args)
}

// ── pull ─────────────────────────────────────────────────────────────────

func newGdriveSyncPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Restore/update baseline-tracked Drive payloads into the workspace",
		Long: `Pull applies Drive-side changes only for paths listed in
.dotfiles/gdrive-sync/baseline.manifest. Baseline is expected to be tracked in
Git, so a second machine can git pull the index and then restore binary
payloads from the Google Drive mirror.

Files absent from baseline are not copied into the workspace by pull; run
intake to stage new Drive-origin files under inbox/gdrive/<ts>/ for manual
review. If local and Drive both changed a baseline-tracked file, manual mode
asks before applying, clean mode aborts, and force mode overwrites local after
backing up the local version into .sync-conflicts/<ts>/from-workspace/.`,
		RunE:         runGdriveSyncPull,
		SilenceUsage: true,
	}
	cmd.Flags().Bool("strict", false, "accepted for compatibility; pull uses sha256 baseline fingerprints")
	return cmd
}

func runGdriveSyncPull(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gdrivePreflight(p, cfg, runner, state, false, false) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	mode, err := gdriveSyncModeFrom(cmd)
	if err != nil {
		return err
	}

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pull plan for baseline-tracked payloads %s → %s (%s)", cfg.MirrorPath, cfg.LocalPath, mode)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	plan, err := gdrivesync.PullTracked(cfg, gdrivesync.PullOptions{DryRun: true})
	if err != nil {
		return fmt.Errorf("planning pull: %w", err)
	}
	printPullPlan(p, cfg, plan)
	if dryRun || !plan.HasChanges() {
		return nil
	}
	if mode == gdrivesync.ModeClean && len(plan.Conflicts) > 0 {
		return fmt.Errorf("pull refused: %d conflict(s); rerun with --mode=force to overwrite with backups", len(plan.Conflicts))
	}
	force := mode == gdrivesync.ModeForce
	if mode == gdrivesync.ModeManual {
		yes, _ := cmd.Flags().GetBool("yes")
		confirmed, err := ui.Confirm("Apply this pull plan?", yes)
		if err != nil {
			return err
		}
		if !confirmed {
			p.Line("Aborted.")
			return nil
		}
		force = len(plan.Conflicts) > 0
	}
	res, err := gdrivesync.PullTracked(cfg, gdrivesync.PullOptions{Force: force})
	recordSyncResult(state, cfg, "pull", err, false)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}
	printPullResult(p, cfg, res)
	return nil
}

// ── intake ───────────────────────────────────────────────────────────────

func newGdriveSyncIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intake",
		Short: "Stage new GDrive-origin files for manual routing",
		Long: `Compares the mirror against baseline.manifest and imports.manifest to
find new Drive-origin files. New candidates are copied into a timestamped
subdirectory of <local>/inbox/gdrive/<intake-ts>/ for the operator to review
and route.

Changed baseline-tracked files are skipped and left for ` + "`dot gdrive-sync pull`" + `.
Mirror-side deletions against baseline are detected by pull, not intake.

  --strict   Use sha256 fingerprints (catches content changes that
             preserve mtime). Default is fast size+mtime mode.`,
		RunE:         runGdriveSyncIntake,
		SilenceUsage: true,
	}
	cmd.Flags().Bool("strict", false, "use sha256 fingerprints instead of size+mtime")
	return cmd
}

func runGdriveSyncIntake(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gdrivePreflight(p, cfg, runner, state, false, false) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	strict, _ := cmd.Flags().GetBool("strict")
	if _, err := gdriveSyncModeFrom(cmd); err != nil {
		return err
	}

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	mode := "fast"
	if strict {
		mode = "strict"
	}
	p.Line("Intaking %s → %s/inbox/gdrive/<ts>/ (%s mode)", cfg.MirrorPath, stripTrailingSlash(cfg.LocalPath), mode)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	res, err := gdrivesync.Intake(cmd.Context(), runner, cfg, gdrivesync.IntakeOptions{
		Strict: strict,
		DryRun: dryRun,
	})
	if err != nil {
		return fmt.Errorf("intake failed: %w", err)
	}

	printPullResult(p, cfg, res.Pull)
	if res.StagingDir != "" {
		p.Line("  ✓ %d intaked into %s", len(res.Intaked), res.StagingDir)
	} else {
		p.Line("  %d intaked", len(res.Intaked))
	}
	p.Line("  %d skipped (baseline match)", len(res.SkippedBase))
	if len(res.SkippedTracked) > 0 {
		p.Line("  %d skipped (tracked conflict/unresolved)", len(res.SkippedTracked))
	}
	p.Line("  %d skipped (imports match)", len(res.SkippedImports))
	return nil
}

func printPullResult(p *Printer, cfg *gdrivesync.Config, res *gdrivesync.PullResult) {
	if res == nil {
		return
	}
	p.Line("  %d pulled (%d restored)", len(res.Pulled), len(res.Restored))
	if len(res.LocalModified) > 0 {
		p.Line("  %d local-modified tracked files left for push", len(res.LocalModified))
	}
	if len(res.Conflicts) > 0 {
		p.Line("  %d tracked conflicts — Drive copies saved under %s", len(res.Conflicts),
			filepath.Join(stripTrailingSlash(cfg.LocalPath), ".sync-conflicts"))
	}
	if len(res.Tombstones) > 0 {
		p.Line("  %d tombstones recorded — see %s", len(res.Tombstones), cfg.LocalPaths.TombstonesFile)
	}
}

func printPullPlan(p *Printer, cfg *gdrivesync.Config, res *gdrivesync.PullResult) {
	if res == nil {
		return
	}
	updates := differenceStrings(res.Pulled, res.Restored)
	affected := affectedDirsFromLists(res.Pulled, res.Restored, res.LocalModified, pullConflictPaths(res.Conflicts), tombstonePaths(res.Tombstones))
	if len(affected) > 0 {
		p.Section("Affected folders")
		printPathList(p, affected)
	}
	if len(updates) > 0 {
		p.Section(fmt.Sprintf("Updates from Drive: %d", len(updates)))
		printPathList(p, updates)
	}
	if len(res.Restored) > 0 {
		p.Section(fmt.Sprintf("Restores from Drive: %d", len(res.Restored)))
		printPathList(p, res.Restored)
	}
	if len(res.LocalModified) > 0 {
		p.Section(fmt.Sprintf("Local-only changes: %d", len(res.LocalModified)))
		printPathList(p, res.LocalModified)
	}
	if len(res.Conflicts) > 0 {
		p.Section(fmt.Sprintf("Conflicts: %d", len(res.Conflicts)))
		for _, c := range res.Conflicts {
			reason := c.Reason
			if reason == "" {
				reason = "local and Drive both changed"
			}
			p.Line("  !  %s — %s", c.RelPath, reason)
			if c.BackupPath != "" {
				p.Line("     backup: %s", c.BackupPath)
			}
		}
	}
	if len(res.Tombstones) > 0 {
		p.Section(fmt.Sprintf("Mirror deletions: %d", len(res.Tombstones)))
		printPathList(p, tombstonePaths(res.Tombstones))
		p.Line("  tombstones: %s", cfg.LocalPaths.TombstonesFile)
	}
	if len(affected) == 0 && len(res.LocalModified) == 0 {
		p.Line("  No pull changes.")
	}
}

// ── inbox (list / forget / clear) ────────────────────────────────────────

func newGdriveSyncInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Inspect and manage the GDrive intake staging area",
		Long: `View what's staged + tracked under .dotfiles/gdrive-sync/, force a
re-intake of one path, or clear the imports + tombstones manifests
entirely.

  dot gdrive-sync inbox                  # alias for list
  dot gdrive-sync inbox list
  dot gdrive-sync inbox forget <relpath> # next intake re-stages this path
  dot gdrive-sync inbox clear            # empty imports + tombstones`,
		RunE: runGdriveSyncInboxList,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show staged run-dirs, imports manifest entries, and tombstones",
			RunE:  runGdriveSyncInboxList,
		},
		&cobra.Command{
			Use:          "forget <relpath>",
			Short:        "Drop a path from imports.manifest so the next intake re-stages it",
			Args:         cobra.ExactArgs(1),
			RunE:         runGdriveSyncInboxForget,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "clear",
			Short:        "Empty imports.manifest and tombstones.log",
			RunE:         runGdriveSyncInboxClear,
			SilenceUsage: true,
		},
	)
	return cmd
}

func runGdriveSyncInboxList(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gdriveBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	p := printerFrom(cmd)

	stagingRoot := stripTrailingSlash(cfg.LocalPath) + "/inbox/gdrive"
	runDirs, _ := os.ReadDir(stagingRoot)
	dirCount := 0
	totalFiles := 0
	for _, e := range runDirs {
		if !e.IsDir() {
			continue
		}
		dirCount++
		_ = filepath.WalkDir(filepath.Join(stagingRoot, e.Name()), func(_ string, d fs.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				totalFiles++
			}
			return nil
		})
	}

	imports, err := gdrivesync.LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	if err != nil {
		return fmt.Errorf("loading imports: %w", err)
	}
	tomb, err := gdrivesync.LoadTombstones(cfg.LocalPaths.TombstonesFile)
	if err != nil {
		return fmt.Errorf("loading tombstones: %w", err)
	}

	p.Header("gdrive-sync inbox")
	p.KV("Staging root", stagingRoot)
	p.KV("Pending run-dirs", fmt.Sprintf("%d (%d files)", dirCount, totalFiles))
	p.KV("Imports manifest", fmt.Sprintf("%d entries", len(imports)))
	p.KV("Tombstones", fmt.Sprintf("%d entries", len(tomb)))
	if len(tomb) > 0 {
		p.Section("Recent tombstones (newest 5):")
		shown := tomb
		if len(shown) > 5 {
			shown = shown[len(shown)-5:]
		}
		for _, t := range shown {
			p.Bullet("•", fmt.Sprintf("%s (detected %s)", t.RelPath, t.DetectedAt.Format(time.RFC3339)))
		}
	}
	p.Blank()
	return nil
}

func runGdriveSyncInboxForget(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	rel := strings.TrimSpace(args[0])
	if rel == "" {
		return fmt.Errorf("relpath cannot be empty")
	}
	dropped, err := gdrivesync.ForgetImport(cfg.LocalPaths, rel)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if dropped {
		p.Line("✓ forgot %q — next intake will re-stage it if mirror still has it", rel)
	} else {
		p.Line("no entry for %q in imports.manifest — nothing to forget", rel)
	}
	return nil
}

func runGdriveSyncInboxClear(cmd *cobra.Command, _ []string) error {
	state, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	yes, _ := cmd.Flags().GetBool("yes")
	imports, _ := gdrivesync.LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	tomb, _ := gdrivesync.LoadTombstones(cfg.LocalPaths.TombstonesFile)
	p := printerFrom(cmd)
	if len(imports) == 0 && len(tomb) == 0 {
		p.Line("imports.manifest and tombstones.log are already empty.")
		return nil
	}
	confirmed, err := ui.Confirm(fmt.Sprintf("Clear %d imports + %d tombstones? Next intake will re-stage anything still on mirror.", len(imports), len(tomb)), yes)
	if err != nil {
		return err
	}
	if !confirmed {
		p.Line("Aborted.")
		return nil
	}
	if err := gdrivesync.ClearImportsAndTombstones(cfg.LocalPaths); err != nil {
		return err
	}
	p.Line("✓ cleared %d imports + %d tombstones.", len(imports), len(tomb))
	_ = state
	return nil
}

// ── push ─────────────────────────────────────────────────────────────────

func newGdriveSyncPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Preview and send workspace changes to mirror under a propagation policy",
		Long: `Push the workspace tree to the gdrive mirror under a propagation
policy. The default policy '{create:true, update:true, delete:false}'
copies new and modified files but never deletes mirror-side content. By default
push prints the upload plan and asks before applying.

Flag --propagate= takes a comma-separated allowlist; absent items are
disabled. Examples:

  dot gdrive-sync push                              # preview, then confirm
  dot gdrive-sync push --mode=clean                 # apply only if no conflicts
  dot gdrive-sync push --mode=force                 # overwrite with backups
  dot gdrive-sync push --propagate=create,update,delete   # full sync
  dot gdrive-sync push --propagate=create           # additive only
  dot gdrive-sync push --propagate=update           # in-place updates only

The per-workspace store (.dotfiles/) and intake staging area
(inbox/gdrive/) are always excluded so they never round-trip to mirror.`,
		RunE:         runGdriveSyncPush,
		SilenceUsage: true,
	}
	cmd.Flags().String("propagate", "", "comma-separated allowlist of propagation kinds (create,update,delete)")
	return cmd
}

func runGdriveSyncPush(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if cmd.Flags().Changed("propagate") {
		raw, _ := cmd.Flags().GetString("propagate")
		policy, err := parsePropagateFlag(raw)
		if err != nil {
			return fmt.Errorf("--propagate: %w", err)
		}
		cfg.Propagation = policy
	}

	if !gdrivePreflight(p, cfg, runner, state, false, false) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	mode, err := gdriveSyncModeFrom(cmd)
	if err != nil {
		return err
	}

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Push plan for %s → %s (%s, mode=%s)", cfg.LocalPath, cfg.MirrorPath, cfg.Propagation, mode)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	plan, err := gdrivesync.PlanPush(cfg)
	if err != nil {
		return fmt.Errorf("planning push: %w", err)
	}
	printPushPlan(p, plan)
	if dryRun || (!plan.HasChanges() && !plan.HasConflicts()) {
		return nil
	}
	if mode == gdrivesync.ModeClean && plan.HasConflicts() {
		return fmt.Errorf("push refused: %d conflict(s); rerun with --mode=force to overwrite with backups", len(plan.Conflicts))
	}
	if mode == gdrivesync.ModeManual {
		yes, _ := cmd.Flags().GetBool("yes")
		confirmed, err := ui.Confirm("Apply this push plan?", yes)
		if err != nil {
			return err
		}
		if !confirmed {
			p.Line("Aborted.")
			return nil
		}
	}
	pushErr := gdrivesync.Push(cmd.Context(), runner, cfg, false)
	recordSyncResult(state, cfg, "push", pushErr, false)
	if pushErr != nil {
		return fmt.Errorf("push failed: %w", pushErr)
	}
	p.Line("✓ Push complete.")
	return nil
}

func printPushPlan(p *Printer, plan *gdrivesync.PushPlan) {
	if plan == nil {
		return
	}
	affected := affectedDirsFromLists(plan.Creates, plan.Updates, plan.Deletes, pushConflictPaths(plan.Conflicts))
	if len(affected) > 0 {
		p.Section("Affected folders")
		printPathList(p, affected)
	}
	if len(plan.Creates) > 0 {
		p.Section(fmt.Sprintf("Uploads: %d", len(plan.Creates)))
		printPathList(p, plan.Creates)
	}
	if len(plan.Updates) > 0 {
		p.Section(fmt.Sprintf("Updates: %d", len(plan.Updates)))
		printPathList(p, plan.Updates)
	}
	if len(plan.Deletes) > 0 {
		p.Section(fmt.Sprintf("Deletes: %d", len(plan.Deletes)))
		printPathList(p, plan.Deletes)
	}
	if len(plan.SkippedPolicy) > 0 {
		p.Section(fmt.Sprintf("Skipped by propagation policy: %d", len(plan.SkippedPolicy)))
		printPathList(p, plan.SkippedPolicy)
	}
	if len(plan.Conflicts) > 0 {
		p.Section(fmt.Sprintf("Drive conflicts: %d", len(plan.Conflicts)))
		for _, c := range plan.Conflicts {
			reason := c.Reason
			if reason == "" {
				reason = "local and mirror differ"
			}
			p.Line("  !  %s — %s", c.RelPath, reason)
		}
	}
	if len(affected) == 0 && len(plan.SkippedPolicy) == 0 {
		p.Line("  No push changes.")
	}
}

// parsePropagateFlag parses the --propagate= comma-separated allowlist.
// Empty (after split + trim) is rejected — there's no meaningful rsync
// invocation that does nothing.
func parsePropagateFlag(value string) (gdrivesync.PropagationPolicy, error) {
	var p gdrivesync.PropagationPolicy
	seen := map[string]bool{}
	nonEmpty := 0
	for _, raw := range strings.Split(value, ",") {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		nonEmpty++
		if seen[v] {
			return p, fmt.Errorf("duplicate token %q", v)
		}
		seen[v] = true
		switch v {
		case "create":
			p.Create = true
		case "update":
			p.Update = true
		case "delete":
			p.Delete = true
		default:
			return p, fmt.Errorf("unknown token %q (want create|update|delete)", v)
		}
	}
	if nonEmpty == 0 {
		return p, fmt.Errorf("must list at least one of create,update,delete")
	}
	return p, nil
}

func gdriveSyncModeFrom(cmd *cobra.Command) (gdrivesync.RunMode, error) {
	raw, _ := cmd.Flags().GetString("mode")
	mode, err := gdrivesync.ParseRunMode(raw)
	if err != nil {
		return "", fmt.Errorf("--mode: %w", err)
	}
	return mode, nil
}

func printPathList(p *Printer, paths []string) {
	for _, path := range paths {
		p.Line("  -  %s", path)
	}
}

func affectedDirsFromLists(groups ...[]string) []string {
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, rel := range group {
			dir := filepath.ToSlash(filepath.Dir(rel))
			if dir == "." || dir == "/" {
				dir = "."
			}
			seen[dir] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func differenceStrings(all, subtract []string) []string {
	if len(all) == 0 {
		return nil
	}
	remove := map[string]struct{}{}
	for _, s := range subtract {
		remove[s] = struct{}{}
	}
	out := make([]string, 0, len(all))
	for _, s := range all {
		if _, ok := remove[s]; ok {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func pullConflictPaths(conflicts []gdrivesync.PullConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.RelPath)
	}
	sort.Strings(out)
	return out
}

func pushConflictPaths(conflicts []gdrivesync.PushConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.RelPath)
	}
	sort.Strings(out)
	return out
}

func tombstonePaths(tombstones []gdrivesync.Tombstone) []string {
	out := make([]string, 0, len(tombstones))
	for _, t := range tombstones {
		out = append(out, t.RelPath)
	}
	sort.Strings(out)
	return out
}

// ── status ───────────────────────────────────────────────────────────────

func newGdriveSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show local↔mirror sync status",
		RunE:  runGdriveSyncStatus,
	}
}

func runGdriveSyncStatus(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	sched, _, err := gdriveScheduler(cfg, runner)
	if err != nil {
		return err
	}
	st, err := gdrivesync.GetStatus(cmd.Context(), runner, cfg, state, sched)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	p.Header("Gdrive Sync Status")

	if st.RsyncVersion != "" {
		p.KV("rsync", st.RsyncVersion)
	} else {
		p.KV("rsync", "not installed")
	}
	p.KV("Local", st.LocalPath)
	p.KV("Mirror", st.MirrorPath)
	if st.StoreDir != "" {
		p.KV("Config", st.StoreDir)
	}
	p.KV("Local exists", boolStr(st.LocalExists))
	p.KV("Mirror exists", boolStr(st.MirrorExists))
	if st.Paused {
		p.KV("Paused", "yes — run `dot gdrive-sync resume` to activate")
	} else {
		p.KV("Paused", "no")
	}
	p.KV("Propagation", st.Propagation.String())
	if st.Interval > 0 {
		p.KV("Push interval", formatInterval(st.Interval))
		p.KV("Push mode", st.PushMode.String())
		p.KV("Push scheduler", st.SchedulerState.String())
	} else {
		p.KV("Push scheduler", "(off — `dot gdrive-sync setup --push-interval=DUR` to enable)")
	}
	if st.PullInterval > 0 {
		p.KV("Pull interval", formatInterval(st.PullInterval))
		p.KV("Pull mode", st.PullMode.String())
		p.KV("Pull scheduler", st.IntakeSchedulerState.String())
	} else {
		p.KV("Pull scheduler", "(off — `dot gdrive-sync setup --pull-interval=DUR` to enable)")
	}
	if st.Propagation.Delete {
		p.KV("Max delete", fmt.Sprintf("%d", st.MaxDelete))
	}
	p.KV("Lock held", boolStr(st.LockHeld))
	p.KV("Last pull", formatLastSync(st.LastPull))
	p.KV("Last push", formatLastSync(st.LastPush))
	p.KV("Last intake", formatLastSync(st.LastIntake))
	if st.LastIntakeTSDir != "" {
		p.KV("Last intake dir", st.LastIntakeTSDir)
	}

	if len(st.Conflicts) > 0 {
		p.Section(fmt.Sprintf("Conflicts: %d backup directories", len(st.Conflicts)))
		now := time.Now()
		for _, c := range st.Conflicts {
			age := now.Sub(c.ModTime).Truncate(time.Hour)
			p.Bullet("•", fmt.Sprintf("%s (%s ago)", c.Timestamp, age))
		}
	}
	if n := len(st.Shared); n > 0 {
		auto, manual := 0, 0
		for _, e := range st.Shared {
			if e.Reason == gdrivesync.SharedManual {
				manual++
			} else {
				auto++
			}
		}
		p.KV("Shared", fmt.Sprintf("%d entries (%d auto, %d manual) — see `dot gdrive-sync shared`", n, auto, manual))
	}
	p.Blank()
	return nil
}

func formatLastSync(t time.Time) string {
	if t.IsZero() {
		return "(never)"
	}
	ago := time.Since(t).Truncate(time.Second)
	return fmt.Sprintf("%s ago", ago)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ── migrate ──────────────────────────────────────────────────────────────

func newGdriveSyncMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "One-shot: convert legacy symlinks + pull mirror into workspace",
		Long: `Idempotent migration step. Removes the dual-path symlinks (.gdrive,
inbox/downloads, inbox/incoming), creates real directories where needed,
and runs an additive (no --delete) rsync pull from the mirror to seed the
workspace. Leaves Paused=true so the operator verifies before activating.`,
		RunE:         runGdriveSyncMigrate,
		SilenceUsage: true,
	}
}

func runGdriveSyncMigrate(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	// Migrate legitimately operates against both a paused tree and one with pending symlinks
	// — it's the activation step that fixes both.
	if !gdrivePreflight(p, cfg, runner, state, true, true) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	if dryRun {
		p.Line("(dry-run — no changes)")
	}
	if err := gdrivesync.Migrate(cmd.Context(), runner, cfg, state, gdrivesync.MigrateOptions{DryRun: dryRun}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// ── conflicts ────────────────────────────────────────────────────────────

func newGdriveSyncConflictsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "conflicts",
		Short: "List .sync-conflicts/ backup directories",
		RunE:  runGdriveSyncConflicts,
	}
}

func runGdriveSyncConflicts(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	confs, err := gdrivesync.ListConflicts(stripTrailingSlash(cfg.LocalPath))
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if len(confs) == 0 {
		p.Line("No conflict backups under %s/.sync-conflicts/", stripTrailingSlash(cfg.LocalPath))
		return nil
	}
	p.Header(fmt.Sprintf("Conflict backups under %s/.sync-conflicts/", stripTrailingSlash(cfg.LocalPath)))
	now := time.Now()
	for _, c := range confs {
		age := now.Sub(c.ModTime).Truncate(time.Hour)
		marker := "•"
		if age > 30*24*time.Hour {
			marker = "▲" // older than 30 days — candidate for cleanup
		}
		p.Bullet(marker, fmt.Sprintf("%s (%s ago) — %s", c.Timestamp, age, c.Path))
	}
	p.Blank()
	return nil
}

// ── pause / resume ───────────────────────────────────────────────────────

func newGdriveSyncResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "resume",
		Short:        "Clear the Paused gate so pull/push/sync can run",
		RunE:         runGdriveSyncResume,
		SilenceUsage: true,
	}
}

func runGdriveSyncResume(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if cfg.Paused {
		if err := setLocalPaused(cfg, false); err != nil {
			return fmt.Errorf("saving local config: %w", err)
		}
		p.Line("✓ gdrive-sync resumed.")
	} else {
		p.Line("gdrive-sync was not paused.")
	}

	if cfg.Interval == 0 && cfg.PullInterval == 0 {
		p.Line("scheduler remains off — run `dot gdrive-sync setup --push-interval=DUR` or `--pull-interval=DUR` to enable.")
		return nil
	}
	// If the scheduler is configured and installed, reattach it so periodic runs resume.
	sched, _, err := gdriveScheduler(cfg, runner)
	if err != nil {
		return nil // state save succeeded; scheduler is best-effort
	}
	if sched.State(cmd.Context()) != gdrivesync.SchedulerNotInstalled {
		if err := sched.Resume(cmd.Context()); err != nil {
			p.Warn("scheduler resume failed: %v", err)
		} else {
			p.Line("✓ scheduler resumed.")
		}
	}
	return nil
}

func newGdriveSyncPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pause",
		Short:        "Set the Paused gate so pull/push/sync refuse to run",
		RunE:         runGdriveSyncPause,
		SilenceUsage: true,
	}
}

func runGdriveSyncPause(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if !cfg.Paused {
		if err := setLocalPaused(cfg, true); err != nil {
			return fmt.Errorf("saving local config: %w", err)
		}
		p.Line("✓ gdrive-sync paused.")
	} else {
		p.Line("gdrive-sync was already paused.")
	}

	// Stop the scheduler if installed so we don't waste invocations
	// hitting the paused gate every Interval seconds.
	sched, _, err := gdriveScheduler(cfg, runner)
	if err != nil {
		return nil
	}
	if sched.State(cmd.Context()) == gdrivesync.SchedulerRunning {
		if err := sched.Pause(cmd.Context()); err != nil {
			p.Warn("scheduler pause failed: %v", err)
		} else {
			p.Line("✓ scheduler stopped.")
		}
	}
	return nil
}

// ── setup ────────────────────────────────────────────────────────────────

func newGdriveSyncSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install rsync (if missing) and manage opt-in gdrive-sync schedulers",
		Long: `One-time setup. Verifies rsync is available (offers to install via brew/apt
if not), then configures the platform's user-scheduler (launchd LaunchAgent on
macOS, systemd user-timer on Linux). Automatic sync is off by default; pass an
interval flag to opt in.

  --push-interval=DUR    Deploy automatic ` + "`dot gdrive-sync push --mode=MODE`" + `.
  --pull-interval=DUR    Deploy automatic ` + "`dot gdrive-sync pull --mode=MODE`" + `.
  --push-mode=MODE       Automatic push mode: clean or force (default clean).
  --pull-mode=MODE       Automatic intake mode: clean or force (default clean).

Idempotent — re-run safely after an interval change to reload the unit.`,
		RunE:         runGdriveSyncSetup,
		SilenceUsage: true,
	}
	cmd.Flags().String("push-interval", "", "deploy push scheduler at this cadence (e.g. 15m, 1h, 0 to remove)")
	cmd.Flags().String("pull-interval", "", "deploy pull scheduler at this cadence (e.g. 15m, 1h, 0 to remove)")
	cmd.Flags().String("push-mode", gdrivesync.ModeClean.String(), "automatic push mode: clean or force")
	cmd.Flags().String("pull-mode", gdrivesync.ModeClean.String(), "automatic intake mode: clean or force")
	return cmd
}

func runGdriveSyncSetup(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	ctx := cmd.Context()
	p := printerFrom(cmd)

	if dryRun {
		p.Line("(dry-run — no changes)")
	}

	pushInterval, pullInterval := 0, 0
	if cmd.Flags().Changed("push-interval") {
		raw, _ := cmd.Flags().GetString("push-interval")
		seconds, err := parseIntervalFlag(raw)
		if err != nil {
			return fmt.Errorf("--push-interval: %w", err)
		}
		pushInterval = seconds
	}
	if cmd.Flags().Changed("pull-interval") {
		raw, _ := cmd.Flags().GetString("pull-interval")
		seconds, err := parseIntervalFlag(raw)
		if err != nil {
			return fmt.Errorf("--pull-interval: %w", err)
		}
		pullInterval = seconds
	}
	pushModeRaw, _ := cmd.Flags().GetString("push-mode")
	pushMode, err := parseAutomaticModeFlag(pushModeRaw)
	if err != nil {
		return fmt.Errorf("--push-mode: %w", err)
	}
	pullModeRaw, _ := cmd.Flags().GetString("pull-mode")
	pullMode, err := parseAutomaticModeFlag(pullModeRaw)
	if err != nil {
		return fmt.Errorf("--pull-mode: %w", err)
	}
	if err := setLocalSchedule(cfg, pushInterval, pullInterval, pushMode, pullMode, dryRun); err != nil {
		return fmt.Errorf("saving scheduler config: %w", err)
	}

	// 1. Check / install rsync
	p.Line("Checking rsync...")
	ver, ok := rsync.CheckRsync(runner)
	if ok {
		p.Line("  ✓ rsync installed (%s)", ver)
	} else {
		confirmed, err := ui.Confirm("rsync not found. Install it?", yes)
		if err != nil {
			return err
		}
		if !confirmed {
			p.Line("Aborted.")
			return nil
		}
		if err := rsync.InstallRsync(ctx, runner); err != nil {
			return fmt.Errorf("installing rsync: %w", err)
		}
		ver, ok = rsync.CheckRsync(runner)
		if !ok {
			return fmt.Errorf("rsync not found in PATH after install")
		}
		p.Line("  ✓ rsync installed (%s)", ver)
	}

	// 2. Deploy scheduler(s) only when explicitly enabled.
	p.Line("Configuring opt-in scheduler...")
	sched, paths, err := gdriveScheduler(cfg, runner)
	if err != nil {
		return err
	}
	if err := sched.Install(ctx); err != nil {
		return fmt.Errorf("installing scheduler: %w", err)
	}
	if cfg.Interval > 0 {
		p.Line("  ✓ push unit installed (interval: %s, mode: %s)", formatInterval(cfg.Interval), cfg.PushMode)
		p.Line("  unit: %s", scheduleUnitLabel(paths))
	} else {
		p.Line("  (push scheduler off — pass --push-interval=DUR to enable)")
	}
	if cfg.PullInterval > 0 {
		p.Line("  ✓ pull unit installed (interval: %s, mode: %s)", formatInterval(cfg.PullInterval), cfg.PullMode)
	} else {
		p.Line("  (pull scheduler off — pass --pull-interval=DUR to enable)")
	}
	p.Line("  log:  %s", cfg.LogFile)

	p.Blank()
	p.Line("✓ gdrive-sync setup complete.")
	if cfg.Paused {
		p.Line("  Paused gate is set — run `dot gdrive-sync resume` to start syncing.")
	} else {
		p.Line("  Run `dot gdrive-sync push` or `dot gdrive-sync pull` when you want to sync manually.")
	}
	return nil
}

// parseIntervalFlag accepts a Go duration string ("15m", "1h"),
// a bare integer (seconds), or "0" to disable. Returns seconds.
func parseIntervalFlag(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, nil
	}
	var seconds int
	// Try Go duration first (handles "15m", "1h", "30s").
	if d, err := time.ParseDuration(raw); err == nil {
		seconds = int(d.Seconds())
	} else {
		// Bare integer fallback.
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil {
			return 0, fmt.Errorf("not a duration or seconds: %q", raw)
		}
		seconds = parsed
	}
	if seconds != 0 && (seconds < 60 || seconds > 86400) {
		return 0, fmt.Errorf("must be 0 or 60..86400 seconds (got %d)", seconds)
	}
	return seconds, nil
}

func parsePullIntervalFlag(raw string) (int, error) {
	return parseIntervalFlag(raw)
}

func parseAutomaticModeFlag(raw string) (gdrivesync.RunMode, error) {
	mode, err := gdrivesync.ParseRunMode(raw)
	if err != nil {
		return "", err
	}
	if mode == gdrivesync.ModeManual {
		return "", fmt.Errorf("manual mode cannot be used for automatic schedulers")
	}
	return mode, nil
}

// setLocalSchedule mutates LocalConfig scheduler settings, persists, and
// keeps cfg in sync.
func setLocalSchedule(cfg *gdrivesync.Config, pushInterval, pullInterval int, pushMode, pullMode gdrivesync.RunMode, dryRun bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	local, ok, err := gdrivesync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		local = &gdrivesync.LocalConfig{Propagation: gdrivesync.DefaultPropagationPolicy()}
	}
	local.Interval = pushInterval
	local.PullInterval = pullInterval
	local.PushMode = pushMode
	local.PullMode = pullMode
	if !dryRun {
		if err := gdrivesync.SaveLocalConfig(cfg.LocalPaths, local); err != nil {
			return err
		}
	}
	cfg.Interval = pushInterval
	cfg.PullInterval = pullInterval
	cfg.PushMode = pushMode
	cfg.PullMode = pullMode
	return nil
}

// setLocalPaused mutates the local config's Paused field, persists, and
// keeps cfg in sync so callers see the new value without re-running
// ResolveConfig.
func setLocalPaused(cfg *gdrivesync.Config, paused bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	local, ok, err := gdrivesync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		// Should not happen — ResolveConfig migrates first. Defensive fallback.
		local = &gdrivesync.LocalConfig{Propagation: gdrivesync.DefaultPropagationPolicy()}
	}
	local.Paused = paused
	if err := gdrivesync.SaveLocalConfig(cfg.LocalPaths, local); err != nil {
		return err
	}
	cfg.Paused = paused
	return nil
}

// ── shared (manual exclusion list) ───────────────────────────────────────

func newGdriveSyncSharedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shared",
		Short: "Manage shared-folder exclusions (auto-detected + manual)",
		Long: `View and manage which folders gdrive-sync skips because they are shared.

Two layers feed this list:
  - auto    — Drive shortcuts surfaced via .shortcut-targets-by-id/ or
              the Shared drives/ root. Detected by filesystem property,
              never by name.
  - manual  — relative paths the operator added (state.modules.gdrive_sync
              .shared_excludes). Use this for owned-but-shared-out folders
              that have no filesystem signal.

Both layers feed a per-run dynamic excludes file passed to rsync.

  dot gdrive-sync shared             # alias for list
  dot gdrive-sync shared list
  dot gdrive-sync shared add <path>...
  dot gdrive-sync shared remove <path>...
  dot gdrive-sync shared clear`,
		RunE: runGdriveSyncSharedList,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show auto-detected + manual shared entries",
			RunE:  runGdriveSyncSharedList,
		},
		&cobra.Command{
			Use:          "add <path>...",
			Short:        "Add one or more paths to the manual shared-excludes list",
			Args:         cobra.MinimumNArgs(1),
			RunE:         runGdriveSyncSharedAdd,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "remove <path>...",
			Aliases:      []string{"rm"},
			Short:        "Remove one or more paths from the manual shared-excludes list",
			Args:         cobra.MinimumNArgs(1),
			RunE:         runGdriveSyncSharedRemove,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "clear",
			Short:        "Empty the manual shared-excludes list",
			RunE:         runGdriveSyncSharedClear,
			SilenceUsage: true,
		},
	)
	return cmd
}

func runGdriveSyncSharedList(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gdriveBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	entries, err := gdrivesync.ScanShared(stripTrailingSlash(cfg.MirrorPath), cfg.SharedExcludes)
	if err != nil {
		return fmt.Errorf("scanning shared entries: %w", err)
	}
	p := printerFrom(cmd)
	if len(entries) == 0 {
		p.Line("No shared entries detected and no manual excludes configured.")
		p.Line("Add owned-but-shared-out folders with: dot gdrive-sync shared add <path>")
		return nil
	}
	p.Header(fmt.Sprintf("Shared exclusions under %s", stripTrailingSlash(cfg.MirrorPath)))
	for _, e := range entries {
		detail := e.Detail
		if detail == "" {
			detail = "—"
		}
		p.Line("  %-8s  %-40s  %s", e.Reason.String(), e.RelPath, detail)
	}
	p.Blank()
	p.Line("auto entries are detected from filesystem properties; manual entries are operator-curated.")
	return nil
}

func runGdriveSyncSharedAdd(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	mirror := stripTrailingSlash(cfg.MirrorPath)
	added := make([]string, 0, len(args))
	localCfg, err := editableLocalConfig(cfg)
	if err != nil {
		return err
	}
	current := append([]string(nil), localCfg.SharedExcludes...)

	for _, raw := range args {
		rel, err := relativizeForMirror(raw, mirror)
		if err != nil {
			return err
		}
		if !containsString(current, rel) {
			current = append(current, rel)
			added = append(added, rel)
		}
	}

	dedupedSorted := dedupSorted(current)
	localCfg.SharedExcludes = dedupedSorted
	if err := gdrivesync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = dedupedSorted

	p := printerFrom(cmd)
	if len(added) == 0 {
		p.Line("No new entries — all already present.")
	} else {
		for _, rel := range added {
			p.Line("✓ added %q", rel)
		}
	}
	return nil
}

func runGdriveSyncSharedRemove(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	mirror := stripTrailingSlash(cfg.MirrorPath)
	removed := make([]string, 0, len(args))
	localCfg, err := editableLocalConfig(cfg)
	if err != nil {
		return err
	}
	current := append([]string(nil), localCfg.SharedExcludes...)

	for _, raw := range args {
		rel, err := relativizeForMirror(raw, mirror)
		if err != nil {
			return err
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
	if err := gdrivesync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = current

	p := printerFrom(cmd)
	if len(removed) == 0 {
		p.Line("No matching entries — nothing removed.")
	} else {
		for _, rel := range removed {
			p.Line("✓ removed %q", rel)
		}
	}
	return nil
}

func runGdriveSyncSharedClear(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	localCfg, err := editableLocalConfig(cfg)
	if err != nil {
		return err
	}
	n := len(localCfg.SharedExcludes)
	p := printerFrom(cmd)
	if n == 0 {
		p.Line("Manual shared-excludes list is already empty.")
		return nil
	}
	confirmed, err := ui.Confirm(fmt.Sprintf("Clear %d manual shared-excludes entries?", n), yes)
	if err != nil {
		return err
	}
	if !confirmed {
		p.Line("Aborted.")
		return nil
	}
	localCfg.SharedExcludes = nil
	if err := gdrivesync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = nil
	p.Line("✓ Cleared %d manual entries.", n)
	return nil
}

func editableLocalConfig(cfg *gdrivesync.Config) (*gdrivesync.LocalConfig, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("local paths unresolved")
	}
	localCfg, ok, err := gdrivesync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return nil, err
	}
	if !ok {
		localCfg = &gdrivesync.LocalConfig{Propagation: gdrivesync.DefaultPropagationPolicy()}
	}
	return localCfg, nil
}

// relativizeForMirror normalizes a user-supplied path so it lives under
// mirror as a relative path. Absolute paths must be inside mirror.
// Trailing slashes and "./" prefixes are stripped. Empty results,
// "..", and parent escapes are rejected.
func relativizeForMirror(raw, mirror string) (string, error) {
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

// dedupSorted returns a stable, sorted copy of in with duplicates removed.
func dedupSorted(in []string) []string {
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

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// ── small helpers ────────────────────────────────────────────────────────

// scheduleUnitLabel returns a human-friendly identifier for the scheduler
// artifact on the current platform — the launchd plist path on macOS,
// or the systemd timer unit path on Linux.
func scheduleUnitLabel(paths *gdrivesync.Paths) string {
	if runtime.GOOS == "darwin" {
		return paths.LaunchdPlist
	}
	return paths.SystemdTimer
}

func stripTrailingSlash(p string) string {
	if len(p) > 1 && p[len(p)-1] == '/' {
		return p[:len(p)-1]
	}
	return p
}
