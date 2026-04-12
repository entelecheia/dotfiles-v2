package config

import (
	"embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed profiles/*.yaml
var embeddedProfiles embed.FS

const maxExtendsDepth = 5

// Load resolves a profile by name (or custom path), applies env overrides,
// and attaches system info.
func Load(profileName, customPath string, sysInfo *SystemInfo) (*Config, error) {
	var cfg *Config
	var err error

	if customPath != "" {
		cfg, err = loadFromFile(customPath)
	} else {
		if profileName == "" {
			profileName = "full"
		}
		cfg, err = resolveProfile(profileName, 0)
	}
	if err != nil {
		return nil, err
	}

	cfg.System = sysInfo
	return cfg, nil
}

// AvailableProfiles returns the list of built-in profile names.
func AvailableProfiles() []string {
	return []string{"minimal", "full", "server"}
}

func resolveProfile(name string, depth int) (*Config, error) {
	if depth > maxExtendsDepth {
		return nil, fmt.Errorf("profile extends chain too deep (max %d)", maxExtendsDepth)
	}

	cfg, err := loadEmbeddedProfile(name)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", name, err)
	}

	if cfg.Extends == "" {
		return cfg, nil
	}

	base, err := resolveProfile(cfg.Extends, depth+1)
	if err != nil {
		return nil, err
	}

	return mergeConfigs(base, cfg), nil
}

func loadEmbeddedProfile(name string) (*Config, error) {
	data, err := embeddedProfiles.ReadFile("profiles/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("profile %q not found: %w", name, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing profile %q: %w", name, err)
	}
	return &cfg, nil
}

func loadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	return &cfg, nil
}

// mergeConfigs overlays child onto base. Non-zero child values win.
func mergeConfigs(base, overlay *Config) *Config {
	merged := *base

	// Packages: keep base, append extra from overlay
	merged.Packages = base.Packages
	if len(overlay.Packages) > 0 {
		merged.Packages = overlay.Packages
	}
	merged.PackagesExtra = append(base.PackagesExtra, overlay.PackagesExtra...)

	// Modules: overlay wins per-module if explicitly set
	merged.Modules = mergeModules(base.Modules, overlay.Modules)

	// Clear extends (already resolved)
	merged.Extends = ""

	return &merged
}

func mergeModules(base, overlay ModulesConfig) ModulesConfig {
	m := base
	if overlay.Packages.Enabled {
		m.Packages = overlay.Packages
	}
	if overlay.Shell.Enabled {
		m.Shell = overlay.Shell
	}
	if overlay.Node.Enabled {
		m.Node = overlay.Node
	}
	if overlay.Git.Enabled {
		m.Git = overlay.Git
	}
	if overlay.SSH.Enabled {
		m.SSH = overlay.SSH
	}
	if overlay.Terminal.Enabled {
		m.Terminal = overlay.Terminal
	}
	if overlay.Tmux.Enabled {
		m.Tmux = overlay.Tmux
	}
	if overlay.Workspace.Enabled {
		m.Workspace = overlay.Workspace
	}
	if overlay.AITools.Enabled {
		m.AITools = overlay.AITools
	}
	if overlay.Fonts.Enabled {
		m.Fonts = overlay.Fonts
	}
	if overlay.Conda.Enabled {
		m.Conda = overlay.Conda
	}
	if overlay.GPG.Enabled {
		m.GPG = overlay.GPG
	}
	if overlay.Secrets.Enabled {
		m.Secrets = overlay.Secrets
	}
	return m
}

// ApplyEnvOverrides applies environment variable overrides to a config.
func ApplyEnvOverrides(cfg *Config) {
	if v := os.Getenv("DOTFILES_NAME"); v != "" {
		cfg.Name = v
	}
	if v := os.Getenv("DOTFILES_EMAIL"); v != "" {
		cfg.Email = v
	}
	if v := os.Getenv("DOTFILES_PROFILE"); v != "" {
		// Profile is handled at CLI level, not here
	}
	if v := os.Getenv("DOTFILES_WORKSPACE_PATH"); v != "" {
		cfg.Modules.Workspace.Path = v
	}
}
