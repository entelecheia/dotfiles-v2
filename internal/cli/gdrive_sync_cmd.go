package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
		Short: "Sync workspace ⟷ gdrive-workspace mirror via local rsync",
		Long: `Local-only rsync mirror between ~/workspace/work and the cloud-sync
client's mirror tree (default ~/gdrive-workspace/work). No SSH; the cloud
client itself handles upload/download to/from Drive (or Dropbox, etc.).

Workspace is authoritative: pull only fetches newer files (no --delete on
the workspace side); push uses --delete-after to propagate workspace
deletions to the mirror, guarded by --max-delete=N.

Getting started:
  dot gdrive-sync setup       Install rsync (if missing) + auto-sync scheduler
  dot gdrive-sync migrate     One-time symlink → real-dir conversion + bring-down
  dot gdrive-sync resume      Activate two-way sync after migrate verified
  dot gdrive-sync             Default = sync (pull then push)

Maintenance:
  dot gdrive-sync status      Show last sync, conflicts, paused state, scheduler
  dot gdrive-sync conflicts   List timestamped backup directories
  dot gdrive-sync pause       Stop auto-sync (scheduler + paused gate)
  dot gdrive-sync resume      Restart auto-sync`,
		RunE:         runGdriveSync,
		SilenceUsage: true,
	}
	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rsync progress output")
	cmd.AddCommand(
		newGdriveSyncSyncCmd(),
		newGdriveSyncPullCmd(),
		newGdriveSyncPushCmd(),
		newGdriveSyncStatusCmd(),
		newGdriveSyncMigrateCmd(),
		newGdriveSyncConflictsCmd(),
		newGdriveSyncSetupCmd(),
		newGdriveSyncResumeCmd(),
		newGdriveSyncPauseCmd(),
		newGdriveSyncSharedCmd(),
	)
	return cmd
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
	if !bypassPause && state.Modules.GdriveSync.Paused {
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

// recordSyncResult updates the state's Last* timestamps and the on-disk
// log. Called from sync/pull/push handlers after the operation finishes.
// Errors are surfaced but do not override the underlying sync result.
func recordSyncResult(state *config.UserState, cfg *gdrivesync.Config, op string, syncErr error, dryRun bool) {
	if dryRun {
		return
	}
	exitCode := 0
	if syncErr != nil {
		exitCode = 1
	}
	gdrivesync.AppendLog(cfg.LogFile, op, exitCode)
	gdrivesync.RotateLog(cfg.LogFile, 2000, 1000)

	if syncErr == nil {
		now := time.Now()
		switch op {
		case "pull":
			state.Modules.GdriveSync.LastPull = now
		case "push":
			state.Modules.GdriveSync.LastPush = now
		case "sync":
			state.Modules.GdriveSync.LastPull = now
			state.Modules.GdriveSync.LastPush = now
			state.Modules.GdriveSync.LastSync = now
		}
		_ = config.SaveState(state)
	}
}

// ── sync (root default + explicit subcommand) ────────────────────────────

func newGdriveSyncSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "sync",
		Short:        "Pull then push (default)",
		RunE:         runGdriveSync,
		SilenceUsage: true,
	}
}

func runGdriveSync(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gdrivePreflight(p, cfg, runner, state, false, false) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Syncing %s ⟷ %s", cfg.LocalPath, cfg.MirrorPath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	syncErr := gdrivesync.Sync(cmd.Context(), runner, cfg, dryRun)
	recordSyncResult(state, cfg, "sync", syncErr, dryRun)
	if syncErr != nil {
		return fmt.Errorf("sync failed: %w", syncErr)
	}
	p.Line("✓ Sync complete.")
	return nil
}

// ── pull ─────────────────────────────────────────────────────────────────

func newGdriveSyncPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pull",
		Short:        "Fetch newer files from mirror (--update, no --delete)",
		RunE:         runGdriveSyncPull,
		SilenceUsage: true,
	}
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

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pulling %s → %s", cfg.MirrorPath, cfg.LocalPath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	pullErr := gdrivesync.Pull(cmd.Context(), runner, cfg, dryRun)
	recordSyncResult(state, cfg, "pull", pullErr, dryRun)
	if pullErr != nil {
		return fmt.Errorf("pull failed: %w", pullErr)
	}
	p.Line("✓ Pull complete.")
	return nil
}

// ── push ─────────────────────────────────────────────────────────────────

func newGdriveSyncPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "push",
		Short:        "Send workspace to mirror (--delete-after, capped by --max-delete)",
		RunE:         runGdriveSyncPush,
		SilenceUsage: true,
	}
}

func runGdriveSyncPush(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !gdrivePreflight(p, cfg, runner, state, false, false) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	release, lockErr := gdrivesync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pushing %s → %s", cfg.LocalPath, cfg.MirrorPath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	pushErr := gdrivesync.Push(cmd.Context(), runner, cfg, dryRun)
	recordSyncResult(state, cfg, "push", pushErr, dryRun)
	if pushErr != nil {
		return fmt.Errorf("push failed: %w", pushErr)
	}
	p.Line("✓ Push complete.")
	return nil
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
	state, cfg, runner, err := gdriveBootstrap(cmd)
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
	p.KV("Local exists", boolStr(st.LocalExists))
	p.KV("Mirror exists", boolStr(st.MirrorExists))
	if st.Paused {
		p.KV("Paused", "yes — run `dot gdrive-sync resume` to activate")
	} else {
		p.KV("Paused", "no")
	}
	p.KV("Interval", formatInterval(st.Interval))
	p.KV("Scheduler", st.SchedulerState.String())
	p.KV("Max delete", fmt.Sprintf("%d", st.MaxDelete))
	p.KV("Lock held", boolStr(st.LockHeld))
	p.KV("Last sync", formatLastSync(st.LastSync))
	p.KV("Last pull", formatLastSync(st.LastPull))
	p.KV("Last push", formatLastSync(st.LastPush))

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
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if state.Modules.GdriveSync.Paused {
		state.Modules.GdriveSync.Paused = false
		if err := config.SaveState(state); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
		p.Line("✓ gdrive-sync resumed.")
	} else {
		p.Line("gdrive-sync was not paused.")
	}

	// If the scheduler is installed, reattach it so periodic runs resume.
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
	state, cfg, runner, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)

	if !state.Modules.GdriveSync.Paused {
		state.Modules.GdriveSync.Paused = true
		if err := config.SaveState(state); err != nil {
			return fmt.Errorf("saving state: %w", err)
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
	return &cobra.Command{
		Use:   "setup",
		Short: "Install rsync (if missing) and deploy auto-sync scheduler",
		Long: `One-time setup. Verifies rsync is available (offers to install via brew/apt
if not), then deploys the platform's user-scheduler (launchd LaunchAgent on
macOS, systemd user-timer on Linux) to run ` + "`dot gdrive-sync sync`" + ` every
Interval seconds (default 300).

Idempotent — re-run safely after an interval change to reload the unit.`,
		RunE:         runGdriveSyncSetup,
		SilenceUsage: true,
	}
}

func runGdriveSyncSetup(cmd *cobra.Command, _ []string) error {
	state, cfg, runner, err := gdriveBootstrap(cmd)
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

	// 2. Deploy scheduler
	p.Line("Deploying auto-sync scheduler...")
	sched, paths, err := gdriveScheduler(cfg, runner)
	if err != nil {
		return err
	}
	if err := sched.Install(ctx); err != nil {
		return fmt.Errorf("installing scheduler: %w", err)
	}
	p.Line("  ✓ scheduler installed (interval: %s)", formatInterval(cfg.Interval))
	p.Line("  unit: %s", scheduleUnitLabel(paths))
	p.Line("  log:  %s", cfg.LogFile)

	// 3. Persist Interval (in case ResolveConfig clamped it).
	if state.Modules.GdriveSync.Interval != cfg.Interval {
		state.Modules.GdriveSync.Interval = cfg.Interval
		if err := config.SaveState(state); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
	}

	p.Blank()
	p.Line("✓ gdrive-sync setup complete.")
	if state.Modules.GdriveSync.Paused {
		p.Line("  Paused gate is set — run `dot gdrive-sync resume` to start syncing.")
	} else {
		p.Line("  Run `dot gdrive-sync sync` for an immediate sync, or wait for the timer.")
	}
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
	_, cfg, _, err := gdriveBootstrap(cmd)
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
	state, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	mirror := stripTrailingSlash(cfg.MirrorPath)
	added := make([]string, 0, len(args))
	current := append([]string(nil), state.Modules.GdriveSync.SharedExcludes...)

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
	state.Modules.GdriveSync.SharedExcludes = dedupedSorted
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

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
	state, cfg, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	mirror := stripTrailingSlash(cfg.MirrorPath)
	removed := make([]string, 0, len(args))
	current := append([]string(nil), state.Modules.GdriveSync.SharedExcludes...)

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

	state.Modules.GdriveSync.SharedExcludes = current
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

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
	state, _, _, err := gdriveBootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	n := len(state.Modules.GdriveSync.SharedExcludes)
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
	state.Modules.GdriveSync.SharedExcludes = nil
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	p.Line("✓ Cleared %d manual entries.", n)
	return nil
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
