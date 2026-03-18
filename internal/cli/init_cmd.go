package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive setup for dotfiles",
		Long:  "Collect user preferences and save them to the dotfiles state file.",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	homeOverride, _ := cmd.Flags().GetString("home")

	var state *config.UserState
	var err error
	if homeOverride != "" {
		state, err = config.LoadStateForHome(homeOverride)
	} else {
		state, err = config.LoadState()
	}
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// If state already has data, ask whether to reconfigure.
	if state.Name != "" && !yes {
		fmt.Printf("Current configuration:\n")
		ui.PrintStateSummary(state)
		fmt.Println()

		reconfigure, err := ui.ConfirmBool("Reconfigure existing settings?", false, false)
		if err != nil {
			return err
		}
		if !reconfigure {
			fmt.Println("Keeping existing configuration.")
			return nil
		}
	}

	// --- Identity ---
	if err := ui.ConfigureIdentity(state, yes); err != nil {
		return err
	}

	// --- Profile ---
	sysInfo, err := config.DetectSystem()
	if err != nil {
		return fmt.Errorf("detecting system: %w", err)
	}
	if err := ui.ConfigureProfile(state, sysInfo.SuggestProfile(), yes); err != nil {
		return err
	}

	// --- SSH ---
	if err := ui.ConfigureSSH(state, yes); err != nil {
		return err
	}

	// --- Workspace ---
	if err := ui.ConfigureWorkspace(state, state.Profile, yes); err != nil {
		return err
	}

	// --- AI Tools ---
	if err := ui.ConfigureAITools(state, yes); err != nil {
		return err
	}

	// --- Terminal ---
	if err := ui.ConfigureTerminal(state, state.Profile, yes); err != nil {
		return err
	}

	// --- Fonts ---
	if err := ui.ConfigureFonts(state, state.Profile, yes); err != nil {
		return err
	}

	// --- Persist ---
	if homeOverride != "" {
		if err := config.SaveStateForHome(homeOverride, state); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
	} else {
		if err := config.SaveState(state); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("Configuration saved.")
	statePath := config.StatePath()
	if homeOverride != "" {
		statePath = config.StatePathForHome(homeOverride)
	}
	fmt.Printf("  State file: %s\n", statePath)
	ui.PrintStateSummary(state)
	fmt.Println()
	fmt.Println("Run 'dotfiles apply' to apply the configuration.")
	return nil
}
