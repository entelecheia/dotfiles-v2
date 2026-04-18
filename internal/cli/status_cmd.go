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
	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" dotfiles Status "))
	fmt.Println()

	// ── load shared state ──────────────────────────────────────────────
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	sysInfo, _ := config.DetectSystem()

	// ── System ─────────────────────────────────────────────────────────
	fmt.Println(ui.StyleSection.Render("▸ System"))
	if sysInfo != nil {
		printKV("OS", sysInfo.OS+"/"+sysInfo.Arch)
		printKV("Hostname", sysInfo.Hostname)
		printKV("Shell", sysInfo.Shell)
		if sysInfo.HasGit {
			printKV("Git", sysInfo.GitVersion)
		}
		if sysInfo.HasBrew {
			printKV("Brew", sysInfo.BrewPath)
		}
		if sysInfo.HasNVIDIAGPU {
			printKV("GPU", sysInfo.GPUModel)
		}
		if sysInfo.HasCUDA {
			printKV("CUDA", sysInfo.CUDAHome)
		}
	}

	// ── User ───────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(ui.StyleSection.Render("▸ User"))
	printKV("Name", state.Name)
	printKV("Email", state.Email)
	printKV("GitHub", state.GithubUser)
	printKV("Profile", state.Profile)
	printKV("Config", config.StatePath())

	// ── Modules ────────────────────────────────────────────────────────
	statusPrintModules(cmd, state, sysInfo)

	// ── Secrets ────────────────────────────────────────────────────────
	statusPrintSecrets(state)

	// ── Sync ───────────────────────────────────────────────────────────
	statusPrintSync(state)

	// ── Workspace ──────────────────────────────────────────────────────
	statusPrintWorkspace()

	fmt.Println()
	return nil
}

// statusPrintModules checks all enabled modules and prints a compact summary.
func statusPrintModules(cmd *cobra.Command, state *config.UserState, sysInfo *config.SystemInfo) {
	fmt.Println()

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
		fmt.Println(ui.StyleSection.Render("▸ Modules"))
		fmt.Printf("  %s\n", ui.StyleHint.Render("(could not load config: "+err.Error()+")"))
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
		fmt.Println(ui.StyleSection.Render("▸ Modules"))
		fmt.Printf("  %s\n", ui.StyleHint.Render("(check failed: "+err.Error()+")"))
		return
	}

	satisfied := 0
	total := len(modules)
	for _, m := range modules {
		if r := results[m.Name()]; r != nil && r.Satisfied {
			satisfied++
		}
	}

	fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Modules (%d/%d satisfied)", satisfied, total)))
	for _, m := range modules {
		r := results[m.Name()]
		mark := ui.StyleHint.Render("✗")
		if r != nil && r.Satisfied {
			mark = ui.StyleSuccess.Render("✓")
		}
		fmt.Printf("  %s  %s\n", mark, ui.StyleValue.Render(m.Name()))
	}

	if pending := total - satisfied; pending > 0 {
		fmt.Println()
		fmt.Printf("  %s\n", ui.StyleHint.Render(
			fmt.Sprintf("%d module(s) need attention — run 'dotfiles check' for details.", pending)))
	}
}

// statusPrintSecrets prints a compact secrets summary.
func statusPrintSecrets(state *config.UserState) {
	fmt.Println()
	fmt.Println(ui.StyleSection.Render("▸ Secrets"))

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(cannot resolve home dir)"))
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

	printKV("SSH key", fileStatus(filepath.Join(home, ".ssh", keyName)))
	printKV("Shell secrets", fileStatus(filepath.Join(home, ".config", "shell", "90-secrets.sh")))

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
	printKV("Encrypted", fmt.Sprintf("%d file(s)", ageCount))

	if lb := state.Secrets.LastBackup; lb != nil && lb.Path != "" {
		printKV("Last backup", fmt.Sprintf("%s (%s ago, %d files)",
			lb.Path, humanDuration(time.Since(lb.Time)), lb.Files))
	} else {
		printKV("Last backup", "(none)")
	}
}

// statusPrintSync prints a compact rsync sync summary.
func statusPrintSync(state *config.UserState) {
	fmt.Println()
	fmt.Println(ui.StyleSection.Render("▸ Sync"))

	if state.Modules.Rsync.RemoteHost == "" {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(not configured — run 'dotfiles sync setup')"))
		return
	}

	syncCfg, err := rsync.ResolveConfig(state)
	if err != nil {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(config error: "+err.Error()+")"))
		return
	}

	paths, err := rsync.ResolvePaths()
	if err != nil {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(cannot resolve paths)"))
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(true, logger)
	engine := template.NewEngine()
	sched := rsync.NewScheduler(runner, paths, syncCfg, engine)

	st, err := rsync.GetStatus(context.Background(), sched, syncCfg)
	if err != nil {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(status unavailable: "+err.Error()+")"))
		return
	}

	printKV("Remote", st.RemoteHost+":"+st.RemotePath)
	printKV("Scheduler", st.SchedulerState.String())

	if st.LastSyncTime != nil {
		printKV("Last sync", humanDuration(time.Since(*st.LastSyncTime))+" ago")
	} else {
		printKV("Last sync", "(never)")
	}

	if st.LastResult != "" {
		printKV("Last result", st.LastResult)
	}
}

// statusPrintWorkspace lists registered projects and active tmux sessions.
func statusPrintWorkspace() {
	fmt.Println()

	cfg, err := workspace.LoadConfig()
	if err != nil {
		fmt.Println(ui.StyleSection.Render("▸ Workspace"))
		fmt.Printf("  %s\n", ui.StyleHint.Render("(could not load config: "+err.Error()+")"))
		return
	}

	fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Workspace (%d projects)", len(cfg.Projects))))

	if len(cfg.Projects) == 0 {
		fmt.Printf("  %s\n", ui.StyleHint.Render("(none — use 'dotfiles register <name>' to add one)"))
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

	for _, p := range cfg.Projects {
		marker := " "
		if active[p.Name] {
			marker = "*"
		}
		fmt.Printf("  %s %s  %s\n",
			ui.StyleSuccess.Render(marker),
			ui.StyleValue.Render(fmt.Sprintf("%-18s", p.Name)),
			ui.StyleHint.Render(p.Path))
	}
}
