package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Sync      UserSyncState      `yaml:"sync,omitempty"`
	Rsync     UserRsyncState     `yaml:"rsync,omitempty"`
}

// UserRsyncState holds rsync sync config from user state.
type UserRsyncState struct {
	RemoteHost string `yaml:"remote_host,omitempty"` // user@host (SSH)
	RemotePath string `yaml:"remote_path,omitempty"` // remote workspace path
	Interval   int    `yaml:"interval,omitempty"`    // sync interval in seconds, default 300
}

// UserSyncState holds rclone bisync config from user state.
type UserSyncState struct {
	Remote   string `yaml:"remote,omitempty"`   // rclone remote name, default "gdrive"
	Path     string `yaml:"path,omitempty"`     // remote path, default "work"
	Interval int    `yaml:"interval,omitempty"` // sync interval in seconds, default 300
}

// UserWorkspaceState holds workspace config from user state.
type UserWorkspaceState struct {
	Path    string `yaml:"path,omitempty"`
	Gdrive  string `yaml:"gdrive,omitempty"`
	Symlink string `yaml:"symlink,omitempty"` // explicit symlink target for Path
}

// UserFontsState holds font config from user state.
type UserFontsState struct {
	Family string `yaml:"family,omitempty"`
}

// UserSSHState holds SSH config from user state.
type UserSSHState struct {
	KeyName string `yaml:"key_name,omitempty"`
}

// validProfiles lists the allowed profile names.
var validProfiles = []string{"minimal", "full", "server"}

// Validate performs lightweight sanity checks on critical fields.
// Returns an error with a clear message for invalid values.
func (s *UserState) Validate() error {
	if s.Profile != "" {
		valid := false
		for _, p := range validProfiles {
			if s.Profile == p {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid profile %q (must be one of: %s)", s.Profile, strings.Join(validProfiles, ", "))
		}
	}
	if s.Email != "" && !strings.Contains(s.Email, "@") {
		return fmt.Errorf("invalid email %q (missing @)", s.Email)
	}
	if s.GithubUser != "" {
		if len(s.GithubUser) > 39 {
			return fmt.Errorf("invalid github_user %q (max 39 characters)", s.GithubUser)
		}
		for _, r := range s.GithubUser {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return fmt.Errorf("invalid github_user %q (alphanumeric + hyphens only)", s.GithubUser)
			}
		}
	}
	if s.Modules.Sync.Interval != 0 && (s.Modules.Sync.Interval < 60 || s.Modules.Sync.Interval > 86400) {
		return fmt.Errorf("sync.interval must be 0 or 60..86400 seconds (got %d)", s.Modules.Sync.Interval)
	}
	if s.Modules.Rsync.Interval != 0 && (s.Modules.Rsync.Interval < 60 || s.Modules.Rsync.Interval > 86400) {
		return fmt.Errorf("rsync.interval must be 0 or 60..86400 seconds (got %d)", s.Modules.Rsync.Interval)
	}
	return nil
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
// Returns an empty state on missing file, an error on parse failure.
// Validation warnings are printed to stderr but do not fail the load
// (so users can recover by running 'dotfiles reconfigure').
func LoadState() (*UserState, error) {
	return loadStateAt(StatePath())
}

func loadStateAt(path string) (*UserState, error) {
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
	if err := state.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: state file has invalid values: %v\n", err)
		fmt.Fprintln(os.Stderr, "  Run 'dotfiles reconfigure' to fix.")
	}
	return &state, nil
}

// SaveState writes user state to disk atomically.
// Validates before writing — invalid state is never persisted.
func SaveState(state *UserState) error {
	return saveStateAt(StatePath(), state)
}

// saveStateAt performs an atomic write: marshal → temp file → fsync → rename.
// On POSIX filesystems, rename is atomic, so partial writes cannot corrupt
// the existing config file.
func saveStateAt(path string, state *UserState) error {
	if err := state.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid state: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	// Write to temp file in the same directory (same filesystem → rename is atomic)
	tmpFile, err := os.CreateTemp(dir, ".config.yaml.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file: %w", err)
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

// LoadStateFrom reads user state from an arbitrary file path.
// Unlike LoadState, it returns an error if the file does not exist.
func LoadStateFrom(path string) (*UserState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var state UserState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := state.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: imported config has invalid values: %v\n", err)
	}
	if state.Name == "" && state.Profile == "" {
		return nil, fmt.Errorf("imported config is empty (no name or profile set)")
	}
	return &state, nil
}

// LoadStateForHome reads user state from a specific home directory.
func LoadStateForHome(homeDir string) (*UserState, error) {
	return loadStateAt(StatePathForHome(homeDir))
}

// SaveStateForHome writes user state to a specific home directory atomically.
func SaveStateForHome(homeDir string, state *UserState) error {
	return saveStateAt(StatePathForHome(homeDir), state)
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
		cfg.Modules.Workspace.Symlink = state.Modules.Workspace.Symlink
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
