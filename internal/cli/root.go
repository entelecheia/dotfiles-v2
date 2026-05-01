package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command with all subcommands.
func NewRootCmd(version, commit string) *cobra.Command {
	root := &cobra.Command{
		Use:   "dotfiles",
		Short: "User environment & workspace management tool",
		Long: `dotfiles-v2: Declarative user environment configuration with modular profiles.

Run without arguments to see a getting-started guide.
Run 'dotfiles usecase' for detailed workflow examples.
Also available as 'dot' for convenience.`,
		Aliases:       []string{"dot"},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No subcommand → show friendly welcome screen
			printWelcome(cmd, version, commit)
			return nil
		},
	}
	resolvedVer, resolvedCommit := ResolveVersion(version, commit)
	root.Version = resolvedVer + " (" + resolvedCommit + ")"

	// Persistent flags for all subcommands
	root.PersistentFlags().Bool("yes", false, "Unattended mode (skip all prompts)")
	root.PersistentFlags().Bool("dry-run", false, "Show what would be done without executing")
	root.PersistentFlags().String("profile", "", "Profile name (minimal, full, server)")
	root.PersistentFlags().StringSlice("module", nil, "Run specific modules only")
	root.PersistentFlags().String("config", "", "Path to custom config YAML")
	root.PersistentFlags().String("home", "", "Override home directory (for admin setup of other users)")

	// Existing dotfiles commands
	root.AddCommand(newApplyCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newDiffCmd())
	root.AddCommand(newPreflightCmd())
	root.AddCommand(newSecretsCmd())
	root.AddCommand(newUpgradeCmd(version))
	root.AddCommand(newReconfigureCmd())
	root.AddCommand(newVersionCmd(version, commit))
	root.AddCommand(newConfigCmd())
	root.AddCommand(newUsecaseCmd())

	// Workspace cleanup
	root.AddCommand(newCleanCmd())

	// Drive management commands
	root.AddCommand(newDriveExcludeCmd())

	// Sync (rsync binary sync to remote server)
	root.AddCommand(newSyncCmd())

	// Gdrive-sync (local rsync mirror between workspace and gdrive-workspace)
	root.AddCommand(newGdriveSyncCmd())

	// Clone (rclone Google Drive sync)
	root.AddCommand(newCloneCmd())

	// Workspace commands
	root.AddCommand(newOpenCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newUnregisterCmd())
	root.AddCommand(newLayoutsCmd())
	root.AddCommand(newDoctorCmd())

	// macOS apps + settings backup/restore
	root.AddCommand(newAppsCmd())

	// Profile snapshots (config + app lists + optional secrets)
	root.AddCommand(newProfileCmd())

	// Dual-workspace ops
	root.AddCommand(newWorkspaceDualCmd())
	root.AddCommand(newWsMkdirAliasCmd())
	root.AddCommand(newWsMvAliasCmd())
	root.AddCommand(newWsRmAliasCmd())
	root.AddCommand(newWsAuditAliasCmd())
	root.AddCommand(newWsReconcileAliasCmd())

	return root
}

// knownSubcommands is the set of all registered subcommand names + built-ins.
// Used by Execute to decide whether to inject "open" for implicit project routing.
func knownSubcommands(cmd *cobra.Command) map[string]bool {
	names := map[string]bool{
		"help":       true,
		"completion": true,
	}
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
		for _, alias := range sub.Aliases {
			names[alias] = true
		}
	}
	return names
}

// Execute runs the root command.
func Execute(version, commit string) error {
	cmd := NewRootCmd(version, commit)

	// If the first arg is not a known subcommand or flag, show an error
	// guiding the user to `dotfiles open <project>` (explicit is safer than
	// implicit routing which could mask typos as project launches).
	if len(os.Args) > 1 {
		first := os.Args[1]
		if first != "" && first[0] != '-' {
			known := knownSubcommands(cmd)
			if !known[first] {
				fmt.Fprintf(os.Stderr, "Unknown command %q\n", first)
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintf(os.Stderr, "If you meant to launch a workspace, use:\n")
				fmt.Fprintf(os.Stderr, "  dotfiles open %s\n", first)
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "See 'dotfiles help' for available commands, or 'dotfiles usecase' for examples.")
				os.Exit(1)
			}
		}
	}

	return cmd.Execute()
}
