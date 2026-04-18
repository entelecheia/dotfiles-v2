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

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Show pending changes without applying",
		Long:  "Show what would change if 'dotfiles apply' were run (dry-run with verbose change details).",
		RunE:  runDiff,
	}
}

func runDiff(cmd *cobra.Command, _ []string) error {
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
	runner := exec.NewRunner(true, logger)
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

	p := printerFrom(cmd)
	p.Header("Dry-run Diff")
	p.KV("Profile", profileName)
	p.Section("Modules")

	pendingCount := 0
	for _, m := range modules {
		r := results[m.Name()]
		if r.Satisfied {
			marker := ui.StyleSuccess.Render(ui.MarkPresent)
			p.Bullet(marker, fmt.Sprintf("%-15s %s", ui.StyleValue.Render(m.Name()), ui.StyleHint.Render("already satisfied")))
			continue
		}
		pendingCount++
		marker := ui.StyleHint.Render(ui.MarkPending)
		p.Bullet(marker, fmt.Sprintf("%-15s %s", ui.StyleValue.Render(m.Name()),
			ui.StyleWarning.Render(fmt.Sprintf("%d pending change(s)", len(r.Changes)))))
		for _, c := range r.Changes {
			p.Line("      → %s", c.Description)
			if c.Command != "" {
				p.Line("        $ %s", c.Command)
			}
		}
	}

	p.Blank()
	if pendingCount == 0 {
		p.Success("Nothing to do — all modules satisfied.")
	} else {
		p.Line("%d module(s) have pending changes. Run 'dotfiles apply' to apply them.", pendingCount)
	}

	return nil
}
