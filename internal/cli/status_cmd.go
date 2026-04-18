package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/module"
	"github.com/entelecheia/dotfiles-v2/internal/rsync"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
	"github.com/entelecheia/dotfiles-v2/internal/workspace"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show full environment status at a glance",
		Long:  "Unified dashboard: system, user, modules, secrets, sync, and workspace projects.",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, _ []string) error {
	p := printerFrom(cmd)
	p.Header("dotfiles Status")

	// ── load shared state ──────────────────────────────────────────────
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	sysInfo, _ := config.DetectSystem()

	// ── System ─────────────────────────────────────────────────────────
	p.Section("System")
	if sysInfo != nil {
		p.KV("OS", sysInfo.OS+"/"+sysInfo.Arch)
		p.KV("Hostname", sysInfo.Hostname)
		p.KV("Shell", sysInfo.Shell)
		if sysInfo.HasGit {
			p.KV("Git", sysInfo.GitVersion)
		}
		if sysInfo.HasBrew {
			p.KV("Brew", sysInfo.BrewPath)
		}
		if sysInfo.HasNVIDIAGPU {
			p.KV("GPU", sysInfo.GPUModel)
		}
		if sysInfo.HasCUDA {
			p.KV("CUDA", sysInfo.CUDAHome)
		}
	}

	// ── User ───────────────────────────────────────────────────────────
	p.Section("User")
	p.KV("Name", state.Name)
	p.KV("Email", state.Email)
	p.KV("GitHub", state.GithubUser)
	p.KV("Profile", state.Profile)
	p.KV("Config", config.StatePath())

	// ── Modules ────────────────────────────────────────────────────────
	statusPrintModules(p, cmd, state, sysInfo)

	// ── Secrets ────────────────────────────────────────────────────────
	statusPrintSecrets(p, state)

	// ── Sync ───────────────────────────────────────────────────────────
	statusPrintSync(p, state)

	// ── Workspace ──────────────────────────────────────────────────────
	statusPrintWorkspace(p)

	p.Blank()
	return nil
}

// statusPrintModules checks all enabled modules and prints a compact summary.
func statusPrintModules(p *Printer, cmd *cobra.Command, state *config.UserState, sysInfo *config.SystemInfo) {
	profileName, _ := cmd.Flags().GetString("profile")
	configPath, _ := cmd.Flags().GetString("config")
	if profileName == "" {
		profileName = os.Getenv("DOTFILES_PROFILE")
	}
	if profileName == "" {
		profileName = state.Profile
	}
	if profileName == "" && sysInfo != nil && configPath == "" {
		profileName = sysInfo.SuggestProfile()
	}

	cfg, err := config.Load(profileName, configPath, sysInfo)
	if err != nil {
		p.Section("Modules")
		p.Line("  %s", ui.StyleHint.Render("(could not load config: "+err.Error()+")"))
		return
	}
	config.ApplyStateToConfig(cfg, state)
	config.ApplyEnvOverrides(cfg)

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(true, logger)
	brew := exec.NewBrew(runner)
	tmplEngine := template.NewEngine()
	home, _ := os.UserHomeDir()

	registry := module.NewRegistry()
	modules := registry.Resolve(cfg, nil)

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
		p.Section("Modules")
		p.Line("  %s", ui.StyleHint.Render("(check failed: "+err.Error()+")"))
		return
	}

	satisfied := 0
	total := len(modules)
	for _, m := range modules {
		if r := results[m.Name()]; r != nil && r.Satisfied {
			satisfied++
		}
	}

	p.Section(fmt.Sprintf("Modules (%d/%d satisfied)", satisfied, total))
	for _, m := range modules {
		r := results[m.Name()]
		marker := ui.StyleHint.Render(ui.MarkAbsent)
		if r != nil && r.Satisfied {
			marker = ui.StyleSuccess.Render(ui.MarkPresent)
		}
		p.Bullet(marker, ui.StyleValue.Render(m.Name()))
	}

	if pending := total - satisfied; pending > 0 {
		p.Blank()
		p.Line("  %s", ui.StyleHint.Render(
			fmt.Sprintf("%d module(s) need attention — run 'dotfiles check' for details.", pending)))
	}
}

// statusPrintSecrets prints a compact secrets summary.
func statusPrintSecrets(p *Printer, state *config.UserState) {
	p.Section("Secrets")

	home, err := os.UserHomeDir()
	if err != nil {
		p.Line("  %s", ui.StyleHint.Render("(cannot resolve home dir)"))
		return
	}

	keyName := state.SSH.KeyName
	if keyName == "" {
		keyName = "id_ed25519"
	}

	fileStatus := func(path string) string {
		if _, err := os.Stat(path); err == nil {
			return "present"
		}
		return "missing"
	}

	p.KV("SSH key", fileStatus(filepath.Join(home, ".ssh", keyName)))
	p.KV("Shell secrets", fileStatus(filepath.Join(home, ".config", "shell", "90-secrets.sh")))

	// Count .age files
	storeDir := filepath.Join(home, ".local", "share", "dotfiles-secrets")
	ageCount := 0
	if entries, err := os.ReadDir(storeDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".age" {
				ageCount++
			}
		}
	}
	p.KV("Encrypted", fmt.Sprintf("%d file(s)", ageCount))

	if lb := state.Secrets.LastBackup; lb != nil && lb.Path != "" {
		p.KV("Last backup", fmt.Sprintf("%s (%s ago, %d files)",
			lb.Path, humanDuration(time.Since(lb.Time)), lb.Files))
	} else {
		p.KV("Last backup", "(none)")
	}
}

// statusPrintSync prints a compact rsync sync summary.
func statusPrintSync(p *Printer, state *config.UserState) {
	p.Section("Sync")

	if state.Modules.Rsync.RemoteHost == "" {
		p.Line("  %s", ui.StyleHint.Render("(not configured — run 'dotfiles sync setup')"))
		return
	}

	syncCfg, err := rsync.ResolveConfig(state)
	if err != nil {
		p.Line("  %s", ui.StyleHint.Render("(config error: "+err.Error()+")"))
		return
	}

	paths, err := rsync.ResolvePaths()
	if err != nil {
		p.Line("  %s", ui.StyleHint.Render("(cannot resolve paths)"))
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(true, logger)
	engine := template.NewEngine()
	sched := rsync.NewScheduler(runner, paths, syncCfg, engine)

	st, err := rsync.GetStatus(context.Background(), sched, syncCfg)
	if err != nil {
		p.Line("  %s", ui.StyleHint.Render("(status unavailable: "+err.Error()+")"))
		return
	}

	p.KV("Remote", st.RemoteHost+":"+st.RemotePath)
	p.KV("Scheduler", st.SchedulerState.String())

	if st.LastSyncTime != nil {
		p.KV("Last sync", humanDuration(time.Since(*st.LastSyncTime))+" ago")
	} else {
		p.KV("Last sync", "(never)")
	}

	if st.LastResult != "" {
		p.KV("Last result", st.LastResult)
	}
}

// statusPrintWorkspace lists registered projects and active tmux sessions.
func statusPrintWorkspace(p *Printer) {
	cfg, err := workspace.LoadConfig()
	if err != nil {
		p.Section("Workspace")
		p.Line("  %s", ui.StyleHint.Render("(could not load config: "+err.Error()+")"))
		return
	}

	p.Section(fmt.Sprintf("Workspace (%d projects)", len(cfg.Projects)))

	if len(cfg.Projects) == 0 {
		p.Line("  %s", ui.StyleHint.Render("(none — use 'dotfiles register <name>' to add one)"))
		return
	}

	// Collect active tmux sessions
	active := make(map[string]bool)
	runner := exec.NewRunner(false, slog.Default())
	res, err := runner.RunQuery(context.Background(), "tmux", "list-sessions", "-F", "#{session_name}")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			if line != "" {
				active[line] = true
			}
		}
	}

	for _, proj := range cfg.Projects {
		marker := ui.StyleHint.Render(ui.MarkPartial)
		if active[proj.Name] {
			marker = ui.StyleSuccess.Render(ui.MarkStarred)
		}
		p.Bullet(marker, fmt.Sprintf("%s  %s",
			ui.StyleValue.Render(fmt.Sprintf("%-18s", proj.Name)),
			ui.StyleHint.Render(proj.Path)))
	}
}
