package config

import (
	"os"
	"time"
)

// Config is the root configuration struct, mapped from profile YAML + user state.
type Config struct {
	Extends       string        `yaml:"extends,omitempty"`
	Modules       ModulesConfig `yaml:"modules"`
	Packages      []string      `yaml:"packages"`
	PackagesExtra []string      `yaml:"packages_extra"`
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
	Packages ModuleToggle  `yaml:"packages"`
	Shell    ShellConfig   `yaml:"shell"`
	Node     ModuleToggle  `yaml:"node"`
	Git      GitConfig     `yaml:"git"`
	SSH      SSHModConfig  `yaml:"ssh"`
	Terminal TermConfig    `yaml:"terminal"`
	Tmux     ModuleToggle  `yaml:"tmux"`
	Workspace WorkConfig  `yaml:"workspace"`
	AITools  ModuleToggle  `yaml:"ai_tools"`
	Fonts    FontsConfig   `yaml:"fonts"`
	Conda    ModuleToggle  `yaml:"conda"`
	GPG      ModuleToggle  `yaml:"gpg"`
	Secrets  ModuleToggle  `yaml:"secrets"`
}

// ModuleToggle is a simple enabled/disabled toggle.
type ModuleToggle struct {
	Enabled bool `yaml:"enabled"`
}

// ShellConfig configures the shell module.
type ShellConfig struct {
	Enabled bool `yaml:"enabled"`
}

// GitConfig configures the git module.
type GitConfig struct {
	Enabled bool `yaml:"enabled"`
	Signing bool `yaml:"signing"`
}

// SSHModConfig configures the ssh module.
type SSHModConfig struct {
	Enabled bool   `yaml:"enabled"`
	KeyName string `yaml:"key_name,omitempty"`
}

// TermConfig configures the terminal module.
type TermConfig struct {
	Enabled bool `yaml:"enabled"`
	Warp    bool `yaml:"warp"`
}

// WorkConfig configures the workspace module.
type WorkConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Path          string `yaml:"path,omitempty"`            // workspace root (e.g. ~/workspace)
	Gdrive        string `yaml:"gdrive,omitempty"`          // Google Drive physical path
	GdriveSymlink string `yaml:"gdrive_symlink,omitempty"`  // symlink name for Drive (e.g. ~/gdrive-workspace)
	Symlink       string `yaml:"symlink,omitempty"`         // explicit symlink target for Path (if set, Path → Symlink)
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
	case "ai-tools":
		return c.Modules.AITools.Enabled
	case "fonts":
		return c.Modules.Fonts.Enabled
	case "conda":
		return c.Modules.Conda.Enabled
	case "gpg":
		return c.Modules.GPG.Enabled
	case "secrets":
		return c.Modules.Secrets.Enabled
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

// TemplateData returns a map suitable for Go template rendering.
func (c *Config) TemplateData() map[string]any {
	home, _ := os.UserHomeDir()

	isDarwin := false
	isLinux := false
	os := ""
	arch := ""
	hostname := ""
	if c.System != nil {
		isDarwin = c.System.OS == "darwin"
		isLinux = c.System.OS == "linux"
		os = c.System.OS
		arch = c.System.Arch
		hostname = c.System.Hostname
	}

	hasCUDA := false
	cudaHome := ""
	hasNVIDIAGPU := false
	isDGX := false
	gpuModel := ""
	if c.System != nil {
		hasCUDA = c.System.HasCUDA
		cudaHome = c.System.CUDAHome
		hasNVIDIAGPU = c.System.HasNVIDIAGPU
		isDGX = c.System.IsDGX
		gpuModel = c.System.GPUModel
	}

	return map[string]any{
		"Home":            home,
		"Name":            c.Name,
		"Email":           c.Email,
		"GithubUser":      c.GithubUser,
		"Timezone":        c.Timezone,
		"OS":              os,
		"Arch":            arch,
		"Hostname":        hostname,
		"IsDarwin":        isDarwin,
		"IsLinux":         isLinux,
		"Profile":         "", // set by caller
		"EnableWorkspace": c.Modules.Workspace.Enabled,
		"EnableAITools":   c.Modules.AITools.Enabled,
		"EnableWarp":      c.Modules.Terminal.Warp,
		"WorkspacePath":   c.Modules.Workspace.Path,
		"WorkspaceGdrive":  c.Modules.Workspace.Gdrive,
		"GdriveSymlink":    c.Modules.Workspace.GdriveSymlink,
		"SSHKeyName":      c.Modules.SSH.KeyName,
		"GitSigning":      c.Modules.Git.Signing,
		"FontFamily":      c.Modules.Fonts.Family,
		// GPU/CUDA
		"HasCUDA":      hasCUDA,
		"CUDAHome":     cudaHome,
		"HasNVIDIAGPU": hasNVIDIAGPU,
		"IsDGX":        isDGX,
		"GPUModel":     gpuModel,
	}
}
