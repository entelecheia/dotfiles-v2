package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/rsync"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync workspace binaries with remote server via rsync",
		Long: `Binary-only workspace sync with a remote server over SSH using rsync.

Default is pull-then-push: pull newer binaries from remote, then push
local binaries to remote. Text files (.md, .py, etc.) use git only.

Getting started:
  dot sync setup       Guided setup (rsync, SSH, extensions, scheduler)

Sync operations:
  dot sync             Pull then push (default)
  dot sync pull        Pull only: remote → local (--update, safe)
  dot sync push        Push only: local → remote (--delete-after)

Maintenance:
  dot sync status      Show sync health, last run, scheduler state
  dot sync log         View recent sync log entries
  dot sync pause       Temporarily stop auto-sync
  dot sync resume      Restart auto-sync`,
		RunE:         runSync,
		SilenceUsage: true,
	}

	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rsync progress output")

	cmd.AddCommand(
		newSyncPullCmd(),
		newSyncPushCmd(),
		newSyncStatusCmd(),
		newSyncSetupCmd(),
		newSyncLogCmd(),
		newSyncPauseCmd(),
		newSyncResumeCmd(),
	)

	return cmd
}

// syncBootstrap loads state, resolves config and paths for sync commands.
func syncBootstrap(cmd *cobra.Command) (*config.UserState, *rsync.Config, *rsync.Paths, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}

	cfg, err := rsync.ResolveConfig(state)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	paths, err := rsync.ResolvePaths()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)

	return state, cfg, paths, runner, nil
}

// syncPreflight validates rsync + extensions file before any sync operation.
func syncPreflight(p *Printer, cfg *rsync.Config, runner *exec.Runner) bool {
	if !runner.CommandExists("rsync") {
		p.Line("rsync is not installed. Run 'dot sync setup' to get started.")
		return false
	}
	if cfg.RemoteHost == "" {
		p.Line("Remote host not configured. Run 'dot sync setup' to configure.")
		return false
	}
	if !runner.FileExists(cfg.ExtensionsFile) {
		p.Line("Extensions file not found. Run 'dot sync setup' to configure sync.")
		return false
	}
	return true
}

// ── sync (pull-then-push) ────────────────────────────────────────────────

func runSync(cmd *cobra.Command, _ []string) error {
	_, cfg, _, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !syncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Acquire lock
	release, lockErr := rsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Syncing %s ⟷ %s:%s", cfg.LocalPath, cfg.RemoteHost, cfg.RemotePath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	syncErr := rsync.Sync(cmd.Context(), runner, cfg, dryRun)
	if !dryRun {
		exitCode := 0
		if syncErr != nil {
			exitCode = 1
		}
		rsync.AppendLog(cfg.LogFile, exitCode, exitCode)
		rsync.RotateLog(cfg.LogFile, 2000, 1000)
	}
	if syncErr != nil {
		return fmt.Errorf("sync failed: %w", syncErr)
	}
	p.Line("✓ Sync complete.")
	return nil
}

// ── pull ─────────────────────────────────────────────────────────────────

func newSyncPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pull",
		Short:        "Pull newer binaries from remote (--update, safe)",
		RunE:         runSyncPull,
		SilenceUsage: true,
	}
}

func runSyncPull(cmd *cobra.Command, _ []string) error {
	_, cfg, _, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !syncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	release, lockErr := rsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pulling %s:%s → %s", cfg.RemoteHost, cfg.RemotePath, cfg.LocalPath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	if err := rsync.Pull(cmd.Context(), runner, cfg, dryRun); err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}
	p.Line("✓ Pull complete.")
	return nil
}

// ── push ─────────────────────────────────────────────────────────────────

func newSyncPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "push",
		Short:        "Push binaries to remote (--delete-after, local is authority)",
		RunE:         runSyncPush,
		SilenceUsage: true,
	}
}

func runSyncPush(cmd *cobra.Command, _ []string) error {
	_, cfg, _, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	p := printerFrom(cmd)
	if !syncPreflight(p, cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	release, lockErr := rsync.AcquireLock(cfg.LockDir)
	if lockErr != nil {
		p.Line("  %s", lockErr)
		return nil
	}
	defer release()

	p.Line("Pushing %s → %s:%s", cfg.LocalPath, cfg.RemoteHost, cfg.RemotePath)
	if dryRun {
		p.Line("  (dry-run — no changes)")
	}
	if err := rsync.Push(cmd.Context(), runner, cfg, dryRun); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}
	p.Line("✓ Push complete.")
	return nil
}

// ── setup ────────────────────────────────────────────────────────────────

func newSyncSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "setup",
		Short:        "Install rsync, configure SSH, and deploy sync infrastructure",
		RunE:         runSyncSetup,
		SilenceUsage: true,
	}
}

func runSyncSetup(cmd *cobra.Command, _ []string) error {
	state, _, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	ctx := cmd.Context()
	p := printerFrom(cmd)

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

	// 2. Configure remote host
	defaultHost := state.Modules.Rsync.RemoteHost
	remoteHost, err := ui.Input("Remote host (user@host)", defaultHost, yes)
	if err != nil {
		return err
	}
	if remoteHost == "" {
		p.Line("Remote host is required.")
		return nil
	}

	// 3. Verify SSH access
	p.Line("Checking SSH access to %s...", remoteHost)
	if err := rsync.CheckSSH(ctx, runner, remoteHost); err != nil {
		p.Line("  ⚠ %v", err)
		p.Line("  Fix SSH access and try again.")
	} else {
		p.Line("  ✓ SSH to %s OK", remoteHost)
	}

	// 4. Configure paths
	defaultLocal := state.Modules.Workspace.Path
	if defaultLocal == "" {
		home, _ := os.UserHomeDir()
		defaultLocal = filepath.Join(home, "workspace", "work")
	}
	localPath, err := ui.Input("Local workspace path", defaultLocal, yes)
	if err != nil {
		return err
	}

	defaultRemote := state.Modules.Rsync.RemotePath
	if defaultRemote == "" {
		defaultRemote = "~/workspace/work/"
	}
	remotePath, err := ui.Input("Remote workspace path", defaultRemote, yes)
	if err != nil {
		return err
	}

	// Update state
	state.Modules.Rsync.RemoteHost = remoteHost
	state.Modules.Rsync.RemotePath = remotePath
	if state.Modules.Rsync.Interval <= 0 {
		state.Modules.Rsync.Interval = 300
	}
	state.Modules.Workspace.Path = localPath

	// 5. Deploy extensions file
	p.Line("Deploying binary extensions file...")
	engine := template.NewEngine()
	extContent, err := engine.ReadStatic("rsync/binary-extensions.conf")
	if err != nil {
		return fmt.Errorf("reading extensions template: %w", err)
	}
	if err := runner.MkdirAll(filepath.Dir(paths.ExtensionsFile), 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	if err := runner.WriteFile(paths.ExtensionsFile, extContent, 0644); err != nil {
		return fmt.Errorf("writing extensions file: %w", err)
	}
	p.Line("  ✓ %s", paths.ExtensionsFile)

	// 6. Deploy scheduler
	p.Line("Deploying auto-sync scheduler...")
	cfg, err := rsync.ResolveConfig(state)
	if err != nil {
		return err
	}
	sched := rsync.NewScheduler(runner, paths, cfg, engine)
	if err := sched.Install(ctx); err != nil {
		return fmt.Errorf("installing scheduler: %w", err)
	}
	p.Line("  ✓ scheduler installed")

	// 7. Save state
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	p.Line("\n✓ Sync setup complete. Run 'dot sync' to start syncing.")
	return nil
}

// ── status ───────────────────────────────────────────────────────────────

func newSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current sync status",
		RunE:  runSyncStatus,
	}
}

func runSyncStatus(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}

	engine := template.NewEngine()
	sched := rsync.NewScheduler(runner, paths, cfg, engine)
	st, err := rsync.GetStatus(cmd.Context(), sched, cfg)
	if err != nil {
		return err
	}

	p := printerFrom(cmd)
	p.Line("")
	p.Line("%s", ui.StyleHeader.Render(" Workspace Sync Status "))
	p.Line("")

	if st.RsyncVersion != "" {
		printKV("rsync", st.RsyncVersion)
	} else {
		printKV("rsync", "not installed")
	}

	printKV("Local", st.LocalPath)
	if st.RemoteHost != "" {
		printKV("Remote", st.RemoteHost+":"+st.RemotePath)
	} else {
		printKV("Remote", "(not configured)")
	}
	printKV("Interval", formatInterval(st.Interval))
	printKV("Scheduler", st.SchedulerState.String())

	if st.LastSyncTime != nil {
		ago := time.Since(*st.LastSyncTime).Truncate(time.Second)
		printKV("Last sync", fmt.Sprintf("%s ago", ago))
	} else {
		printKV("Last sync", "(never)")
	}

	if st.LastResult != "" {
		printKV("Last result", st.LastResult)
	}

	p.Line("")
	return nil
}

// ── log ──────────────────────────────────────────────────────────────────

func newSyncLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log [lines]",
		Short: "Show recent sync log entries",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runSyncLog,
	}
}

func runSyncLog(cmd *cobra.Command, args []string) error {
	_, cfg, _, _, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}

	n := 50
	if len(args) > 0 {
		if parsed, err := strconv.Atoi(args[0]); err == nil && parsed > 0 {
			n = parsed
		}
	}

	p := printerFrom(cmd)
	lines, err := rsync.TailLog(cfg.LogFile, n)
	if err != nil {
		p.Line("No log file found at %s", cfg.LogFile)
		return nil
	}

	p.Line("%s", lines)
	return nil
}

// ── pause / resume ───────────────────────────────────────────────────────

func newSyncPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause auto-sync scheduler",
		RunE:  runSyncPause,
	}
}

func runSyncPause(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}

	engine := template.NewEngine()
	sched := rsync.NewScheduler(runner, paths, cfg, engine)

	p := printerFrom(cmd)
	if sched.State(cmd.Context()) == rsync.SchedulerNotInstalled {
		p.Line("Scheduler not installed. Run 'dot sync setup' to configure auto-sync.")
		return nil
	}

	if err := sched.Pause(cmd.Context()); err != nil {
		return fmt.Errorf("pausing scheduler: %w", err)
	}
	p.Line("Auto-sync paused.")
	return nil
}

func newSyncResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume auto-sync scheduler",
		RunE:  runSyncResume,
	}
}

func runSyncResume(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}

	engine := template.NewEngine()
	sched := rsync.NewScheduler(runner, paths, cfg, engine)

	p := printerFrom(cmd)
	if sched.State(cmd.Context()) == rsync.SchedulerNotInstalled {
		p.Line("Scheduler not installed. Run 'dot sync setup' to configure auto-sync.")
		return nil
	}

	if err := sched.Resume(cmd.Context()); err != nil {
		return fmt.Errorf("resuming scheduler: %w", err)
	}
	p.Line("Auto-sync resumed.")
	return nil
}
