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
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check current state against profile",
		Long:  "Check which modules are satisfied and which need changes.",
		RunE:  runCheck,
	}
}

func runCheck(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	profileName, _ := cmd.Flags().GetString("profile")
	moduleFilter, _ := cmd.Flags().GetStringSlice("module")
	configPath, _ := cmd.Flags().GetString("config")

	if profileName == "" {
		profileName = os.Getenv("DOTFILES_PROFILE")
	}

	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if profileName == "" && state.Profile != "" {
		profileName = state.Profile
	}

	sysInfo, err := config.DetectSystem()
	if err != nil {
		return fmt.Errorf("detecting system: %w", err)
	}

	if profileName == "" && configPath == "" {
		profileName = sysInfo.SuggestProfile()
	}

	cfg, err := config.Load(profileName, configPath, sysInfo)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	config.ApplyStateToConfig(cfg, state)
	config.ApplyEnvOverrides(cfg)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	runner := exec.NewRunner(true, logger) // always dry-run for check
	brew := exec.NewBrew(runner)
	tmplEngine := template.NewEngine()

	home, _ := os.UserHomeDir()

	registry := module.NewRegistry()
	modules := registry.Resolve(cfg, moduleFilter)

	rc := &module.RunContext{
		Config:   cfg,
		Runner:   runner,
		Brew:     brew,
		Template: tmplEngine,
		DryRun:   true,
		Yes:      true,
		HomeDir:  home,
	}

	results, err := module.CheckAll(ctx, modules, rc)
	if err != nil {
		return err
	}

	// Print report
	fmt.Printf("Profile: %s\n\n", profileName)
	fmt.Printf("%-15s %-10s %s\n", "MODULE", "STATUS", "CHANGES")
	fmt.Printf("%-15s %-10s %s\n", "------", "------", "-------")

	allSatisfied := true
	for _, m := range modules {
		r := results[m.Name()]
		status := "OK"
		if !r.Satisfied {
			status = "PENDING"
			allSatisfied = false
		}
		changeCount := len(r.Changes)
		fmt.Printf("%-15s %-10s %d change(s)\n", m.Name(), status, changeCount)
		for _, c := range r.Changes {
			fmt.Printf("  → %s\n", c.Description)
		}
	}

	fmt.Println()
	if allSatisfied {
		fmt.Println("All modules satisfied.")
	} else {
		fmt.Println("Run 'dotfiles apply' to apply pending changes.")
	}

	return nil
}
