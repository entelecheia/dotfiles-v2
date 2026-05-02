package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	Workspace   UserWorkspaceState  `yaml:"workspace,omitempty"`
	AI          UserAIState         `yaml:"ai,omitempty"`
	Warp        bool                `yaml:"warp,omitempty"`
	PromptStyle string              `yaml:"prompt_style,omitempty"` // "minimal" or "rich"
	Fonts       UserFontsState      `yaml:"fonts,omitempty"`
	Sync        UserSyncState       `yaml:"sync,omitempty"`
	Rsync       UserRsyncState      `yaml:"rsync,omitempty"`
	GdriveSync  UserGdriveSyncState `yaml:"gdrive_sync,omitempty"`
	MacApps     UserMacAppsState    `yaml:"macapps,omitempty"`
}

// UserAIState holds user selections for AI CLI/config helpers.
type UserAIState struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// IsZero lets yaml.v3 omit an unset AI block from user state.
func (a UserAIState) IsZero() bool {
	return !a.Enabled
}

// UnmarshalYAML accepts either:
//
//	modules:
//	  ai:
//	    enabled: true
//
// or the shorthand `ai: true`.
func (a *UserAIState) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var enabled bool
		if err := value.Decode(&enabled); err != nil {
			return err
		}
		a.Enabled = enabled
		return nil
	}
	type raw UserAIState
	return value.Decode((*raw)(a))
}

// UnmarshalYAML accepts the legacy modules.ai_tools key as read-only input and
// normalizes it into modules.ai.enabled.
func (s *UserModulesState) UnmarshalYAML(value *yaml.Node) error {
	type raw UserModulesState
	aux := struct {
		*raw     `yaml:",inline"`
		LegacyAI bool `yaml:"ai_tools"`
	}{
		raw: (*raw)(s),
	}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	if !s.AI.Enabled && aux.LegacyAI {
		s.AI.Enabled = true
	}
	return nil
}

// UserMacAppsState holds user selections for the macapps module.
//
// Install vs. backup are separated: Casks/CasksExtra drive `dotfiles apps install`,
// while BackupApps scopes `dotfiles apps backup/restore`. BackupRoot is shared
// with `dotfiles profile backup/restore` so a single folder (typically a Drive
// secrets dir) holds both app-settings snapshots and profile snapshots.
type UserMacAppsState struct {
	Enabled    bool     `yaml:"enabled,omitempty"`
	Casks      []string `yaml:"casks,omitempty"`       // install list (catalog selections)
	CasksExtra []string `yaml:"casks_extra,omitempty"` // install list (free-form additions)
	BackupApps []string `yaml:"backup_apps,omitempty"` // backup/restore scope (empty = manifest ∩ installed)
	BackupRoot string   `yaml:"backup_root,omitempty"` // single backup root; app-settings/ + profiles/ live below it

	LastBackup *BackupRecord `yaml:"last_backup,omitempty"`
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

// UserGdriveSyncState holds local↔local rsync mirror config from user state.
//
// `dot gdrive-sync` keeps ~/workspace/work and ~/gdrive-workspace/work in sync
// via local rsync (no SSH). Workspace is authoritative: pull uses --update only,
// push uses --delete-after. Last* timestamps are advisory (status display).
//
// Paused defaults to true on a fresh state so `migrate` can run a one-shot
// pull and the user can verify before flipping to false via `resume`.
type UserGdriveSyncState struct {
	LocalPath      string    `yaml:"local_path,omitempty"`  // primary tree, default ~/workspace/work
	MirrorPath     string    `yaml:"mirror_path,omitempty"` // mirror tree, default ~/gdrive-workspace/work
	LastPull       time.Time `yaml:"last_pull,omitempty"`
	LastPush       time.Time `yaml:"last_push,omitempty"`
	LastSync       time.Time `yaml:"last_sync,omitempty"`
	ConflictDir    string    `yaml:"conflict_dir,omitempty"`    // default <local>/.sync-conflicts
	Paused         bool      `yaml:"paused,omitempty"`          // gates pull/push/sync; cleared by `resume`
	MaxDelete      int       `yaml:"max_delete,omitempty"`      // safety cap for push --delete-after, default 1000
	Interval       int       `yaml:"interval,omitempty"`        // auto-sync interval in seconds (launchd/systemd), default 300
	SharedExcludes []string  `yaml:"shared_excludes,omitempty"` // operator-curated owned-but-shared-out folders, relative to mirror_path
}

// RepoConfig describes a git repository to clone into the workspace.
type RepoConfig struct {
	Name   string `yaml:"name"`             // subdirectory name: "work" or "vault"
	Remote string `yaml:"remote,omitempty"` // git remote URL (HTTPS or SSH)
}

// UserWorkspaceState holds workspace config from user state.
type UserWorkspaceState struct {
	Path          string       `yaml:"path,omitempty"`
	Gdrive        string       `yaml:"gdrive,omitempty"`
	GdriveSymlink string       `yaml:"gdrive_symlink,omitempty"` // symlink name for Drive (e.g. ~/gdrive-workspace)
	Symlink       string       `yaml:"symlink,omitempty"`        // explicit symlink target for Path
	Repos         []RepoConfig `yaml:"repos,omitempty"`          // git repos to clone into workspace
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
	if s.Modules.GdriveSync.MaxDelete != 0 && (s.Modules.GdriveSync.MaxDelete < 1 || s.Modules.GdriveSync.MaxDelete > 1000000) {
		return fmt.Errorf("gdrive_sync.max_delete must be 0 or 1..1000000 (got %d)", s.Modules.GdriveSync.MaxDelete)
	}
	if s.Modules.GdriveSync.Interval != 0 && (s.Modules.GdriveSync.Interval < 60 || s.Modules.GdriveSync.Interval > 86400) {
		return fmt.Errorf("gdrive_sync.interval must be 0 or 60..86400 seconds (got %d)", s.Modules.GdriveSync.Interval)
	}
	for _, p := range s.Modules.GdriveSync.SharedExcludes {
		// Paths must be relative to mirror_path. Absolute paths and parent
		// escapes would let the manual list reach outside the mirror tree
		// and exclude unrelated content (or be portable across machines
		// in misleading ways).
		if strings.HasPrefix(p, "/") {
			return fmt.Errorf("gdrive_sync.shared_excludes entry %q must be relative to mirror_path (no leading /)", p)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == ".." {
				return fmt.Errorf("gdrive_sync.shared_excludes entry %q may not contain '..' segments", p)
			}
		}
	}
	seen := make(map[string]bool)
	for _, repo := range s.Modules.Workspace.Repos {
		if repo.Name != "work" && repo.Name != "vault" {
			return fmt.Errorf("invalid workspace repo name %q (must be \"work\" or \"vault\")", repo.Name)
		}
		if repo.Remote == "" {
			return fmt.Errorf("workspace repo %q has empty remote URL", repo.Name)
		}
		if seen[repo.Name] {
			return fmt.Errorf("duplicate workspace repo name %q", repo.Name)
		}
		seen[repo.Name] = true
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
		cfg.Modules.Workspace.GdriveSymlink = state.Modules.Workspace.GdriveSymlink
		cfg.Modules.Workspace.Symlink = state.Modules.Workspace.Symlink
		cfg.Modules.Workspace.Repos = state.Modules.Workspace.Repos
	}
	if state.Modules.AI.Enabled {
		cfg.Modules.AI.Enabled = true
	}
	if state.Modules.Warp {
		cfg.Modules.Terminal.Warp = true
	}
	if state.Modules.PromptStyle != "" {
		cfg.Modules.Terminal.PromptStyle = state.Modules.PromptStyle
	}
	if state.Modules.Fonts.Family != "" {
		cfg.Modules.Fonts.Family = state.Modules.Fonts.Family
	}
	if state.SSH.KeyName != "" {
		cfg.Modules.SSH.KeyName = state.SSH.KeyName
	}
	// MacApps: user state toggles the module and overlays cask selections.
	if state.Modules.MacApps.Enabled {
		cfg.Modules.MacApps.Enabled = true
	}
	if state.Modules.MacApps.BackupRoot != "" {
		cfg.Modules.MacApps.BackupRoot = state.Modules.MacApps.BackupRoot
	}
	if len(state.Modules.MacApps.Casks) > 0 {
		cfg.Casks = state.Modules.MacApps.Casks
	}
	if len(state.Modules.MacApps.CasksExtra) > 0 {
		cfg.CasksExtra = append(cfg.CasksExtra, state.Modules.MacApps.CasksExtra...)
	}
}
