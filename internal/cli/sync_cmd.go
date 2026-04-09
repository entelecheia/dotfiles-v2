package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
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
		Short: "Sync workspace with Google Drive via rclone bisync",
		Long: `Bidirectional sync between local workspace and Google Drive using rclone bisync.

Getting started:
  dot sync setup       Full guided setup (install rclone, auth, filter, scheduler)
  dot sync connect     Configure a new Google Drive remote
  dot sync reconnect   Fix expired authentication

Daily use:
  dot sync             Trigger immediate sync
  dot sync status      Show sync health, last run, scheduler state
  dot sync log         View recent sync log entries

Scheduler control:
  dot sync pause       Temporarily stop auto-sync
  dot sync resume      Restart auto-sync`,
		RunE:         runSyncNow,
		SilenceUsage: true,
	}

	cmd.Flags().Bool("resync", false, "Force initial baseline sync (first-time only)")
	cmd.PersistentFlags().BoolP("verbose", "V", false, "Show rclone progress output")

	cmd.AddCommand(
		newSyncStatusCmd(),
		newSyncSetupCmd(),
		newSyncConnectCmd(),
		newSyncReconnectCmd(),
		newSyncResetCmd(),
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

// ── sync (default) ────────────────────────────────────────────────────────

func runSyncNow(cmd *cobra.Command, _ []string) error {
	_, cfg, _, runner, err := syncBootstrap(cmd)
	if err != nil {
		return err
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if !runner.CommandExists("rclone") {
		fmt.Println("rclone is not installed. Run 'dot sync setup' to get started.")
		return nil
	}

	if !runner.FileExists(cfg.FilterFile) {
		fmt.Println("Filter file not found. Run 'dot sync setup' to configure sync.")
		return nil
	}

	resync, _ := cmd.Flags().GetBool("resync")

	fmt.Printf("Syncing %s ⟷ %s\n", cfg.LocalPath, cfg.RemotePath)
	if resync {
		fmt.Println("(--resync mode — establishing initial baseline)")
	}
	if dryRun {
		fmt.Println("(dry-run mode — no changes will be made)")
	}

	err = gosync.Bisync(cmd.Context(), runner, cfg, resync, dryRun)
	if err != nil {
		// Auto-recover from out-of-sync or missing baseline errors
		errMsg := err.Error()
		if strings.Contains(errMsg, "out of sync") || strings.Contains(errMsg, "no sync baseline") {
			paths, perr := gosync.ResolvePaths()
			if perr != nil {
				return fmt.Errorf("bisync failed: %w", err)
			}
			fmt.Println("\nBaseline out of date. Syncing paths and recreating...")
			if serr := gosync.SyncOnce(cmd.Context(), runner, cfg); serr != nil {
				fmt.Printf("  ⚠ Path sync: %v\n", serr)
			}
			if berr := gosync.CreateBaseline(cmd.Context(), runner, cfg, paths); berr != nil {
				return fmt.Errorf("baseline recreation failed: %w (original: %w)", berr, err)
			}
			fmt.Println("Retrying sync...")
			if err2 := gosync.Bisync(cmd.Context(), runner, cfg, false, dryRun); err2 != nil {
				return fmt.Errorf("bisync retry failed: %w", err2)
			}
			fmt.Println("Sync complete (after baseline refresh).")
			return nil
		}
		return fmt.Errorf("bisync failed: %w", err)
	}

	fmt.Println("Sync complete.")
	return nil
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
		}
		if !dryRun {
			if !gosync.HasRemote(ctx, runner, remote) {
				return fmt.Errorf("remote '%s' still not configured after setup", remote)
			}
			fmt.Printf("  ✓ remote '%s' configured\n", remote)
		}
	}

	// Verify remote access
	remoteAccessible := false
	if !dryRun {
		fmt.Printf("Verifying access to %s:...\n", remote)
		if err := gosync.CheckRemote(ctx, runner, remote); err != nil {
			fmt.Printf("  ⚠ %v\n", err)
			fmt.Println("  Continue anyway — you may need to re-authenticate later.")
		} else {
			fmt.Printf("  ✓ remote '%s' accessible\n", remote)
			remoteAccessible = true
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
	// Re-resolve config with updated state
	state.Modules.Workspace.Path = localPath
	cfg, err := gosync.ResolveConfig(state)
	if err != nil {
		return err
	}

	// 4. Deploy filter file
	fmt.Println("Deploying filter file...")
	engine := template.NewEngine()
	filterContent, err := engine.ReadStatic("sync/workspace-filter.txt")
	if err != nil {
		return fmt.Errorf("reading filter template: %w", err)
	}
	filterDir := paths.FilterFile[:len(paths.FilterFile)-len("/workspace-filter.txt")]
	if err := runner.MkdirAll(filterDir, 0755); err != nil {
		return fmt.Errorf("creating rclone config dir: %w", err)
	}
	if err := runner.WriteFile(paths.FilterFile, filterContent, 0644); err != nil {
		return fmt.Errorf("writing filter file: %w", err)
	}
	fmt.Printf("  ✓ %s\n", paths.FilterFile)

	// 5. Deploy scheduler
	fmt.Println("Deploying auto-sync scheduler...")
	sched := gosync.NewScheduler(runner, paths, cfg, engine)
	if err := sched.Install(ctx); err != nil {
		return fmt.Errorf("installing scheduler: %w", err)
	}
	fmt.Println("  ✓ scheduler installed")

	// 6. Create bisync baseline (listing-based, avoids --resync pitfalls)
	if !dryRun && remoteAccessible {
		if gosync.HasBaseline(paths, cfg) {
			fmt.Println("\nBaseline already exists. Use 'dot sync reset' to recreate.")
		} else {
			fmt.Println("\nCreating bisync baseline...")
			if err := gosync.CreateBaseline(ctx, runner, cfg, paths); err != nil {
				fmt.Printf("  ⚠ Baseline creation failed: %v\n", err)
				fmt.Println("  You can retry with: dot sync reset")
			}
		}
	} else if !dryRun && !remoteAccessible {
		fmt.Println("\n⚠ Skipping baseline — remote not accessible.")
		fmt.Println("  To fix authentication:")
		fmt.Println("    dot sync reconnect")
		fmt.Println("  Then create baseline:")
		fmt.Println("    dot sync reset")
	}

	// 7. Save state
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	fmt.Println("\n✓ Sync setup complete.")
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

	if st.LastSyncTime != nil {
		ago := time.Since(*st.LastSyncTime).Truncate(time.Second)
		fmt.Printf("  Last sync:  %s ago\n", ago)
	} else {
		fmt.Println("  Last sync:  (never)")
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

// ── reset ─────────────────────────────────────────────────────────────────

func newSyncResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Recreate bisync baseline from current file listings",
		Long: `Generates fresh bisync baseline by listing files on both sides.

This avoids the --resync flag which can fail on Google Drive workspaces
with shared files (permission errors) or large file counts (quota errors).

Use this when:
  - Initial setup failed to create a baseline
  - Bisync reports "cannot find prior Path1 or Path2 listings"
  - You want to start fresh after sync issues`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, cfg, paths, runner, err := syncBootstrap(cmd)
			if err != nil {
				return err
			}

			if !runner.CommandExists("rclone") {
				fmt.Println("rclone is not installed. Run 'dot sync setup' to get started.")
				return nil
			}

			fmt.Println("Syncing paths to match (one-way copy)...")
			if err := gosync.SyncOnce(cmd.Context(), runner, cfg); err != nil {
				fmt.Printf("  ⚠ Sync failed: %v\n  Continuing with baseline creation...\n", err)
			}

			fmt.Println("Removing old baseline...")
			gosync.RemoveBaseline(paths, cfg)

			fmt.Println("Creating new bisync baseline...")
			if err := gosync.CreateBaseline(cmd.Context(), runner, cfg, paths); err != nil {
				return fmt.Errorf("baseline creation failed: %w", err)
			}

			fmt.Println("\n✓ Baseline recreated. Run 'dot sync' to start syncing.")
			return nil
		},
	}
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

			// Save remote name to state
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
