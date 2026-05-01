package cli

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/gdrivesync"
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
  dot gdrive-sync migrate     One-time symlink → real-dir conversion + bring-down
  dot gdrive-sync resume      Activate two-way sync after migrate verified
  dot gdrive-sync             Default = sync (pull then push)

Maintenance:
  dot gdrive-sync status      Show last sync, conflicts, paused state
  dot gdrive-sync conflicts   List timestamped backup directories
  dot gdrive-sync pause       Stop sync from running until resume`,
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
		newGdriveSyncResumeCmd(),
		newGdriveSyncPauseCmd(),
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
	st, err := gdrivesync.GetStatus(cmd.Context(), runner, cfg, state)
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
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if !state.Modules.GdriveSync.Paused {
		printerFrom(cmd).Line("gdrive-sync already active.")
		return nil
	}
	state.Modules.GdriveSync.Paused = false
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	printerFrom(cmd).Line("✓ gdrive-sync resumed. Try `dot gdrive-sync sync --dry-run` first.")
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
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if state.Modules.GdriveSync.Paused {
		printerFrom(cmd).Line("gdrive-sync already paused.")
		return nil
	}
	state.Modules.GdriveSync.Paused = true
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	printerFrom(cmd).Line("✓ gdrive-sync paused. Run `dot gdrive-sync resume` to clear.")
	return nil
}

// ── small helpers ────────────────────────────────────────────────────────

func stripTrailingSlash(p string) string {
	if len(p) > 1 && p[len(p)-1] == '/' {
		return p[:len(p)-1]
	}
	return p
}
