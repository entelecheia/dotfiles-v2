package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show current configuration",
		Long:  "Display active profile, system info, enabled modules, and user settings.",
		RunE:  runConfig,
	}
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

	// Profile
	fmt.Printf("Profile: %s\n", profileName)
	fmt.Printf("Config:  %s\n\n", config.StatePath())

	// System
	fmt.Println("System:")
	fmt.Printf("  OS:       %s/%s\n", sysInfo.OS, sysInfo.Arch)
	fmt.Printf("  Hostname: %s\n", sysInfo.Hostname)
	fmt.Printf("  Shell:    %s\n", sysInfo.Shell)
	if sysInfo.HasBrew {
		fmt.Printf("  Brew:     %s\n", sysInfo.BrewPath)
	}
	if sysInfo.HasGit {
		fmt.Printf("  Git:      %s\n", sysInfo.GitVersion)
	}
	if sysInfo.HasNVIDIAGPU {
		fmt.Printf("  GPU:      %s\n", sysInfo.GPUModel)
	}
	if sysInfo.HasCUDA {
		fmt.Printf("  CUDA:     %s\n", sysInfo.CUDAHome)
	}

	// User
	fmt.Println("\nUser:")
	fmt.Printf("  Name:     %s\n", cfg.Name)
	fmt.Printf("  Email:    %s\n", cfg.Email)
	fmt.Printf("  GitHub:   %s\n", cfg.GithubUser)
	fmt.Printf("  Timezone: %s\n", cfg.Timezone)

	// Modules
	fmt.Println("\nModules:")
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

	var enabled, disabled []string
	for _, m := range allModules {
		if m.enabled {
			entry := m.name
			if m.detail != "" {
				entry += " (" + m.detail + ")"
			}
			enabled = append(enabled, entry)
		} else {
			disabled = append(disabled, m.name)
		}
	}

	fmt.Printf("  Enabled:  %s\n", strings.Join(enabled, ", "))
	if len(disabled) > 0 {
		fmt.Printf("  Disabled: %s\n", strings.Join(disabled, ", "))
	}

	// Packages
	pkgs := cfg.AllPackages()
	if len(pkgs) > 0 {
		fmt.Printf("\nPackages (%d): %s\n", len(pkgs), strings.Join(pkgs, ", "))
	}

	return nil
}

func fmtIf(cond bool, label string) string {
	if cond {
		return label
	}
	return ""
}
