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
		{"terminal", cfg.Modules.Terminal.Enabled, fmtIf(cfg.Modules.Terminal.Warp, "warp")},
		{"tmux", cfg.Modules.Tmux.Enabled, ""},
		{"workspace", cfg.Modules.Workspace.Enabled, cfg.Modules.Workspace.Path},
		{"ai-tools", cfg.Modules.AITools.Enabled, ""},
		{"fonts", cfg.Modules.Fonts.Enabled, cfg.Modules.Fonts.Family},
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
	fmt.Println()

	return nil
}

func printKV(key, value string) {
	if value == "" {
		value = ui.StyleHint.Render("(unset)")
	} else {
		value = ui.StyleValue.Render(value)
	}
	fmt.Printf("  %s  %s\n", ui.StyleKey.Render(key+":"), value)
}

func fmtIf(cond bool, label string) string {
	if cond {
		return label
	}
	return ""
}
