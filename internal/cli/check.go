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
	p := printerFrom(cmd)
	p.Line("Profile: %s\n", profileName)
	p.Line("%-15s %-10s %s", "MODULE", "STATUS", "CHANGES")
	p.Line("%-15s %-10s %s", "------", "------", "-------")

	okCount, pendingCount, totalChanges := 0, 0, 0
	for _, m := range modules {
		r := results[m.Name()]
		status := "OK"
		if !r.Satisfied {
			status = "PENDING"
			pendingCount++
			totalChanges += len(r.Changes)
		} else {
			okCount++
		}
		changeCount := len(r.Changes)
		p.Line("%-15s %-10s %d change(s)", m.Name(), status, changeCount)
		for _, c := range r.Changes {
			p.Line("  → %s", c.Description)
		}
	}

	p.Line("")
	if pendingCount == 0 {
		p.Line("All %d modules satisfied.", okCount)
	} else {
		p.Line("%d/%d modules satisfied, %d pending (%d change(s)).",
			okCount, okCount+pendingCount, pendingCount, totalChanges)
		p.Line("Run 'dotfiles apply' to apply pending changes.")
	}

	return nil
}
