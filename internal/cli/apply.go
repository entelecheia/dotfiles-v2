package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/module"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

var errAborted = errors.New("aborted by user")

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
	homeOverride, _ := cmd.Flags().GetString("home")

	// Check env vars
	if os.Getenv("DOTFILES_YES") == "true" {
		yes = true
	}
	if profileName == "" {
		profileName = os.Getenv("DOTFILES_PROFILE")
	}
	if homeOverride == "" {
		homeOverride = os.Getenv("DOTFILES_HOME")
	}

	// Resolve home directory
	home := homeOverride
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	// Load user state (from target home if overridden)
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

	// Interactive configuration
	p := printerFrom(cmd)
	state.Profile = profileName
	if err := configureInteractive(p, state, profileName, yes); err != nil {
		if errors.Is(err, errAborted) {
			p.Line("Aborted.")
			return nil
		}
		return err
	}

	// Save updated state
	if homeOverride != "" {
		if err := config.SaveStateForHome(homeOverride, state); err != nil {
			return fmt.Errorf("saving state: %w", err)
		}
	} else {
		if err := config.SaveState(state); err != nil {
			return fmt.Errorf("saving state: %w", err)
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

	// Build module list
	registry := module.NewRegistry()
	modules := registry.Resolve(cfg, moduleFilter)

	if len(modules) == 0 {
		p.Line("No modules to apply.")
		return nil
	}

	// Show plan
	p.Line("\nProfile: %s", profileName)
	if dryRun {
		p.Line("Mode: dry-run (no changes will be made)")
	}
	if homeOverride != "" {
		p.Line("Home: %s", homeOverride)
	}
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		names = append(names, m.Name())
	}
	p.Line("Modules: %s", strings.Join(names, ", "))

	// Final confirmation
	if !yes && !dryRun {
		confirmed, err := ui.Confirm("Apply this configuration?", false)
		if err != nil {
			return err
		}
		if !confirmed {
			return errAborted
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

	p.Line("")
	if err := module.RunAll(ctx, modules, rc); err != nil {
		return err
	}

	// Refresh shell completion scripts from the current cobra tree so any
	// new subcommand (e.g. gdrive-sync) tab-completes after the next shell
	// session. Failures here are logged but don't fail apply — completion
	// is a UX nicety, not a correctness requirement.
	if changed, err := installCompletions(cmd.Root(), runner, home); err != nil {
		runner.Logger.Warn("installing shell completions failed", "err", err)
	} else if changed && !dryRun {
		p.Line("✓ shell completions refreshed in %s", completionDir(home))
	}
	return nil
}

// configureInteractive walks through each configuration section interactively.
// Skipped entirely when --yes is set.
func configureInteractive(p *Printer, state *config.UserState, profile string, yes bool) error {
	if yes {
		return nil
	}

	p.Line("\n=== Configuration ===")

	if err := ui.ConfigureIdentity(state, false); err != nil {
		return err
	}

	if err := ui.ConfigureSSH(state, false); err != nil {
		return err
	}

	if err := ui.ConfigureWorkspace(state, profile, false); err != nil {
		return err
	}

	if err := ui.ConfigureAITools(state, false); err != nil {
		return err
	}

	if err := ui.ConfigureTerminal(state, profile, false); err != nil {
		return err
	}

	if err := ui.ConfigureFonts(state, profile, false); err != nil {
		return err
	}

	ui.PrintStateSummary(state)
	return nil
}
