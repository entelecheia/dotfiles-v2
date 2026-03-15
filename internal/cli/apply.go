package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/module"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Apply dotfiles configuration",
		Long:  "Apply the selected profile's configuration to the user environment.",
		RunE:  runApply,
	}
}

func runApply(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	profileName, _ := cmd.Flags().GetString("profile")
	moduleFilter, _ := cmd.Flags().GetStringSlice("module")
	configPath, _ := cmd.Flags().GetString("config")

	// Check env vars
	if os.Getenv("DOTFILES_YES") == "true" {
		yes = true
	}
	if profileName == "" {
		profileName = os.Getenv("DOTFILES_PROFILE")
	}

	// Load user state
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Use profile from state if not specified
	if profileName == "" && state.Profile != "" {
		profileName = state.Profile
	}

	// Detect system
	sysInfo, err := config.DetectSystem()
	if err != nil {
		return fmt.Errorf("detecting system: %w", err)
	}

	// Select profile
	if profileName == "" && configPath == "" {
		suggested := sysInfo.SuggestProfile()
		if yes {
			profileName = suggested
		} else {
			profileName, err = ui.Select(
				"Select profile",
				config.AvailableProfiles(),
				suggested,
				false,
			)
			if err != nil {
				return err
			}
		}
	}

	// Load config from profile
	cfg, err := config.Load(profileName, configPath, sysInfo)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply user state to config
	config.ApplyStateToConfig(cfg, state)

	// Apply env overrides
	config.ApplyEnvOverrides(cfg)

	// Setup runner
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	runner := exec.NewRunner(dryRun, logger)
	brew := exec.NewBrew(runner)
	tmplEngine := template.NewEngine()

	home, _ := os.UserHomeDir()

	// Build module list
	registry := module.NewRegistry()
	modules := registry.Resolve(cfg, moduleFilter)

	if len(modules) == 0 {
		fmt.Println("No modules to apply.")
		return nil
	}

	// Show plan
	fmt.Printf("Profile: %s\n", profileName)
	if dryRun {
		fmt.Println("Mode: dry-run (no changes will be made)")
	}
	fmt.Printf("Modules: ")
	for i, m := range modules {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(m.Name())
	}
	fmt.Println()

	if !yes && !dryRun {
		confirmed, err := ui.Confirm("Apply this configuration?", false)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Execute modules
	rc := &module.RunContext{
		Config:   cfg,
		Runner:   runner,
		Brew:     brew,
		Template: tmplEngine,
		DryRun:   dryRun,
		Yes:      yes,
		HomeDir:  home,
	}

	fmt.Println()
	return module.RunAll(ctx, modules, rc)
}
