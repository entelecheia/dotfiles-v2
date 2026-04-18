package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show current configuration",
		Long:  "Display active profile, system info, enabled modules, and user settings.",
		RunE:  runConfig,
	}
	cmd.AddCommand(newConfigExportCmd())
	return cmd
}

func newConfigExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [path]",
		Short: "Export configuration to a portable YAML file",
		Long: `Export the current dotfiles configuration to stdout or a file.
The exported file can be used on another machine with 'dotfiles init --from <file>'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runConfigExport,
	}
}

func runConfigExport(cmd *cobra.Command, args []string) error {
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

	if state.Name == "" {
		return fmt.Errorf("no configuration found — run 'dotfiles init' first")
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if len(args) == 0 {
		fmt.Print(string(data))
		return nil
	}

	path := args[0]
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}
	fmt.Printf("Configuration exported to %s\n", path)
	return nil
}

func runConfig(cmd *cobra.Command, _ []string) error {
	profileName, _ := cmd.Flags().GetString("profile")
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

	fmt.Println()
	fmt.Println(ui.StyleHeader.Render(" dotfiles Configuration "))
	fmt.Println()
	printKV("Profile", profileName)
	printKV("Config", config.StatePath())

	fmt.Println(ui.StyleSection.Render("▸ System"))
	printKV("OS", sysInfo.OS+"/"+sysInfo.Arch)
	printKV("Hostname", sysInfo.Hostname)
	printKV("Shell", sysInfo.Shell)
	if sysInfo.HasBrew {
		printKV("Brew", sysInfo.BrewPath)
	}
	if sysInfo.HasGit {
		printKV("Git", sysInfo.GitVersion)
	}
	if sysInfo.HasNVIDIAGPU {
		printKV("GPU", sysInfo.GPUModel)
	}
	if sysInfo.HasCUDA {
		printKV("CUDA", sysInfo.CUDAHome)
	}

	fmt.Println()
	fmt.Println(ui.StyleSection.Render("▸ User"))
	printKV("Name", cfg.Name)
	printKV("Email", cfg.Email)
	printKV("GitHub", cfg.GithubUser)
	printKV("Timezone", cfg.Timezone)

	if cfg.Modules.Workspace.Enabled {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render("▸ Workspace"))
		printKV("Path", cfg.Modules.Workspace.Path)
		if cfg.Modules.Workspace.Gdrive != "" {
			printKV("GDrive", cfg.Modules.Workspace.Gdrive)
		}
		if cfg.Modules.Workspace.GdriveSymlink != "" {
			printKV("GDrive link", cfg.Modules.Workspace.GdriveSymlink)
		}
		if cfg.Modules.Workspace.Symlink != "" {
			printKV("Symlink", cfg.Modules.Workspace.Symlink)
		}
		for _, repo := range cfg.Modules.Workspace.Repos {
			printKV(repo.Name+" repo", repo.Remote)
		}
	}

	fmt.Println()
	fmt.Println(ui.StyleSection.Render("▸ Modules"))
	allModules := []struct {
		name    string
		enabled bool
		detail  string
	}{
		{"packages", cfg.Modules.Packages.Enabled, ""},
		{"shell", cfg.Modules.Shell.Enabled, ""},
		{"node", cfg.Modules.Node.Enabled, ""},
		{"git", cfg.Modules.Git.Enabled, fmtIf(cfg.Modules.Git.Signing, "signing")},
		{"ssh", cfg.Modules.SSH.Enabled, cfg.Modules.SSH.KeyName},
		{"terminal", cfg.Modules.Terminal.Enabled, terminalDetail(cfg)},
		{"tmux", cfg.Modules.Tmux.Enabled, ""},
		{"workspace", cfg.Modules.Workspace.Enabled, workspaceDetail(cfg)},
		{"ai-tools", cfg.Modules.AITools.Enabled, ""},
		{"fonts", cfg.Modules.Fonts.Enabled, cfg.Modules.Fonts.Family},
		{"macapps", cfg.Modules.MacApps.Enabled, macappsDetail(state)},
		{"conda", cfg.Modules.Conda.Enabled, ""},
		{"gpg", cfg.Modules.GPG.Enabled, ""},
		{"secrets", cfg.Modules.Secrets.Enabled, ""},
	}

	for _, m := range allModules {
		mark := ui.StyleHint.Render("✗")
		detail := ""
		if m.enabled {
			mark = ui.StyleSuccess.Render("✓")
			if m.detail != "" {
				detail = ui.StyleHint.Render("  (" + m.detail + ")")
			}
		}
		fmt.Printf("  %s  %s%s\n", mark, ui.StyleValue.Render(m.name), detail)
	}

	pkgs := cfg.AllPackages()
	if len(pkgs) > 0 {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Packages (%d)", len(pkgs))))
		fmt.Printf("  %s\n", ui.StyleHint.Render(strings.Join(pkgs, ", ")))
	}

	casks := cfg.AllCasks()
	if len(casks) > 0 {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Casks — install list (%d)", len(casks))))
		fmt.Printf("  %s\n", ui.StyleHint.Render(strings.Join(casks, ", ")))
	}

	if len(state.Modules.MacApps.BackupApps) > 0 {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render(fmt.Sprintf("▸ Casks — backup list (%d)", len(state.Modules.MacApps.BackupApps))))
		fmt.Printf("  %s\n", ui.StyleHint.Render(strings.Join(state.Modules.MacApps.BackupApps, ", ")))
	}

	if state.Modules.MacApps.BackupRoot != "" {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render("▸ Backup"))
		printKV("Root", state.Modules.MacApps.BackupRoot)
		if state.Modules.MacApps.LastBackup != nil {
			lb := state.Modules.MacApps.LastBackup
			printKV("Last backup", fmt.Sprintf("%s (%d files)", lb.Time.Format("2006-01-02 15:04"), lb.Files))
			printKV("Path", lb.Path)
		}
	}

	if state.Secrets.AgeIdentity != "" {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render("▸ Secrets"))
		printKV("Age identity", state.Secrets.AgeIdentity)
		for i, r := range state.Secrets.AgeRecipients {
			printKV(fmt.Sprintf("Recipient %d", i+1), r)
		}
		if state.Secrets.LastBackup != nil {
			lb := state.Secrets.LastBackup
			printKV("Last backup", fmt.Sprintf("%s (%d files)", lb.Time.Format("2006-01-02 15:04"), lb.Files))
		}
	}

	if state.Modules.Sync.Remote != "" || state.Modules.Rsync.RemoteHost != "" {
		fmt.Println()
		fmt.Println(ui.StyleSection.Render("▸ Sync"))
		if state.Modules.Sync.Remote != "" {
			printKV("rclone", state.Modules.Sync.Remote)
			if state.Modules.Sync.Path != "" {
				printKV("  path", state.Modules.Sync.Path)
			}
			if state.Modules.Sync.Interval > 0 {
				printKV("  interval", fmt.Sprintf("%ds", state.Modules.Sync.Interval))
			}
		}
		if state.Modules.Rsync.RemoteHost != "" {
			printKV("rsync", state.Modules.Rsync.RemoteHost)
			if state.Modules.Rsync.RemotePath != "" {
				printKV("  path", state.Modules.Rsync.RemotePath)
			}
			if state.Modules.Rsync.Interval > 0 {
				printKV("  interval", fmt.Sprintf("%ds", state.Modules.Rsync.Interval))
			}
		}
	}

	fmt.Println()
	return nil
}

// printKV is the os.Stdout-backed shim for callers that don't yet have a
// cobra-aware Printer. New code should prefer `p := printerFrom(cmd); p.KV(...)`
// so output can be captured in tests.
func printKV(key, value string) {
	(&Printer{Out: os.Stdout, Err: os.Stderr}).KV(key, value)
}

func workspaceDetail(cfg *config.Config) string {
	detail := cfg.Modules.Workspace.Path
	if n := len(cfg.Modules.Workspace.Repos); n > 0 {
		detail += fmt.Sprintf(" (%d repo(s))", n)
	}
	return detail
}

func terminalDetail(cfg *config.Config) string {
	parts := []string{}
	if cfg.Modules.Terminal.PromptStyle != "" {
		parts = append(parts, "prompt="+cfg.Modules.Terminal.PromptStyle)
	}
	if cfg.Modules.Terminal.Warp {
		parts = append(parts, "warp")
	}
	return strings.Join(parts, ", ")
}

func macappsDetail(state *config.UserState) string {
	n := len(state.Modules.MacApps.Casks) + len(state.Modules.MacApps.CasksExtra)
	if n == 0 {
		return "defaults"
	}
	return fmt.Sprintf("%d casks", n)
}

func fmtIf(cond bool, label string) string {
	if cond {
		return label
	}
	return ""
}
