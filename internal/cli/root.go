package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command with all subcommands.
func NewRootCmd(version, commit string) *cobra.Command {
	root := &cobra.Command{
		Use:   "dotfiles",
		Short: "User environment & workspace management tool",
		Long: `dotfiles-v2: Declarative user environment configuration with modular profiles.

Also available as 'dot' for convenience. Workspace commands:
  dotfiles open <project>   Launch or resume a tmux workspace
  dotfiles list             Show registered projects and active sessions
  dotfiles register <name>  Register current directory as a project
  dotfiles layouts          List available workspace layouts
  dotfiles doctor           Check tool installation status`,
		Aliases: []string{"dot"},
	}
	root.Version = version + " (" + commit + ")"

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
	root.AddCommand(newInitCmd())
	root.AddCommand(newDiffCmd())
	root.AddCommand(newPreflightCmd())
	root.AddCommand(newSecretsCmd())
	root.AddCommand(newUpgradeCmd(version))
	root.AddCommand(newReconfigureCmd())
	root.AddCommand(newVersionCmd(version, commit))
	root.AddCommand(newConfigCmd())

	// Drive management commands
	root.AddCommand(newDriveExcludeCmd())

	// Sync commands
	root.AddCommand(newSyncCmd())

	// Workspace commands
	root.AddCommand(newOpenCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newUnregisterCmd())
	root.AddCommand(newLayoutsCmd())
	root.AddCommand(newDoctorCmd())

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

	// Implicit project routing: if the first arg is not a known subcommand
	// or flag, prepend "open" so `dot myproject` works as `dot open myproject`.
	if len(os.Args) > 1 {
		first := os.Args[1]
		if first != "" && first[0] != '-' {
			known := knownSubcommands(cmd)
			if !known[first] {
				os.Args = append([]string{os.Args[0], "open"}, os.Args[1:]...)
			}
		}
	}

	return cmd.Execute()
}
