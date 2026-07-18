package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration struct, mapped from profile YAML + user state.
type Config struct {
	Extends       string        `yaml:"extends,omitempty"`
	Modules       ModulesConfig `yaml:"modules"`
	Packages      []string      `yaml:"packages"`
	PackagesExtra []string      `yaml:"packages_extra"`
	Casks         []string      `yaml:"casks,omitempty"`
	CasksExtra    []string      `yaml:"casks_extra,omitempty"`
	// Populated from user state, not profile YAML
	Name       string `yaml:"-"`
	Email      string `yaml:"-"`
	GithubUser string `yaml:"-"`
	Timezone   string `yaml:"-"`
	// Runtime info
	System *SystemInfo `yaml:"-"`
}

// ModulesConfig holds per-module configuration.
type ModulesConfig struct {
	Packages  ModuleToggle  `yaml:"packages"`
	Shell     ShellConfig   `yaml:"shell"`
	Node      ModuleToggle  `yaml:"node"`
	Git       GitConfig     `yaml:"git"`
	SSH       SSHModConfig  `yaml:"ssh"`
	Terminal  TermConfig    `yaml:"terminal"`
	Tmux      ModuleToggle  `yaml:"tmux"`
	Workspace WorkConfig    `yaml:"workspace"`
	AI        AIConfig      `yaml:"ai"`
	Fonts     FontsConfig   `yaml:"fonts"`
	Conda     ModuleToggle  `yaml:"conda"`
	GPG       ModuleToggle  `yaml:"gpg"`
	Secrets   ModuleToggle  `yaml:"secrets"`
	MacApps   MacAppsConfig `yaml:"macapps"`
}

// UnmarshalYAML accepts the legacy modules.ai_tools key as read-only input and
// normalizes it into modules.ai.
func (m *ModulesConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw ModulesConfig
	aux := struct {
		*raw     `yaml:",inline"`
		LegacyAI AIConfig `yaml:"ai_tools"`
	}{
		raw: (*raw)(m),
	}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	if !m.AI.Enabled && aux.LegacyAI.Enabled {
		m.AI = aux.LegacyAI
	}
	return nil
}

// MacAppsConfig configures the macapps module.
type MacAppsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BackupRoot string `yaml:"backup_root,omitempty"` // shared root for app-settings/ + profiles/ snapshots
}

// ModuleToggle is a simple enabled/disabled toggle.
type ModuleToggle struct {
	Enabled bool `yaml:"enabled"`
}

// AIConfig configures AI helper files plus optional agents and skills SSOT deployment.
type AIConfig struct {
	Enabled    bool           `yaml:"enabled"`
	AgentsSSOT bool           `yaml:"agents_ssot,omitempty"`
	HUD        bool           `yaml:"hud,omitempty"`
	Skills     AISkillsConfig `yaml:"skills,omitempty"`
}

// AISkillsConfig provides defaults for the read-only `dot ai skills`
// diagnostics (status/path). Runtime skill symlinks are managed by the Maru
// app; dot never writes them.
type AISkillsConfig struct {
	Enabled  bool     `yaml:"enabled,omitempty"`   // deprecated: ignored, kept so legacy configs still load
	Provider string   `yaml:"provider,omitempty"`  // maru or path (anchor is a legacy alias for maru)
	SSOTPath string   `yaml:"ssot_path,omitempty"` // optional for provider=maru
	Tools    []string `yaml:"tools,omitempty"`     // default tool targets for diagnostics
}

// IsZero lets yaml.v3 omit an unset skills block from user state/config output.
func (c AISkillsConfig) IsZero() bool {
	return !c.Enabled && c.Provider == "" && c.SSOTPath == "" && len(c.Tools) == 0
}

// ShellConfig configures the shell module.
type ShellConfig struct {
	Enabled bool `yaml:"enabled"`
}

// GitConfig configures the git module.
type GitConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Signing       bool   `yaml:"signing"`
	CoauthorGuard string `yaml:"coauthor_guard,omitempty"`
}

// SSHModConfig configures the ssh module.
type SSHModConfig struct {
	Enabled bool   `yaml:"enabled"`
	KeyName string `yaml:"key_name,omitempty"`
}

// TermConfig configures the terminal module.
type TermConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Warp        bool     `yaml:"warp"`
	PromptStyle string   `yaml:"prompt_style,omitempty"` // "minimal" or "rich"
	Apps        []string `yaml:"apps,omitempty"`         // GUI terminal casks: warp, wave, cmux, iterm2
}

// WorkConfig configures the workspace module.
type WorkConfig struct {
	Enabled       bool         `yaml:"enabled"`
	Path          string       `yaml:"path,omitempty"`           // workspace root (e.g. ~/workspace)
	Gdrive        string       `yaml:"gdrive,omitempty"`         // cloud storage root (Google Drive or Dropbox); key kept for compat
	GdriveSymlink string       `yaml:"gdrive_symlink,omitempty"` // symlink name for the cloud root (e.g. ~/gdrive-workspace, ~/Dropbox)
	Symlink       string       `yaml:"symlink,omitempty"`        // explicit symlink target for Path (if set, Path → Symlink)
	Repos         []RepoConfig `yaml:"repos,omitempty"`          // git repos to clone into workspace
}

// FontsConfig configures the fonts module.
type FontsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Family  string `yaml:"family,omitempty"`
}

// SecretsUserConfig holds user-specific secrets configuration.
type SecretsUserConfig struct {
	AgeIdentity   string        `yaml:"age_identity,omitempty"`
	AgeRecipients []string      `yaml:"age_recipients,omitempty"`
	LastBackup    *BackupRecord `yaml:"last_backup,omitempty"`
}

// BackupRecord records the most recent successful secrets backup.
type BackupRecord struct {
	Path  string    `yaml:"path"`
	Time  time.Time `yaml:"time"`
	Files int       `yaml:"files"`
}

// IsModuleEnabled returns whether a given module name is enabled in this config.
func (c *Config) IsModuleEnabled(name string) bool {
	switch name {
	case "packages":
		return c.Modules.Packages.Enabled
	case "shell":
		return c.Modules.Shell.Enabled
	case "node":
		return c.Modules.Node.Enabled
	case "git":
		return c.Modules.Git.Enabled
	case "ssh":
		return c.Modules.SSH.Enabled
	case "terminal":
		return c.Modules.Terminal.Enabled
	case "tmux":
		return c.Modules.Tmux.Enabled
	case "workspace":
		return c.Modules.Workspace.Enabled
	case "ai":
		return c.Modules.AI.Enabled
	case "fonts":
		return c.Modules.Fonts.Enabled
	case "conda":
		return c.Modules.Conda.Enabled
	case "gpg":
		return c.Modules.GPG.Enabled
	case "secrets":
		return c.Modules.Secrets.Enabled
	case "macapps":
		return c.Modules.MacApps.Enabled
	default:
		return false
	}
}

// AllPackages returns the merged package list (base + extra).
func (c *Config) AllPackages() []string {
	seen := make(map[string]bool, len(c.Packages)+len(c.PackagesExtra))
	var result []string
	for _, p := range c.Packages {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	for _, p := range c.PackagesExtra {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// AllCasks returns the merged cask list (base + extra), de-duplicated.
func (c *Config) AllCasks() []string {
	seen := make(map[string]bool, len(c.Casks)+len(c.Modules.Terminal.Apps)+len(c.CasksExtra))
	var result []string
	for _, p := range c.Casks {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	if c.Modules.Terminal.Enabled {
		for _, p := range c.Modules.Terminal.Apps {
			if !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
	}
	for _, p := range c.CasksExtra {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

// TemplateData returns a map suitable for Go template rendering.
func (c *Config) TemplateData() map[string]any {
	home, _ := os.UserHomeDir()

	isDarwin := false
	hostname := ""
	if c.System != nil {
		isDarwin = c.System.OS == "darwin"
		hostname = c.System.Hostname
	}

	hasCUDA := false
	cudaHome := ""
	hasNVIDIAGPU := false
	if c.System != nil {
		hasCUDA = c.System.HasCUDA
		cudaHome = c.System.CUDAHome
		hasNVIDIAGPU = c.System.HasNVIDIAGPU
	}

	return map[string]any{
		"Home":            home,
		"Name":            c.Name,
		"Email":           c.Email,
		"GithubUser":      c.GithubUser,
		"Timezone":        c.Timezone,
		"Hostname":        hostname,
		"IsDarwin":        isDarwin,
		"EnableWorkspace": c.Modules.Workspace.Enabled,
		"EnableAI":        c.Modules.AI.Enabled,
		"WorkspacePath":   c.Modules.Workspace.Path,
		"CloudSymlink":    c.Modules.Workspace.GdriveSymlink,
		"SSHKeyName":      c.Modules.SSH.KeyName,
		"CoauthorGuard":   c.Modules.Git.CoauthorGuard,
		"HasCUDA":         hasCUDA,
		"CUDAHome":        cudaHome,
		"HasNVIDIAGPU":    hasNVIDIAGPU,
	}
}
