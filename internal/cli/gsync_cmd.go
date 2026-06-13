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

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/gsync"
	"github.com/entelecheia/dotfiles-v2/internal/rsync"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/entelecheia/dotfiles-v2/internal/ws"
)

func newGsyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "gsync",
		Aliases: []string{"gdrive-sync"},
		Short:   "Push workspace to gdrive-workspace mirror via local rsync",
		Args:    cobra.NoArgs,
		Long: `Local-only rsync mirror between ~/workspace/work and the cloud-sync
client's mirror tree (default ~/gdrive-workspace/work). No SSH; the cloud
client itself handles upload/download to/from Drive (or Dropbox, etc.).

Workspace is authoritative for new local artifacts, while
.dotfiles/gdrive-sync/baseline.manifest is the Git-shared index for
Drive-backed payloads. Push sends local creates and updates to the mirror;
pull restores or updates baseline-tracked payloads from Drive. New
Drive-origin files still stage into inbox/gdrive for manual routing.

	Getting started:
	  dot gsync setup       Check rsync and disable managed schedulers by default
	  dot gsync resume      Clear the paused gate
	  dot gsync push        Push workspace → mirror (use --mode for clean/force)
	  dot gsync pull        Restore baseline-tracked payloads from mirror

	Maintenance:
	  dot gsync status      Show filter mode, last pull/push/intake, conflicts, paused state, scheduler
	  dot gsync conflicts   List or prune timestamped backup directories
	  dot gsync pause       Stop managed schedulers + set paused gate
	  dot gsync resume      Clear paused gate and re-arm installed schedulers

Run without a subcommand to print this help. Legacy alias: 'dot gdrive-sync' continues to work.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}
	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rsync progress output")
	cmd.PersistentFlags().String("mode", gsync.ModeManual.String(), "execution mode for push/pull: manual, clean, or force")
	cmd.PersistentFlags().String("filter-mode", "", "override config filter mode for this run: include or exclude")
	cmd.AddCommand(
		newGsyncSyncCmd(),
		newGsyncPullCmd(),
		newGsyncPushCmd(),
		newGsyncIntakeCmd(),
		newGsyncInboxCmd(),
		newGsyncStatusCmd(),
		newGsyncConflictsCmd(),
		newGsyncSetupCmd(),
		newGsyncResumeCmd(),
		newGsyncPauseCmd(),
		newGsyncSharedCmd(),
		newGsyncInitCmd(),
		newGsyncMirrorCmd(),
	)
	return cmd
}

// ── mirror ───────────────────────────────────────────────────────────────

func newGsyncMirrorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mirror [path]",
		Short: "Show or set the gsync mirror path (the cloud-synced tree)",
		Long: `With no argument, prints the resolved mirror path.

With a path, sets mirror_path in this workspace's local config
(<workspace>/.dotfiles/gdrive-sync/config.yaml) so it takes effect
immediately, and also records it in the global user state so future
workspaces inherit it. Use this to point the mirror at, e.g.,
~/Dropbox/work after switching cloud providers.`,
		Args:         cobra.MaximumNArgs(1),
		RunE:         runGsyncMirror,
		SilenceUsage: true,
	}
}

func runGsyncMirror(cmd *cobra.Command, args []string) error {
	p := printerFrom(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	home := homeFromCmd(cmd) // honors the persistent --home override

	// Print + dry-run are read-only: use the read-only bootstrap so neither
	// creates the per-workspace .dotfiles/gdrive-sync layout or touches
	// .gitignore on first use.
	if len(args) == 0 || dryRun {
		_, cfg, _, err := gsyncBootstrapReadOnly(cmd)
		if err != nil {
			return err
		}
		if len(args) == 0 {
			p.KV("Mirror", stripTrailingSlash(cfg.MirrorPath))
			return nil
		}
		p.Line("[dry-run] would set mirror_path to %s (local config + global state)", appsettings.ExpandHome(args[0], home))
		return nil
	}

	mirror := appsettings.ExpandHome(args[0], home)
	homeOverride, _ := cmd.Flags().GetString("home")

	// Local config governs the current workspace (global state is ignored
	// once it exists), so write it for immediate effect — but only for the
	// current user. Under --home the admin isn't in the target user's
	// workspace, so the local config (always current-workspace) doesn't
	// apply; only the home-aware global state below is meaningful there.
	if homeOverride == "" {
		_, cfg, _, err := gsyncBootstrap(cmd)
		if err != nil {
			return err
		}
		if cfg.LocalPaths == nil {
			return fmt.Errorf("local paths unresolved — bug in ResolveConfig")
		}
		localCfg, _, err := gsync.LoadLocalConfig(cfg.LocalPaths)
		if err != nil {
			return fmt.Errorf("load local config: %w", err)
		}
		localCfg.MirrorPath = mirror
		if err := gsync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
			return fmt.Errorf("save local config: %w", err)
		}
	}

	// Global state, home-aware: load + save for the target user so an admin
	// using --home writes that user's state (not the current user's), and so
	// future workspaces under that home inherit the new mirror.
	state, err := loadStateForCmd(cmd)
	if err != nil {
		return fmt.Errorf("load global state: %w", err)
	}
	state.Modules.Gsync.MirrorPath = mirror
	if err := persistUserState(cmd, state); err != nil {
		p.Warn("could not update global state: %v", err)
	}

	p.Line("%s", ui.StyleSuccess.Render("✓ mirror path set"))
	p.KV("Mirror", mirror)
	return nil
}

// ── init ─────────────────────────────────────────────────────────────────

func newGsyncInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize <workspace>/.dotfiles/gdrive-sync/ from current state",
		Long: `One-time onboarding for the per-workspace store. Creates
<workspace>/.dotfiles/gdrive-sync/ with config.yaml, include.txt, exclude.txt,
ignore.txt, manifests, log dir; appends '/.dotfiles/' to <workspace>/.gitignore
so the store is never committed; and creates <workspace>/inbox/gdrive/ if
missing.

Idempotent — re-running on a populated store leaves operator edits intact and
just heals any missing pieces.`,
		RunE:         runGsyncInit,
		SilenceUsage: true,
	}
}

func runGsyncInit(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	paths := cfg.LocalPaths
	if paths == nil {
		return fmt.Errorf("local paths unresolved — bug in ResolveConfig")
	}

	// gsyncBootstrap already triggered LoadOrMigrateLocalConfig, so the
	// .dotfiles/gdrive-sync/ tree exists by the time we get here. Heal
	// anything missing (operator may have deleted files) and create the
	// inbox/gdrive staging dir.
	if err := gsync.EnsureLocalLayout(paths); err != nil {
		return fmt.Errorf("ensure layout: %w", err)
	}
	inboxGdrive := stripTrailingSlash(cfg.LocalPath) + "/inbox/gdrive"
	if err := os.MkdirAll(inboxGdrive, 0755); err != nil {
		return fmt.Errorf("create inbox/gdrive: %w", err)
	}

	p.Header("gsync workspace initialized")
	p.KV("Store", paths.StoreDir)
	p.KV("Workspace", stripTrailingSlash(cfg.LocalPath))
	p.KV("Mirror", stripTrailingSlash(cfg.MirrorPath))
	p.KV("Propagation", cfg.Propagation.String())
	p.KV("Filter mode", cfg.FilterMode.String())
	p.KV("Inbox staging", inboxGdrive)
	p.Blank()
	p.Line("Edit %s to customize behavior; %s for include patterns; %s for additional ignore patterns.", paths.ConfigFile, paths.IncludeFile, paths.IgnoreFile)
	p.Line("Run 'dot gsync setup' to verify rsync and keep automatic sync disabled unless intervals are passed.")
	return nil
}

// gsyncBootstrap loads state + resolved config + a runner for any
// gsync subcommand. Mirrors syncBootstrap idiom in sync_cmd.go.
func gsyncBootstrap(cmd *cobra.Command) (*config.UserState, *gsync.Config, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := gsync.ResolveConfig(state)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := applyGsyncFilterModeOverride(cmd, cfg); err != nil {
		return nil, nil, nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	return state, cfg, runner, nil
}

func gsyncBootstrapReadOnly(cmd *cobra.Command) (*config.UserState, *gsync.Config, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}
	cfg, err := gsync.ResolveConfigReadOnly(state)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := applyGsyncFilterModeOverride(cmd, cfg); err != nil {
		return nil, nil, nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)
	return state, cfg, runner, nil
}

func applyGsyncFilterModeOverride(cmd *cobra.Command, cfg *gsync.Config) error {
	if !cmd.Flags().Changed("filter-mode") {
		return nil
	}
	raw, _ := cmd.Flags().GetString("filter-mode")
	mode, err := gsync.ParseFilterMode(raw)
	if err != nil {
		return fmt.Errorf("--filter-mode: %w", err)
	}
	cfg.FilterMode = mode
	return nil
}

// gsyncScheduler builds a Scheduler bound to the same runner+cfg used
// elsewhere in the gsync subcommands. Returns the Paths used so
// callers can introspect plist/timer locations.
func gsyncScheduler(cfg *gsync.Config, runner *exec.Runner) (*gsync.Scheduler, *gsync.Paths, error) {
	paths, err := gsync.ResolvePaths()
	if err != nil {
		return nil, nil, err
	}
	return gsync.NewScheduler(runner, paths, cfg, template.NewEngine()), paths, nil
}

// gsyncPreflight validates that sync can proceed.
func gsyncPreflight(p *Printer, cfg *gsync.Config, runner *exec.Runner) bool {
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
	if cfg.Paused {
		p.Line("gsync is paused. Run `dot gsync resume` to activate.")
		return false
	}
	return true
}

// recordSyncResult updates the on-disk log after a sync operation. Runtime
// timestamps now live in the workspace-local gsync state file.
func recordSyncResult(state *config.UserState, cfg *gsync.Config, op string, syncErr error, dryRun bool) {
	_ = state
	if dryRun {
		return
	}
	exitCode := 0
	if syncErr != nil {
		exitCode = 1
	}
	gsync.AppendLog(cfg.LogFile, op, exitCode)
	gsync.RotateLog(cfg.LogFile, 2000, 1000)

}

// ── sync (root default + explicit subcommand) ────────────────────────────

func newGsyncSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "sync",
		Short:        "Alias for `push` (kept for back-compat; prefer `dot gsync push`)",
		RunE:         runGsync,
		SilenceUsage: true,
	}
}

// runGsync handles the explicit `sync` subcommand. The historical
// Pull+Push semantics were retired; this is now a thin alias for push that
// prints a one-line deprecation hint so callers gradually migrate to
// `push`. The bare `dot gsync` (no subcommand) prints help instead.
func runGsync(cmd *cobra.Command, args []string) error {
	printerFrom(cmd).Line("(note: `sync` is now an alias for `push`; use `dot gsync pull` for baseline-tracked Drive payloads)")
	return runGsyncPush(cmd, args)
}

// ── pull ─────────────────────────────────────────────────────────────────

func newGsyncPullCmd() *cobra.Command {
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
		RunE:         runGsyncPull,
		SilenceUsage: true,
	}
	cmd.Flags().Bool("strict", false, "force sha256 fingerprints for every baseline entry (slower; catches content changes that preserve size+mtime)")
	return cmd
}

func runGsyncPull(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gsyncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	strict, _ := cmd.Flags().GetBool("strict")
	mode, err := gdriveSyncModeFrom(cmd)
	if err != nil {
		return err
	}

	release, lockErr := gsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pull plan for baseline-tracked payloads %s → %s (%s)", cfg.MirrorPath, cfg.LocalPath, mode)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	plan, err := gsync.PullTracked(cfg, gsync.PullOptions{DryRun: true, Strict: strict})
	if err != nil {
		return fmt.Errorf("planning pull: %w", err)
	}
	printPullPlan(p, cfg, plan)
	if dryRun || !plan.HasChanges() {
		return nil
	}
	if mode == gsync.ModeClean && len(plan.Conflicts) > 0 {
		return fmt.Errorf("pull refused: %d conflict(s); rerun with --mode=force to overwrite with backups", len(plan.Conflicts))
	}
	force := mode == gsync.ModeForce
	if mode == gsync.ModeManual {
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
	res, err := gsync.PullTracked(cfg, gsync.PullOptions{Force: force, Strict: strict})
	recordSyncResult(state, cfg, "pull", err, false)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}
	printPullResult(p, cfg, res)
	return nil
}

// ── intake ───────────────────────────────────────────────────────────────

func newGsyncIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intake",
		Short: "Stage new GDrive-origin files for manual routing",
		Long: `Compares the mirror against baseline.manifest and imports.manifest to
find new Drive-origin files. New candidates are copied into a timestamped
subdirectory of <local>/inbox/gdrive/<intake-ts>/ for the operator to review
and route.

Changed baseline-tracked files are skipped and left for ` + "`dot gsync pull`" + `.
Mirror-side deletions against baseline are detected by pull, not intake.

  --strict   Use sha256 fingerprints (catches content changes that
             preserve mtime). Default is fast size+mtime mode.`,
		RunE:         runGsyncIntake,
		SilenceUsage: true,
	}
	cmd.Flags().Bool("strict", false, "use sha256 fingerprints instead of size+mtime")
	return cmd
}

func runGsyncIntake(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gsyncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	strict, _ := cmd.Flags().GetBool("strict")
	if _, err := gdriveSyncModeFrom(cmd); err != nil {
		return err
	}

	release, lockErr := gsync.AcquireLock(cfg.LockDir)
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
	res, err := gsync.Intake(cmd.Context(), runner, cfg, gsync.IntakeOptions{
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

func printPullResult(p *Printer, cfg *gsync.Config, res *gsync.PullResult) {
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

func printPullPlan(p *Printer, cfg *gsync.Config, res *gsync.PullResult) {
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

func newGsyncInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Inspect and manage the GDrive intake staging area",
		Long: `View what's staged + tracked under .dotfiles/gdrive-sync/, force a
re-intake of one path, or clear the imports + tombstones manifests
entirely.

  dot gsync inbox                  # alias for list
  dot gsync inbox list
  dot gsync inbox forget <relpath> # next intake re-stages this path
  dot gsync inbox clear            # empty imports + tombstones`,
		RunE: runGsyncInboxList,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show staged run-dirs, imports manifest entries, and tombstones",
			RunE:  runGsyncInboxList,
		},
		&cobra.Command{
			Use:          "forget <relpath>",
			Short:        "Drop a path from imports.manifest so the next intake re-stages it",
			Args:         cobra.ExactArgs(1),
			RunE:         runGsyncInboxForget,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "clear",
			Short:        "Empty imports.manifest and tombstones.log",
			RunE:         runGsyncInboxClear,
			SilenceUsage: true,
		},
	)
	return cmd
}

func runGsyncInboxList(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrapReadOnly(cmd)
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

	imports, err := gsync.LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	if err != nil {
		return fmt.Errorf("loading imports: %w", err)
	}
	tomb, err := gsync.LoadTombstones(cfg.LocalPaths.TombstonesFile)
	if err != nil {
		return fmt.Errorf("loading tombstones: %w", err)
	}

	p.Header("gsync inbox")
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

func runGsyncInboxForget(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
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
	dropped, err := gsync.ForgetImport(cfg.LocalPaths, rel)
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

func runGsyncInboxClear(cmd *cobra.Command, _ []string) error {
	state, cfg, _, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	yes, _ := cmd.Flags().GetBool("yes")
	imports, _ := gsync.LoadImportsManifest(cfg.LocalPaths.ImportsFile)
	tomb, _ := gsync.LoadTombstones(cfg.LocalPaths.TombstonesFile)
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
	if err := gsync.ClearImportsAndTombstones(cfg.LocalPaths); err != nil {
		return err
	}
	p.Line("✓ cleared %d imports + %d tombstones.", len(imports), len(tomb))
	_ = state
	return nil
}

// ── push ─────────────────────────────────────────────────────────────────

func newGsyncPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Preview and send workspace changes to mirror under a propagation policy",
		Long: `Push the workspace tree to the gdrive mirror under a propagation
policy. The default policy '{create:true, update:true, delete:false}'
copies new and modified files but never deletes mirror-side content. By default
push prints the upload plan and asks before applying.

Flag --propagate= takes a comma-separated allowlist; absent items are
disabled. Examples:

  dot gsync push                              # preview, then confirm
  dot gsync push --mode=clean                 # apply only if no conflicts
  dot gsync push --mode=force                 # overwrite with backups
  dot gsync push --propagate=create,update,delete   # full sync
  dot gsync push --propagate=create           # additive only
  dot gsync push --propagate=update           # in-place updates only

The per-workspace store (.dotfiles/) and intake staging area
(inbox/gdrive/) are always excluded so they never round-trip to mirror.`,
		RunE:         runGsyncPush,
		SilenceUsage: true,
	}
	cmd.Flags().String("propagate", "", "comma-separated allowlist of propagation kinds (create,update,delete)")
	return cmd
}

func runGsyncPush(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gsyncBootstrap(cmd)
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

	if !gsyncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	mode, err := gdriveSyncModeFrom(cmd)
	if err != nil {
		return err
	}

	release, lockErr := gsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Push plan for %s → %s (%s, mode=%s)", cfg.LocalPath, cfg.MirrorPath, cfg.Propagation, mode)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	plan, err := gsync.PlanPush(cfg)
	if err != nil {
		return fmt.Errorf("planning push: %w", err)
	}
	printPushPlan(p, plan)
	if dryRun || (!plan.HasChanges() && !plan.HasConflicts()) {
		recordSyncResult(state, cfg, "push", nil, dryRun)
		if !dryRun && cfg.LocalPaths != nil {
			if err := gsync.UpdateLocalState(cfg.LocalPaths, func(s *gsync.LocalState) {
				s.LastPush = time.Now().UTC()
			}); err != nil {
				return fmt.Errorf("state update: %w", err)
			}
		}
		return nil
	}
	if mode == gsync.ModeClean && plan.HasConflicts() {
		return fmt.Errorf("push refused: %d conflict(s); rerun with --mode=force to overwrite with backups", len(plan.Conflicts))
	}
	if mode == gsync.ModeManual {
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
	pushErr := gsync.Push(cmd.Context(), runner, cfg, false)
	recordSyncResult(state, cfg, "push", pushErr, false)
	if pushErr != nil {
		return fmt.Errorf("push failed: %w", pushErr)
	}
	p.Line("✓ Push complete.")
	return nil
}

func printPushPlan(p *Printer, plan *gsync.PushPlan) {
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
func parsePropagateFlag(value string) (gsync.PropagationPolicy, error) {
	var p gsync.PropagationPolicy
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

func gdriveSyncModeFrom(cmd *cobra.Command) (gsync.RunMode, error) {
	raw, _ := cmd.Flags().GetString("mode")
	mode, err := gsync.ParseRunMode(raw)
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

func pullConflictPaths(conflicts []gsync.PullConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.RelPath)
	}
	sort.Strings(out)
	return out
}

func pushConflictPaths(conflicts []gsync.PushConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.RelPath)
	}
	sort.Strings(out)
	return out
}

func tombstonePaths(tombstones []gsync.Tombstone) []string {
	out := make([]string, 0, len(tombstones))
	for _, t := range tombstones {
		out = append(out, t.RelPath)
	}
	sort.Strings(out)
	return out
}

// ── status ───────────────────────────────────────────────────────────────

func newGsyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show local↔mirror sync status",
		RunE:  runGsyncStatus,
	}
}

func runGsyncStatus(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gsyncBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	sched, _, err := gsyncScheduler(cfg, runner)
	if err != nil {
		return err
	}
	st, err := gsync.GetStatus(cmd.Context(), runner, cfg, state, sched)
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
		p.KV("Paused", "yes — run `dot gsync resume` to activate")
	} else {
		p.KV("Paused", "no")
	}
	p.KV("Propagation", st.Propagation.String())
	p.KV("Filter mode", st.FilterMode.String())
	if st.IncludeFile != "" {
		p.KV("Include file", st.IncludeFile)
	}
	if st.ExcludeFile != "" {
		p.KV("Exclude file", st.ExcludeFile)
	}
	if st.IgnoreFile != "" {
		p.KV("Ignore file", st.IgnoreFile)
	}
	if st.Interval > 0 {
		p.KV("Push interval", formatInterval(st.Interval))
		p.KV("Push mode", st.PushMode.String())
		p.KV("Push scheduler", st.SchedulerState.String())
	} else {
		p.KV("Push scheduler", "(off — `dot gsync setup --push-interval=DUR` to enable)")
	}
	if st.PullInterval > 0 {
		p.KV("Pull interval", formatInterval(st.PullInterval))
		p.KV("Pull mode", st.PullMode.String())
		p.KV("Pull scheduler", st.IntakeSchedulerState.String())
	} else {
		p.KV("Pull scheduler", "(off — `dot gsync setup --pull-interval=DUR` to enable)")
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
			if e.Reason == gsync.SharedManual {
				manual++
			} else {
				auto++
			}
		}
		p.KV("Shared", fmt.Sprintf("%d entries (%d auto, %d manual) — see `dot gsync shared`", n, auto, manual))
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

// ── conflicts ────────────────────────────────────────────────────────────

// conflictTrees returns the (label, root) pairs that accumulate
// .sync-conflicts/ backups: pull backups land in the workspace tree,
// push backups land in the mirror tree.
func conflictTrees(cfg *gsync.Config) [][2]string {
	return [][2]string{
		{"workspace", stripTrailingSlash(cfg.LocalPath)},
		{"mirror", stripTrailingSlash(cfg.MirrorPath)},
	}
}

func newGsyncConflictsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List or prune .sync-conflicts/ backup directories",
		Long: `Conflict backups accumulate in both trees: pull backups under the
workspace, push backups under the mirror.

  dot gsync conflicts                       # alias for list
  dot gsync conflicts list
  dot gsync conflicts prune                 # remove backups older than 30 days
  dot gsync conflicts prune --older-than 7
  dot gsync conflicts prune --all           # remove every backup`,
		RunE: runGsyncConflictsList,
	}
	prune := &cobra.Command{
		Use:          "prune",
		Short:        "Remove old conflict backups from both trees",
		RunE:         runGsyncConflictsPrune,
		SilenceUsage: true,
	}
	prune.Flags().Int("older-than", 30, "prune backups older than this many days")
	prune.Flags().Bool("all", false, "prune every backup regardless of age")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List .sync-conflicts/ backup directories in both trees",
			RunE:  runGsyncConflictsList,
		},
		prune,
	)
	return cmd
}

func runGsyncConflictsList(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	now := time.Now()
	for _, tree := range conflictTrees(cfg) {
		label, root := tree[0], tree[1]
		confs, err := gsync.ListConflicts(root)
		if err != nil {
			return err
		}
		if len(confs) == 0 {
			p.Line("No conflict backups under %s/.sync-conflicts/ (%s)", root, label)
			continue
		}
		p.Header(fmt.Sprintf("Conflict backups under %s/.sync-conflicts/ (%s)", root, label))
		for _, c := range confs {
			age := now.Sub(c.ModTime).Truncate(time.Hour)
			marker := "•"
			if age > 30*24*time.Hour {
				marker = "▲" // older than 30 days — candidate for cleanup
			}
			p.Bullet(marker, fmt.Sprintf("%s (%s ago) — %s", c.Timestamp, age, c.Path))
		}
		p.Blank()
	}
	p.Line("Prune candidates (▲) with: dot gsync conflicts prune")
	return nil
}

// resolvePruneCutoff turns the prune flags into a cutoff time. olderChanged
// reports whether --older-than was set explicitly, so it can be rejected in
// combination with --all.
func resolvePruneCutoff(olderDays int, all, olderChanged bool) (time.Time, error) {
	if all && olderChanged {
		return time.Time{}, fmt.Errorf("--all and --older-than are mutually exclusive")
	}
	if olderDays < 0 {
		return time.Time{}, fmt.Errorf("--older-than must be >= 0 (got %d)", olderDays)
	}
	if all {
		return time.Now(), nil
	}
	return time.Now().Add(-time.Duration(olderDays) * 24 * time.Hour), nil
}

func runGsyncConflictsPrune(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	olderDays, _ := cmd.Flags().GetInt("older-than")
	all, _ := cmd.Flags().GetBool("all")

	cutoff, err := resolvePruneCutoff(olderDays, all, cmd.Flags().Changed("older-than"))
	if err != nil {
		return err
	}

	// Hold the sync lock so RemoveAll never interleaves with an rsync
	// pass that is actively writing new backups.
	release, lockErr := gsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	trees := conflictTrees(cfg)
	plans := make([]*gsync.PruneResult, len(trees))
	var candidates int
	var reclaim int64
	for i, tree := range trees {
		plan, err := gsync.PruneConflicts(tree[1], cutoff, true)
		if err != nil {
			return err
		}
		plans[i] = plan
		candidates += len(plan.Pruned)
		reclaim += plan.Reclaimed
	}

	now := time.Now()
	for i, tree := range trees {
		label := tree[0]
		plan := plans[i]
		if len(plan.Pruned) == 0 {
			continue
		}
		p.Section(fmt.Sprintf("%s — %s", label, plan.Root))
		for _, c := range plan.Pruned {
			age := now.Sub(c.ModTime).Truncate(time.Hour)
			p.Bullet("▲", fmt.Sprintf("%s (%s ago, %s)", c.Timestamp, age, ws.FormatSize(c.Size)))
		}
	}
	if candidates == 0 {
		p.Line("Nothing to prune.")
		return nil
	}
	p.Line("Would reclaim %s across %d backup dir(s).", ws.FormatSize(reclaim), candidates)
	if dryRun {
		p.Line("  (dry-run — no changes)")
		return nil
	}

	confirmed, err := ui.Confirm(fmt.Sprintf("Remove %d backup dir(s), reclaiming %s?", candidates, ws.FormatSize(reclaim)), yes)
	if err != nil {
		return err
	}
	if !confirmed {
		p.Line("Aborted.")
		return nil
	}

	for _, tree := range trees {
		label, root := tree[0], tree[1]
		res, err := gsync.PruneConflicts(root, cutoff, false)
		if err != nil {
			return err
		}
		if len(res.Pruned) == 0 {
			continue
		}
		p.Success("pruned %d backup dir(s) (freed %s) under %s/.sync-conflicts/", len(res.Pruned), ws.FormatSize(res.Reclaimed), root)
		if label == "mirror" {
			p.Line("  The Drive sync client will propagate these deletions and reclaim cloud quota.")
		}
	}
	return nil
}

// ── pause / resume ───────────────────────────────────────────────────────

func newGsyncResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "resume",
		Short:        "Clear the Paused gate so pull/push/sync can run",
		RunE:         runGsyncResume,
		SilenceUsage: true,
	}
}

func runGsyncResume(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if cfg.Paused {
		if err := setLocalPaused(cfg, false); err != nil {
			return fmt.Errorf("saving local config: %w", err)
		}
		p.Line("✓ gsync resumed.")
	} else {
		p.Line("gsync was not paused.")
	}

	if cfg.Interval == 0 && cfg.PullInterval == 0 {
		p.Line("scheduler remains off — run `dot gsync setup --push-interval=DUR` or `--pull-interval=DUR` to enable.")
		return nil
	}
	// If the scheduler is configured and installed, reattach it so periodic runs resume.
	sched, _, err := gsyncScheduler(cfg, runner)
	if err != nil {
		return nil // state save succeeded; scheduler is best-effort
	}
	if sched.State(cmd.Context()) != gsync.SchedulerNotInstalled {
		if err := sched.Resume(cmd.Context()); err != nil {
			p.Warn("scheduler resume failed: %v", err)
		} else {
			p.Line("✓ scheduler resumed.")
		}
	}
	return nil
}

func newGsyncPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pause",
		Short:        "Set the Paused gate so pull/push/sync refuse to run",
		RunE:         runGsyncPause,
		SilenceUsage: true,
	}
}

func runGsyncPause(cmd *cobra.Command, _ []string) error {
	_, cfg, runner, err := gsyncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if !cfg.Paused {
		if err := setLocalPaused(cfg, true); err != nil {
			return fmt.Errorf("saving local config: %w", err)
		}
		p.Line("✓ gsync paused.")
	} else {
		p.Line("gsync was already paused.")
	}

	// Stop the scheduler if installed so we don't waste invocations
	// hitting the paused gate every Interval seconds.
	sched, _, err := gsyncScheduler(cfg, runner)
	if err != nil {
		return nil
	}
	if sched.State(cmd.Context()) == gsync.SchedulerRunning {
		if err := sched.Pause(cmd.Context()); err != nil {
			p.Warn("scheduler pause failed: %v", err)
		} else {
			p.Line("✓ scheduler stopped.")
		}
	}
	return nil
}

// ── setup ────────────────────────────────────────────────────────────────

func newGsyncSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install rsync (if missing) and manage opt-in gsync schedulers",
		Long: `One-time setup. Verifies rsync is available (offers to install via brew/apt
if not), then configures the platform's user-scheduler (launchd LaunchAgent on
macOS, systemd user-timer on Linux). Automatic sync is off by default; pass an
interval flag to opt in.

  --push-interval=DUR    Deploy automatic ` + "`dot gsync push --mode=MODE`" + `.
  --pull-interval=DUR    Deploy automatic ` + "`dot gsync pull --mode=MODE`" + `.
  --push-mode=MODE       Automatic push mode: clean or force (default clean).
  --pull-mode=MODE       Automatic intake mode: clean or force (default clean).

Idempotent — re-run safely after an interval change to reload the unit.`,
		RunE:         runGsyncSetup,
		SilenceUsage: true,
	}
	cmd.Flags().String("push-interval", "", "deploy push scheduler at this cadence (e.g. 15m, 1h, 0 to remove)")
	cmd.Flags().String("pull-interval", "", "deploy pull scheduler at this cadence (e.g. 15m, 1h, 0 to remove)")
	cmd.Flags().String("push-mode", gsync.ModeClean.String(), "automatic push mode: clean or force")
	cmd.Flags().String("pull-mode", gsync.ModeClean.String(), "automatic intake mode: clean or force")
	return cmd
}

func runGsyncSetup(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	var cfg *gsync.Config
	var runner *exec.Runner
	var err error
	if dryRun {
		_, cfg, runner, err = gsyncBootstrapReadOnly(cmd)
	} else {
		_, cfg, runner, err = gsyncBootstrap(cmd)
	}
	if err != nil {
		return err
	}
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
		if dryRun {
			p.Line("  ~ rsync not found; would install after confirmation")
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
	}

	// 2. Deploy scheduler(s) only when explicitly enabled.
	p.Line("Configuring opt-in scheduler...")
	sched, paths, err := gsyncScheduler(cfg, runner)
	if err != nil {
		return err
	}
	if dryRun {
		if cfg.Interval > 0 {
			p.Line("  ~ would install push unit (interval: %s, mode: %s)", formatInterval(cfg.Interval), cfg.PushMode)
			p.Line("  unit: %s", scheduleUnitLabel(paths))
		} else {
			p.Line("  ~ would ensure push scheduler is off")
		}
		if cfg.PullInterval > 0 {
			p.Line("  ~ would install pull unit (interval: %s, mode: %s)", formatInterval(cfg.PullInterval), cfg.PullMode)
		} else {
			p.Line("  ~ would ensure pull scheduler is off")
		}
		p.Line("  log:  %s", cfg.LogFile)
		p.Blank()
		p.Line("✓ gsync setup dry-run complete.")
		return nil
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
	p.Line("✓ gsync setup complete.")
	if cfg.Paused {
		p.Line("  Paused gate is set — run `dot gsync resume` to start syncing.")
	} else {
		p.Line("  Run `dot gsync push` or `dot gsync pull` when you want to sync manually.")
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
	if err := gsync.ValidateScheduleInterval(seconds); err != nil {
		return 0, err
	}
	return seconds, nil
}

func parseAutomaticModeFlag(raw string) (gsync.RunMode, error) {
	mode, err := gsync.ParseRunMode(raw)
	if err != nil {
		return "", err
	}
	return gsync.NormalizeAutomaticMode(mode)
}

// setLocalSchedule mutates LocalConfig scheduler settings, persists, and
// keeps cfg in sync.
func setLocalSchedule(cfg *gsync.Config, pushInterval, pullInterval int, pushMode, pullMode gsync.RunMode, dryRun bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	schedule, err := (gsync.ScheduleSettings{
		Interval:     pushInterval,
		PullInterval: pullInterval,
		PushMode:     pushMode,
		PullMode:     pullMode,
	}).Normalize()
	if err != nil {
		return err
	}
	local, ok, err := gsync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		local = &gsync.LocalConfig{Propagation: gsync.DefaultPropagationPolicy()}
	}
	schedule.ApplyToLocalConfig(local)
	if !dryRun {
		if err := gsync.SaveLocalConfig(cfg.LocalPaths, local); err != nil {
			return err
		}
	}
	cfg.Interval = schedule.Interval
	cfg.PullInterval = schedule.PullInterval
	cfg.PushMode = schedule.PushMode
	cfg.PullMode = schedule.PullMode
	return nil
}

// setLocalPaused mutates the local config's Paused field, persists, and
// keeps cfg in sync so callers see the new value without re-running
// ResolveConfig.
func setLocalPaused(cfg *gsync.Config, paused bool) error {
	if cfg.LocalPaths == nil {
		return fmt.Errorf("local paths unresolved")
	}
	local, ok, err := gsync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok {
		// Should not happen — ResolveConfig migrates first. Defensive fallback.
		local = &gsync.LocalConfig{Propagation: gsync.DefaultPropagationPolicy()}
	}
	local.Paused = paused
	if err := gsync.SaveLocalConfig(cfg.LocalPaths, local); err != nil {
		return err
	}
	cfg.Paused = paused
	return nil
}

// ── shared (manual exclusion list) ───────────────────────────────────────

func newGsyncSharedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shared",
		Short: "Manage shared-folder exclusions (auto-detected + manual)",
		Long: `View and manage which folders gsync skips because they are shared.

Two layers feed this list:
  - auto    — Drive shortcuts surfaced via .shortcut-targets-by-id/ or
              the Shared drives/ root. Detected by filesystem property,
              never by name.
  - manual  — relative paths the operator added (state.modules.gdrive_sync
              .shared_excludes). Use this for owned-but-shared-out folders
              that have no filesystem signal.

Both layers feed a per-run dynamic excludes file passed to rsync.

  dot gsync shared             # alias for list
  dot gsync shared list
  dot gsync shared add <path>...
  dot gsync shared remove <path>...
  dot gsync shared clear`,
		RunE: runGsyncSharedList,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "Show auto-detected + manual shared entries",
			RunE:  runGsyncSharedList,
		},
		&cobra.Command{
			Use:          "add <path>...",
			Short:        "Add one or more paths to the manual shared-excludes list",
			Args:         cobra.MinimumNArgs(1),
			RunE:         runGsyncSharedAdd,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "remove <path>...",
			Aliases:      []string{"rm"},
			Short:        "Remove one or more paths from the manual shared-excludes list",
			Args:         cobra.MinimumNArgs(1),
			RunE:         runGsyncSharedRemove,
			SilenceUsage: true,
		},
		&cobra.Command{
			Use:          "clear",
			Short:        "Empty the manual shared-excludes list",
			RunE:         runGsyncSharedClear,
			SilenceUsage: true,
		},
	)
	return cmd
}

func runGsyncSharedList(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	entries, err := gsync.ScanShared(stripTrailingSlash(cfg.MirrorPath), cfg.SharedExcludes)
	if err != nil {
		return fmt.Errorf("scanning shared entries: %w", err)
	}
	p := printerFrom(cmd)
	if len(entries) == 0 {
		p.Line("No shared entries detected and no manual excludes configured.")
		p.Line("Add owned-but-shared-out folders with: dot gsync shared add <path>")
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

func runGsyncSharedAdd(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
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
	if err := gsync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
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

func runGsyncSharedRemove(cmd *cobra.Command, args []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
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
	if err := gsync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
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

func runGsyncSharedClear(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := gsyncBootstrap(cmd)
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
	if err := gsync.SaveLocalConfig(cfg.LocalPaths, localCfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}
	cfg.SharedExcludes = nil
	p.Line("✓ Cleared %d manual entries.", n)
	return nil
}

func editableLocalConfig(cfg *gsync.Config) (*gsync.LocalConfig, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("local paths unresolved")
	}
	localCfg, ok, err := gsync.LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return nil, err
	}
	if !ok {
		localCfg = &gsync.LocalConfig{Propagation: gsync.DefaultPropagationPolicy()}
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
func scheduleUnitLabel(paths *gsync.Paths) string {
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
