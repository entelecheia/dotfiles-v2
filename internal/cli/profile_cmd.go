package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/profilesnap"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// newProfileCmd returns `dotfiles profile` — version-aware snapshots of the
// user-level state (config, install/backup lists, optional secrets).
func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Snapshot and restore dotfiles profile state (config, app lists, secrets)",
		Long: `Manage per-host profile snapshots under <backup-root>/profiles/<hostname>/<version>/.

Each snapshot captures:
  - config.yaml          → ~/.config/dotfiles/config.yaml
  - apps/install.yaml    → install list (casks + casks_extra)
  - apps/backup.yaml     → backup list + backup root
  - meta.yaml            → timestamp, tag, hostname, user
  - secrets/             → optional copy of ~/.ssh/age_key* (--include-secrets)

The shared backup root is resolved via --to/--from, the user state
(BackupRoot), an auto-detected Drive "secrets" folder, or a local default.`,
	}
	cmd.AddCommand(newProfileRootCmd())
	cmd.AddCommand(newProfileBackupCmd())
	cmd.AddCommand(newProfileRestoreCmd())
	cmd.AddCommand(newProfileListCmd())
	cmd.AddCommand(newProfilePruneCmd())
	return cmd
}

// --- root ---

func newProfileRootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "root [path]",
		Short: "Show or set the shared backup root for profiles and app-settings",
		Long: `Display or change the backup root directory.

With no arguments, prints the current effective root (state → auto-detect → default).
With a path argument, saves it to state. Use --detect to auto-discover a Google Drive
secrets folder, or --reset to clear the saved value and fall back to auto-detection.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runProfileRoot,
	}
	c.Flags().Bool("detect", false, "Auto-detect Google Drive secrets folder and save")
	c.Flags().Bool("reset", false, "Clear saved root (revert to auto-detect / default)")
	return c
}

func runProfileRoot(cmd *cobra.Command, args []string) error {
	homeOverride, _ := cmd.Flags().GetString("home")
	detect, _ := cmd.Flags().GetBool("detect")
	reset, _ := cmd.Flags().GetBool("reset")

	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	home, _ := os.UserHomeDir()
	if homeOverride != "" {
		home = homeOverride
	}

	switch {
	case reset:
		state.Modules.MacApps.BackupRoot = ""
		if err := persistProfileState(cmd, state); err != nil {
			return err
		}
		effective := resolveBackupRoot(cmd, state, home)
		fmt.Println(ui.StyleSuccess.Render("✓ backup root cleared"))
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Effective:"), ui.StyleValue.Render(effective))

	case detect:
		drive := appsettings.DetectDriveCandidate(home)
		if drive == "" {
			return fmt.Errorf("no Google Drive secrets folder detected under %s", home)
		}
		state.Modules.MacApps.BackupRoot = drive
		if err := persistProfileState(cmd, state); err != nil {
			return err
		}
		fmt.Println(ui.StyleSuccess.Render("✓ backup root set (auto-detected)"))
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Root:"), ui.StyleValue.Render(drive))

	case len(args) == 1:
		path := args[0]
		state.Modules.MacApps.BackupRoot = path
		if err := persistProfileState(cmd, state); err != nil {
			return err
		}
		fmt.Println(ui.StyleSuccess.Render("✓ backup root set"))
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Root:"), ui.StyleValue.Render(path))

	default:
		// Show current
		effective := resolveBackupRoot(cmd, state, home)
		saved := state.Modules.MacApps.BackupRoot
		source := "default"
		if saved != "" {
			source = "state"
		} else if d := appsettings.DetectDriveCandidate(home); d != "" {
			source = "auto-detected (Drive)"
		}
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Root:"), ui.StyleValue.Render(effective))
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Source:"), ui.StyleHint.Render(source))
		if saved != "" {
			fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Saved:"), ui.StyleHint.Render(saved))
		}
	}
	return nil
}

func persistProfileState(cmd *cobra.Command, state *config.UserState) error {
	homeOverride, _ := cmd.Flags().GetString("home")
	if homeOverride != "" {
		return config.SaveStateForHome(homeOverride, state)
	}
	return config.SaveState(state)
}

// --- shared helpers ---

func newProfileEngine(cmd *cobra.Command) (*profilesnap.Engine, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	homeOverride, _ := cmd.Flags().GetString("home")

	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}

	home, _ := os.UserHomeDir()
	if homeOverride != "" {
		home = homeOverride
	}

	statePath := config.StatePath()
	if homeOverride != "" {
		statePath = config.StatePathForHome(homeOverride)
	}

	root := resolveBackupRoot(cmd, state, home)

	hostname, _ := os.Hostname()
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	runner := exec.NewRunner(dryRun, logger)

	return &profilesnap.Engine{
		Runner:     runner,
		HomeDir:    home,
		Root:       root,
		Hostname:   hostname,
		User:       os.Getenv("USER"),
		StatePath:  statePath,
		SecretsDir: filepath.Join(home, ".ssh"),
	}, nil
}

// --- backup ---

func newProfileBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "Create a new versioned snapshot of this host's profile",
		Args:  cobra.NoArgs,
		RunE:  runProfileBackup,
	}
	c.Flags().String("to", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().String("tag", "", "Human-friendly label stored in meta.yaml")
	c.Flags().Bool("include-secrets", false, "Copy ~/.ssh/age_key* into the snapshot")
	return c
}

func runProfileBackup(cmd *cobra.Command, _ []string) error {
	eng, err := newProfileEngine(cmd)
	if err != nil {
		return err
	}
	tag, _ := cmd.Flags().GetString("tag")
	includeSecrets, _ := cmd.Flags().GetBool("include-secrets")

	if _, err := os.Stat(eng.StatePath); err != nil && os.IsNotExist(err) {
		return fmt.Errorf("no state file at %s — run 'dotfiles init' first", eng.StatePath)
	}

	snap, err := eng.Backup(profilesnap.BackupOptions{
		Tag:            tag,
		IncludeSecrets: includeSecrets,
	})
	if err != nil {
		return err
	}
	fmt.Println(ui.StyleSuccess.Render("✓ snapshot created"))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Version:"), ui.StyleValue.Render(snap.Version))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Path:"), ui.StyleValue.Render(snap.Path))
	if snap.Tag != "" {
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Tag:"), ui.StyleValue.Render(snap.Tag))
	}
	if snap.WithSecret {
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Secrets:"), ui.StyleSuccess.Render("included"))
	}
	return nil
}

// --- restore ---

func newProfileRestoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore",
		Short: "Apply a profile snapshot (defaults to the latest) back to this host",
		Args:  cobra.NoArgs,
		RunE:  runProfileRestore,
	}
	c.Flags().String("from", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().String("version", "", "Specific version to restore (default: latest)")
	c.Flags().Bool("latest", false, "Restore the version pointed at by latest.txt (redundant with default)")
	c.Flags().Bool("include-secrets", false, "Restore ~/.ssh/age_key* from the snapshot if present")
	c.Flags().Bool("no-state", false, "Skip copying config.yaml back to ~/.config/dotfiles/")
	return c
}

func runProfileRestore(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	eng, err := newProfileEngine(cmd)
	if err != nil {
		return err
	}
	version, _ := cmd.Flags().GetString("version")
	includeSecrets, _ := cmd.Flags().GetBool("include-secrets")
	noState, _ := cmd.Flags().GetBool("no-state")

	if version == "" {
		v, err := eng.ResolveLatest()
		if err != nil {
			return err
		}
		version = v
	}

	if !yes {
		fmt.Printf("About to overwrite %s from snapshot %s.\n", eng.StatePath, version)
		ok, err := ui.ConfirmBool("Continue?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("aborted")
			return nil
		}
	}

	snap, err := eng.Restore(profilesnap.RestoreOptions{
		Version:        version,
		IncludeSecrets: includeSecrets,
		IncludeState:   !noState,
	})
	if err != nil {
		return err
	}
	fmt.Println(ui.StyleSuccess.Render("✓ restore complete"))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Version:"), ui.StyleValue.Render(snap.Version))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Path:"), ui.StyleValue.Render(snap.Path))
	if snap.Tag != "" {
		fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Tag:"), ui.StyleValue.Render(snap.Tag))
	}
	return nil
}

// --- list ---

func newProfileListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List available profile snapshots for this host",
		Args:  cobra.NoArgs,
		RunE:  runProfileList,
	}
	c.Flags().String("from", "", "Backup root (overrides configured BackupRoot)")
	return c
}

func runProfileList(cmd *cobra.Command, _ []string) error {
	eng, err := newProfileEngine(cmd)
	if err != nil {
		return err
	}
	snaps, err := eng.List()
	if err != nil {
		return err
	}
	fmt.Println(ui.StyleHeader.Render(" Profile Snapshots "))
	fmt.Println()
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Host:"), ui.StyleValue.Render(eng.Hostname))
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render("Root:"), ui.StyleValue.Render(eng.HostRoot()))
	if len(snaps) == 0 {
		fmt.Println()
		fmt.Println(ui.StyleHint.Render("  (no snapshots yet — run 'dotfiles profile backup')"))
		return nil
	}
	fmt.Println()
	for _, s := range snaps {
		marker := "  "
		if s.IsLatest {
			marker = ui.StyleSuccess.Render("★ ")
		}
		extras := []string{}
		if s.Tag != "" {
			extras = append(extras, "tag="+s.Tag)
		}
		if s.WithSecret {
			extras = append(extras, "with-secrets")
		}
		extra := ""
		if len(extras) > 0 {
			extra = "  " + ui.StyleHint.Render("("+strings.Join(extras, ", ")+")")
		}
		fmt.Printf("%s%s  %s%s\n",
			marker,
			ui.StyleValue.Render(s.Version),
			ui.StyleHint.Render(s.CreatedAt.Format("2006-01-02 15:04 UTC")),
			extra)
	}
	fmt.Println()
	return nil
}

// --- prune ---

func newProfilePruneCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "prune",
		Short: "Delete older snapshots, keeping the newest N",
		Args:  cobra.NoArgs,
		RunE:  runProfilePrune,
	}
	c.Flags().String("from", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().Int("keep", 5, "Number of most recent snapshots to keep")
	return c
}

func runProfilePrune(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	eng, err := newProfileEngine(cmd)
	if err != nil {
		return err
	}
	keep, _ := cmd.Flags().GetInt("keep")

	all, err := eng.List()
	if err != nil {
		return err
	}
	if len(all) <= keep {
		fmt.Printf("Nothing to prune (%d snapshots ≤ keep=%d).\n", len(all), keep)
		return nil
	}
	toDelete := len(all) - keep
	if !yes {
		fmt.Printf("About to delete %d snapshot(s) under %s.\n", toDelete, eng.HostRoot())
		ok, err := ui.ConfirmBool("Continue?", false, false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("aborted")
			return nil
		}
	}
	removed, err := eng.Prune(keep)
	if err != nil {
		return err
	}
	fmt.Printf("Pruned %d snapshot(s):\n", len(removed))
	for _, v := range removed {
		fmt.Printf("  - %s\n", v)
	}
	return nil
}

