package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UserState holds user-configured settings persisted to disk.
type UserState struct {
	Name       string            `yaml:"name"`
	Email      string            `yaml:"email"`
	GithubUser string            `yaml:"github_user"`
	Timezone   string            `yaml:"timezone"`
	Profile    string            `yaml:"profile"`
	Modules    UserModulesState  `yaml:"modules,omitempty"`
	SSH        UserSSHState      `yaml:"ssh,omitempty"`
	Secrets    SecretsUserConfig `yaml:"secrets,omitempty"`
}

// UserModulesState holds module opt-in/config from user state.
type UserModulesState struct {
	Workspace UserWorkspaceState `yaml:"workspace,omitempty"`
	AITools   bool               `yaml:"ai_tools,omitempty"`
	Warp      bool               `yaml:"warp,omitempty"`
	Fonts     UserFontsState     `yaml:"fonts,omitempty"`
}

// UserWorkspaceState holds workspace config from user state.
type UserWorkspaceState struct {
	Path   string `yaml:"path,omitempty"`
	Gdrive string `yaml:"gdrive,omitempty"`
}

// UserFontsState holds font config from user state.
type UserFontsState struct {
	Family string `yaml:"family,omitempty"`
}

// UserSSHState holds SSH config from user state.
type UserSSHState struct {
	KeyName string `yaml:"key_name,omitempty"`
}

// StateDir returns the path to the dotfiles config directory.
func StateDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dotfiles")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dotfiles")
}

// StatePath returns the path to the user state file.
func StatePath() string {
	return filepath.Join(StateDir(), "config.yaml")
}

// LoadState reads user state from disk.
func LoadState() (*UserState, error) {
	path := StatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserState{}, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state UserState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return &state, nil
}

// SaveState writes user state to disk.
func SaveState(state *UserState) error {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	path := StatePath()
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	return nil
}

// StateDirForHome returns the state directory for a specific home directory.
func StateDirForHome(homeDir string) string {
	return filepath.Join(homeDir, ".config", "dotfiles")
}

// StatePathForHome returns the state file path for a specific home directory.
func StatePathForHome(homeDir string) string {
	return filepath.Join(StateDirForHome(homeDir), "config.yaml")
}

// LoadStateForHome reads user state from a specific home directory.
func LoadStateForHome(homeDir string) (*UserState, error) {
	path := StatePathForHome(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UserState{}, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state UserState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return &state, nil
}

// SaveStateForHome writes user state to a specific home directory.
func SaveStateForHome(homeDir string, state *UserState) error {
	dir := StateDirForHome(homeDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	path := StatePathForHome(homeDir)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	return nil
}

// ApplyStateToConfig merges user state into a Config loaded from a profile.
func ApplyStateToConfig(cfg *Config, state *UserState) {
	cfg.Name = state.Name
	cfg.Email = state.Email
	cfg.GithubUser = state.GithubUser
	cfg.Timezone = state.Timezone

	// Module opt-ins from state
	if state.Modules.Workspace.Path != "" {
		cfg.Modules.Workspace.Enabled = true
		cfg.Modules.Workspace.Path = state.Modules.Workspace.Path
		cfg.Modules.Workspace.Gdrive = state.Modules.Workspace.Gdrive
	}
	if state.Modules.AITools {
		cfg.Modules.AITools.Enabled = true
	}
	if state.Modules.Warp {
		cfg.Modules.Terminal.Warp = true
	}
	if state.Modules.Fonts.Family != "" {
		cfg.Modules.Fonts.Family = state.Modules.Fonts.Family
	}
	if state.SSH.KeyName != "" {
		cfg.Modules.SSH.KeyName = state.SSH.KeyName
	}
}
