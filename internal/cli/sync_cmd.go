package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	gosync "github.com/entelecheia/dotfiles-v2/internal/sync"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync workspace with Google Drive via rclone",
		Long: `Safe workspace sync with Google Drive using rclone copy --update.

Default is PULL ONLY (consumer mode) — safe for Ubuntu/shared machines.
Use 'dot sync push' or 'dot sync all' for upload.

Getting started:
  dot sync setup       Full guided setup (install rclone, auth, filter, scheduler)
  dot sync connect     Configure a new Google Drive remote
  dot sync reconnect   Fix expired authentication

Sync operations:
  dot sync             Pull only: remote → local (default, safe)
  dot sync pull        Pull only: remote → local (explicit)
  dot sync push        Push only: local → remote
  dot sync all         Bidirectional: pull then push
  dot sync mount       Mount remote as FUSE filesystem

Maintenance:
  dot sync status      Show sync health, last run, scheduler state
  dot sync log         View recent sync log entries
  dot sync skip        Manage skipped-file list
  dot sync pause       Temporarily stop auto-sync
  dot sync resume      Restart auto-sync`,
		RunE:         runSyncPull,
		SilenceUsage: true,
	}

	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rclone progress output")

	cmd.AddCommand(
		newSyncPullCmd(),
		newSyncPushCmd(),
		newSyncAllCmd(),
		newSyncMountCmd(),
		newSyncStatusCmd(),
		newSyncSetupCmd(),
		newSyncSkipCmd(),
		newSyncConnectCmd(),
		newSyncReconnectCmd(),
		newSyncLogCmd(),
		newSyncPauseCmd(),
		newSyncResumeCmd(),
	)

	return cmd
}

// syncBootstrap loads state, resolves config and paths for sync commands.
func syncBootstrap(cmd *cobra.Command) (*config.UserState, *gosync.Config, *gosync.Paths, *exec.Runner, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	state, err := config.LoadState()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("loading state: %w", err)
	}

	cfg, err := gosync.ResolveConfig(state)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	verbose, _ := cmd.Flags().GetBool("verbose")
	cfg.Verbose = verbose

	paths, err := gosync.ResolvePaths()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)

	return state, cfg, paths, runner, nil
}

// ── pull / push / all / mount ─────────────────────────────────────────────

// syncPreflight validates rclone + filter file before any sync operation.
func syncPreflight(cfg *gosync.Config, runner *exec.Runner) bool {
	if !runner.CommandExists("rclone") {
		fmt.Println("rclone is not installed. Run 'dot sync setup' to get started.")
		return false
	}
	if !runner.FileExists(cfg.FilterFile) {
		fmt.Println("Filter file not found. Run 'dot sync setup' to configure sync.")
		return false
	}
	return true
}

func newSyncPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pull",
		Short:        "Pull from remote: download newer files (safe, read-only on remote)",
		RunE:         runSyncPull,
		SilenceUsage: true,
	}
}

func runSyncPull(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	if !syncPreflight(cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	fmt.Printf("Pulling %s → %s\n", cfg.RemotePath, cfg.LocalPath)
	if dryRun {
		fmt.Println("  (dry-run — no changes)")
	}
	if err := gosync.Pull(cmd.Context(), runner, cfg, paths, dryRun); err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}
	fmt.Println("✓ Pull complete.")
	return nil
}

func newSyncPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "push",
		Short:        "Push to remote: upload newer files (writes to remote)",
		RunE:         runSyncPush,
		SilenceUsage: true,
	}
}

func runSyncPush(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	if !syncPreflight(cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	fmt.Printf("Pushing %s → %s\n", cfg.LocalPath, cfg.RemotePath)
	if dryRun {
		fmt.Println("  (dry-run — no changes)")
	}
	if err := gosync.Push(cmd.Context(), runner, cfg, paths, dryRun); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}
	fmt.Println("✓ Push complete.")
	return nil
}

func newSyncAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "all",
		Short:        "Bidirectional: pull then push",
		RunE:         runSyncAll,
		SilenceUsage: true,
	}
}

func runSyncAll(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	if !syncPreflight(cfg, runner) {
		return nil
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	fmt.Printf("Syncing %s ⟷ %s\n", cfg.LocalPath, cfg.RemotePath)
	if dryRun {
		fmt.Println("  (dry-run — no changes)")
	}
	if err := gosync.Sync(cmd.Context(), runner, cfg, paths, dryRun); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}
	fmt.Println("✓ Sync complete.")
	return nil
}

func newSyncMountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mount",
		Short:        "Mount remote as FUSE filesystem (live, no local storage)",
		RunE:         runSyncMount,
		SilenceUsage: true,
	}
	cmd.Flags().Bool("daemon", false, "Run in background (daemon mode)")
	cmd.Flags().Bool("unmount", false, "Unmount instead of mounting")
	return cmd
}

func runSyncMount(cmd *cobra.Command, _ []string) error {
	_, cfg, paths, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	if !runner.CommandExists("rclone") {
		fmt.Println("rclone is not installed. Run 'dot sync setup' to get started.")
		return nil
	}

	unmount, _ := cmd.Flags().GetBool("unmount")
	if unmount {
		if err := gosync.Unmount(cmd.Context(), runner, paths); err != nil {
			return fmt.Errorf("unmount failed: %w", err)
		}
		fmt.Printf("✓ Unmounted %s\n", paths.MountPoint)
		return nil
	}

	daemon, _ := cmd.Flags().GetBool("daemon")
	return gosync.Mount(cmd.Context(), runner, cfg, paths, daemon)
}

// ── setup ─────────────────────────────────────────────────────────────────

func newSyncSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "setup",
		Short:        "Install rclone, configure remote, and deploy sync infrastructure",
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	ctx := cmd.Context()

	// 1. Check / install rclone
	fmt.Println("Checking rclone...")
	ver, ok := gosync.CheckRclone(runner)
	if ok {
		fmt.Printf("  ✓ rclone installed (%s)\n", ver)
	} else {
		confirmed, err := ui.Confirm("rclone not found. Install it?", yes)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Aborted. Install rclone manually: https://rclone.org/install/")
			return nil
		}
		if err := gosync.InstallRclone(ctx, runner); err != nil {
			return fmt.Errorf("installing rclone: %w", err)
		}
		ver, ok = gosync.CheckRclone(runner)
		if !ok {
			return fmt.Errorf("rclone installation failed — not found in PATH after install")
		}
		fmt.Printf("  ✓ rclone installed (%s)\n", ver)
	}

	// 2. Check / configure remote
	remote := state.Modules.Sync.Remote
	if remote == "" {
		remote = "gdrive"
	}

	fmt.Printf("Checking remote '%s'...\n", remote)
	if gosync.HasRemote(ctx, runner, remote) {
		fmt.Printf("  ✓ remote '%s' configured\n", remote)
	} else {
		fmt.Printf("  ✗ remote '%s' not found\n", remote)
		confirmed, err := ui.Confirm(fmt.Sprintf("Configure Google Drive remote '%s'?", remote), yes)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Aborted. Configure manually: rclone config")
			return nil
		}
		if dryRun {
			fmt.Printf("  (dry-run) would configure remote '%s'\n", remote)
		} else {
			if err := gosync.ConfigRemote(ctx, remote); err != nil {
				return fmt.Errorf("configuring remote: %w", err)
			}
			if !gosync.HasRemote(ctx, runner, remote) {
				return fmt.Errorf("remote '%s' still not configured after setup", remote)
			}
			fmt.Printf("  ✓ remote '%s' configured\n", remote)
		}
	}

	// Verify remote access
	if !dryRun {
		fmt.Printf("Verifying access to %s:...\n", remote)
		if err := gosync.CheckRemote(ctx, runner, remote); err != nil {
			fmt.Printf("  ⚠ %v\n", err)
			fmt.Println("  Run 'dot sync reconnect' to fix authentication.")
		} else {
			fmt.Printf("  ✓ remote '%s' accessible\n", remote)
		}
	}

	// 3. Configure paths
	defaultLocal := state.Modules.Workspace.Path
	if defaultLocal == "" {
		home, _ := os.UserHomeDir()
		defaultLocal = home + "/ai-workspace"
	}
	localPath, err := ui.Input("Local workspace path", defaultLocal, yes)
	if err != nil {
		return err
	}

	defaultRemotePath := state.Modules.Sync.Path
	if defaultRemotePath == "" {
		defaultRemotePath = "work"
	}
	remotePath, err := ui.Input("Remote path (on Drive)", defaultRemotePath, yes)
	if err != nil {
		return err
	}

	// Update state
	state.Modules.Sync.Remote = remote
	state.Modules.Sync.Path = remotePath
	if state.Modules.Sync.Interval <= 0 {
		state.Modules.Sync.Interval = 300
	}
	state.Modules.Workspace.Path = localPath

	// 4. Deploy filter file
	fmt.Println("Deploying filter file...")
	engine := template.NewEngine()
	filterContent, err := engine.ReadStatic("sync/workspace-filter.txt")
	if err != nil {
		return fmt.Errorf("reading filter template: %w", err)
	}
	if err := runner.MkdirAll(filepath.Dir(paths.FilterFile), 0755); err != nil {
		return fmt.Errorf("creating rclone config dir: %w", err)
	}
	if err := runner.WriteFile(paths.FilterFile, filterContent, 0644); err != nil {
		return fmt.Errorf("writing filter file: %w", err)
	}
	fmt.Printf("  ✓ %s\n", paths.FilterFile)

	// 5. Deploy scheduler
	fmt.Println("Deploying auto-sync scheduler...")
	cfg, err := gosync.ResolveConfig(state)
	if err != nil {
		return err
	}
	sched := gosync.NewScheduler(runner, paths, cfg, engine)
	if err := sched.Install(ctx); err != nil {
		return fmt.Errorf("installing scheduler: %w", err)
	}
	fmt.Println("  ✓ scheduler installed")

	// 6. Save state
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	fmt.Println("\n✓ Sync setup complete. Run 'dot sync' to start syncing.")
	return nil
}

// ── status ────────────────────────────────────────────────────────────────

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
	sched := gosync.NewScheduler(runner, paths, cfg, engine)
	st, err := gosync.GetStatus(cmd.Context(), sched, cfg)
	if err != nil {
		return err
	}

	fmt.Println("Workspace Sync Status")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━")

	if st.RcloneVersion != "" {
		fmt.Printf("  rclone:     %s (%s)\n", st.RcloneVersion, st.RclonePath)
	} else {
		fmt.Println("  rclone:     not installed")
	}

	fmt.Printf("  Local:      %s\n", st.LocalPath)
	fmt.Printf("  Remote:     %s\n", st.RemotePath)
	fmt.Printf("  Filter:     %s\n", st.FilterFile)
	fmt.Printf("  Interval:   %s\n", formatInterval(st.Interval))
	fmt.Printf("  Scheduler:  %s\n", st.SchedulerState)

	if st.MountPoint != "" {
		mountState := "not mounted"
		if st.Mounted {
			mountState = "mounted"
		}
		fmt.Printf("  Mount:      %s (%s)\n", st.MountPoint, mountState)
	}

	if st.LastSyncTime != nil {
		ago := time.Since(*st.LastSyncTime).Truncate(time.Second)
		fmt.Printf("  Last sync:  %s ago\n", ago)
	} else {
		fmt.Println("  Last sync:  (never)")
	}

	if st.LastStats != "" {
		fmt.Printf("  Last stats: %s\n", st.LastStats)
	}

	if st.LastError != "" {
		fmt.Printf("  Last error: %s\n", st.LastError)
	} else {
		fmt.Println("  Last error: (none)")
	}

	return nil
}

// ── log ───────────────────────────────────────────────────────────────────

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

	lines, err := gosync.TailLog(cfg.LogFile, n)
	if err != nil {
		fmt.Printf("No log file found at %s\n", cfg.LogFile)
		return nil
	}

	fmt.Println(lines)
	return nil
}

// ── skip ──────────────────────────────────────────────────────────────────

func newSyncSkipCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skip",
		Short: "Manage files skipped due to permission errors",
		Long: `View or clear the list of files automatically skipped during sync.

Files that fail with Google Drive permission errors (shared files with
read-only access) are added to a skip list so they are not retried.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, paths, _, err := syncBootstrap(cmd)
			if err != nil {
				return err
			}

			entries, err := gosync.LoadSkipList(paths.SkipFile)
			if err != nil {
				return err
			}

			if len(entries) == 0 {
				fmt.Println("No files in skip list.")
				return nil
			}

			fmt.Printf("Skipped files (%d):\n", len(entries))
			for _, p := range entries {
				fmt.Printf("  %s\n", p)
			}
			fmt.Printf("\nClear with: dot sync skip clear\n")
			return nil
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Remove all entries from the skip list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, paths, _, err := syncBootstrap(cmd)
			if err != nil {
				return err
			}

			if err := gosync.ClearSkipList(paths); err != nil {
				return fmt.Errorf("clearing skip list: %w", err)
			}
			fmt.Println("Skip list cleared. All files will be retried on next sync.")
			return nil
		},
	})

	return cmd
}

// ── connect / reconnect ───────────────────────────────────────────────────

func newSyncConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "connect [remote]",
		Short:        "Configure a new Google Drive remote for rclone",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, _, _, runner, err := syncBootstrap(cmd)
			if err != nil {
				return err
			}

			remote := state.Modules.Sync.Remote
			if remote == "" {
				remote = "gdrive"
			}
			if len(args) > 0 {
				remote = args[0]
			}

			if gosync.HasRemote(cmd.Context(), runner, remote) {
				fmt.Printf("Remote '%s' already exists. Use 'dot sync reconnect' to refresh auth.\n", remote)
				return nil
			}

			fmt.Printf("Configuring Google Drive remote '%s'...\n", remote)
			if err := gosync.ConfigRemote(cmd.Context(), remote); err != nil {
				return fmt.Errorf("configuring remote: %w", err)
			}

			fmt.Printf("✓ Remote '%s' configured.\n", remote)
			state.Modules.Sync.Remote = remote
			return config.SaveState(state)
		},
	}
}

func newSyncReconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "reconnect [remote]",
		Short:        "Refresh Google Drive authentication for rclone",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, _, _, runner, err := syncBootstrap(cmd)
			if err != nil {
				return err
			}

			remote := state.Modules.Sync.Remote
			if remote == "" {
				remote = "gdrive"
			}
			if len(args) > 0 {
				remote = args[0]
			}

			if !gosync.HasRemote(cmd.Context(), runner, remote) {
				fmt.Printf("Remote '%s' not found. Use 'dot sync connect' to create it.\n", remote)
				return nil
			}

			fmt.Printf("Reconnecting remote '%s'...\n", remote)
			if err := gosync.ReconnectRemote(cmd.Context(), remote); err != nil {
				return fmt.Errorf("reconnecting: %w", err)
			}

			fmt.Printf("✓ Remote '%s' reconnected.\n", remote)
			return nil
		},
	}
}

// ── pause / resume ────────────────────────────────────────────────────────

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
	sched := gosync.NewScheduler(runner, paths, cfg, engine)

	if sched.State(cmd.Context()) == gosync.SchedulerNotInstalled {
		fmt.Println("Scheduler not installed. Run 'dot sync setup' to configure auto-sync.")
		return nil
	}

	if err := sched.Pause(cmd.Context()); err != nil {
		return fmt.Errorf("pausing scheduler: %w", err)
	}
	fmt.Println("Auto-sync paused.")
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
	sched := gosync.NewScheduler(runner, paths, cfg, engine)

	if sched.State(cmd.Context()) == gosync.SchedulerNotInstalled {
		fmt.Println("Scheduler not installed. Run 'dot sync setup' to configure auto-sync.")
		return nil
	}

	if err := sched.Resume(cmd.Context()); err != nil {
		return fmt.Errorf("resuming scheduler: %w", err)
	}
	fmt.Println("Auto-sync resumed.")
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func formatInterval(seconds int) string {
	if seconds >= 3600 && seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds >= 60 && seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
