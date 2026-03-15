package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newReconfigureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconfigure",
		Short: "Re-run init prompts with current values as defaults",
		Long:  "Edit existing dotfiles configuration interactively, then optionally apply.",
		RunE:  runReconfigure,
	}
}

func runReconfigure(cmd *cobra.Command, _ []string) error {
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if state.Name == "" {
		fmt.Println("No existing configuration found. Running init...")
	} else {
		fmt.Println("Current configuration:")
		printStateSnapshot(state)
		fmt.Println()
	}

	// Re-run the init flow — it uses existing state values as defaults.
	if err := runInit(cmd, nil); err != nil {
		return err
	}

	yes, _ := cmd.Flags().GetBool("yes")

	apply, err := ui.ConfirmBool("Apply updated configuration now?", false, yes)
	if err != nil {
		return err
	}
	if apply {
		fmt.Println()
		return runApply(cmd, nil)
	}

	fmt.Println("Run 'dotfiles apply' when ready to apply.")
	return nil
}
