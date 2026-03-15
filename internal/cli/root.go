package cli

import "github.com/spf13/cobra"

// NewRootCmd creates the root command with all subcommands.
func NewRootCmd(version, commit string) *cobra.Command {
	root := &cobra.Command{
		Use:   "dotfiles",
		Short: "User environment management tool",
		Long:  "dotfiles-v2: Declarative user environment configuration with modular profiles.",
	}
	root.Version = version + " (" + commit + ")"

	// Persistent flags for all subcommands
	root.PersistentFlags().Bool("yes", false, "Unattended mode (skip all prompts)")
	root.PersistentFlags().Bool("dry-run", false, "Show what would be done without executing")
	root.PersistentFlags().String("profile", "", "Profile name (minimal, full)")
	root.PersistentFlags().StringSlice("module", nil, "Run specific modules only")
	root.PersistentFlags().String("config", "", "Path to custom config YAML")

	root.AddCommand(newApplyCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newDiffCmd())
	root.AddCommand(newSecretsCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newReconfigureCmd())
	root.AddCommand(newVersionCmd(version, commit))

	return root
}

// Execute runs the root command.
func Execute(version, commit string) error {
	return NewRootCmd(version, commit).Execute()
}
