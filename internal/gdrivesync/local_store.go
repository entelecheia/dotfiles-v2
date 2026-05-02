package gdrivesync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// localStoreDir is the per-workspace root for gdrive-sync runtime state
// and config. Lives directly under the local sync tree (e.g.
// ~/workspace/work/.dotfiles/gdrive-sync/) so settings travel with the
// workspace instead of in the user's home config dir.
const (
	localStoreDirRel = ".dotfiles/gdrive-sync"

	localConfigName     = "config.yaml"
	localStateName      = "state.yaml"
	localExcludeName    = "exclude.txt"
	localIgnoreName     = "ignore.txt"
	localSharedDynName  = "shared-excludes.dyn.conf"
	localBaselineName   = "baseline.manifest"
	localImportsName    = "imports.manifest"
	localTombstonesName = "tombstones.log"
	localLogDirRel      = "log"
	localLogFileName    = "gdrive-sync.log"

	gitignoreBlockHeader = "# dotfiles gdrive-sync: track shared baseline, ignore machine-local state"
)

var gitignoreEntries = []string{
	gitignoreBlockHeader,
	"!/.dotfiles/",
	"/.dotfiles/*",
	"!/.dotfiles/gdrive-sync/",
	"/.dotfiles/gdrive-sync/*",
	"!/.dotfiles/gdrive-sync/baseline.manifest",
}

// PropagationPolicy controls which kinds of workspace changes Push
// propagates to the mirror. Default is `{true, true, false}` so the
// safe-by-default behavior is "creates and updates flow, deletes don't".
//
// `Push` translates this into rsync flags (see plan §B). An all-false
// policy is rejected at validation time — there's no rsync invocation
// that does nothing meaningful.
type PropagationPolicy struct {
	Create bool `yaml:"create"`
	Update bool `yaml:"update"`
	Delete bool `yaml:"delete"`
}

// DefaultPropagationPolicy is the safe baseline applied when nothing
// else says otherwise (fresh init, missing config field, etc.).
func DefaultPropagationPolicy() PropagationPolicy {
	return PropagationPolicy{Create: true, Update: true, Delete: false}
}

// Validate rejects no-op combinations.
func (p PropagationPolicy) Validate() error {
	if !p.Create && !p.Update && !p.Delete {
		return fmt.Errorf("propagation must enable at least one of {create, update, delete}")
	}
	return nil
}

// String renders a compact human label like "create+update (delete off)".
func (p PropagationPolicy) String() string {
	on := []string{}
	if p.Create {
		on = append(on, "create")
	}
	if p.Update {
		on = append(on, "update")
	}
	if p.Delete {
		on = append(on, "delete")
	}
	if len(on) == 0 {
		return "(none — invalid)"
	}
	off := []string{}
	if !p.Create {
		off = append(off, "create")
	}
	if !p.Update {
		off = append(off, "update")
	}
	if !p.Delete {
		off = append(off, "delete")
	}
	if len(off) == 0 {
		return strings.Join(on, "+")
	}
	return strings.Join(on, "+") + " (" + strings.Join(off, ",") + " off)"
}

// LocalConfig is the workspace-local source of truth for gdrive-sync
// settings. Persists to <localStoreDir>/config.yaml.
type LocalConfig struct {
	MirrorPath     string            `yaml:"mirror_path,omitempty"`
	Propagation    PropagationPolicy `yaml:"propagation"`
	MaxDelete      int               `yaml:"max_delete,omitempty"`
	Interval       int               `yaml:"interval,omitempty"`      // push scheduler cadence (seconds)
	PullInterval   int               `yaml:"pull_interval,omitempty"` // pull+intake scheduler cadence (0 = off)
	Paused         bool              `yaml:"paused,omitempty"`
	SharedExcludes []string          `yaml:"shared_excludes,omitempty"`
}

// LocalState holds non-config runtime telemetry — the sticky timestamps
// that change every pull/push/intake. Splitting them from LocalConfig keeps
// the editable file (config.yaml) churn-free.
type LocalState struct {
	LastPull        time.Time `yaml:"last_pull,omitempty"`
	LastPush        time.Time `yaml:"last_push,omitempty"`
	LastIntake      time.Time `yaml:"last_intake,omitempty"`
	LastIntakeTSDir string    `yaml:"last_intake_ts_dir,omitempty"`
}

// LocalPaths resolves every well-known path under
// <localPath>/.dotfiles/gdrive-sync/. Constructed via ResolveLocalPaths
// so callers don't string-glue these by hand.
type LocalPaths struct {
	StoreDir        string
	ConfigFile      string
	StateFile       string
	ExcludeFile     string
	IgnoreFile      string
	SharedDynFile   string
	BaselineFile    string
	ImportsFile     string
	TombstonesFile  string
	LogDir          string
	LogFile         string
	WorkspaceRoot   string // the local sync tree itself (parent of .dotfiles)
	WorkspaceIgnore string // <local>/.gitignore (workspace-level)
}

// ResolveLocalPaths returns the canonical layout for a given local
// sync tree. localPath should be the absolute path to the workspace
// root (with or without trailing slash).
func ResolveLocalPaths(localPath string) *LocalPaths {
	root := strings.TrimRight(localPath, "/")
	store := filepath.Join(root, localStoreDirRel)
	logDir := filepath.Join(store, localLogDirRel)
	return &LocalPaths{
		StoreDir:        store,
		ConfigFile:      filepath.Join(store, localConfigName),
		StateFile:       filepath.Join(store, localStateName),
		ExcludeFile:     filepath.Join(store, localExcludeName),
		IgnoreFile:      filepath.Join(store, localIgnoreName),
		SharedDynFile:   filepath.Join(store, localSharedDynName),
		BaselineFile:    filepath.Join(store, localBaselineName),
		ImportsFile:     filepath.Join(store, localImportsName),
		TombstonesFile:  filepath.Join(store, localTombstonesName),
		LogDir:          logDir,
		LogFile:         filepath.Join(logDir, localLogFileName),
		WorkspaceRoot:   root,
		WorkspaceIgnore: filepath.Join(root, ".gitignore"),
	}
}

// EnsureLocalLayout creates the .dotfiles/gdrive-sync/ directory plus
// all default files (empty manifests, header-only ignore.txt, embedded
// excludes copy). Idempotent — existing files are left untouched.
//
// Does NOT load or write config.yaml — that's the migration path's job.
func EnsureLocalLayout(paths *LocalPaths) error {
	for _, dir := range []string{paths.StoreDir, paths.LogDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := materializeExcludeIfMissing(paths.ExcludeFile); err != nil {
		return err
	}
	for _, ent := range []struct {
		path   string
		header string
	}{
		{paths.IgnoreFile, "# User-supplied ignore patterns for `dot gdrive-sync`.\n# One pattern per line; same syntax as exclude.txt. Layered after exclude.txt.\n"},
		{paths.BaselineFile, "# Auto-generated by `dot gdrive-sync push` — do not edit.\n# relpath\\tsize\\tmtime-rfc3339\\tsha256-or-dash\n"},
		{paths.ImportsFile, "# Auto-generated by `dot gdrive-sync intake` — clear via `dot gdrive-sync inbox clear`.\n# relpath\\tsize\\tmtime-rfc3339\\tsha256-or-dash\\timported-rfc3339\n"},
		{paths.TombstonesFile, "# Auto-generated by `dot gdrive-sync intake` — mirror deletions detected.\n# relpath\\tbaseline-fingerprint\\tdetected-rfc3339\n"},
	} {
		if err := writeIfMissing(ent.path, []byte(ent.header)); err != nil {
			return err
		}
	}
	return appendGitignoreBlock(paths.WorkspaceIgnore, gitignoreEntries)
}

// materializeExcludeIfMissing copies the embedded excludes baseline to
// disk so the operator can edit it. Existing on-disk content wins.
func materializeExcludeIfMissing(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	engine := template.NewEngine()
	body, err := engine.ReadStatic(excludesTemplatePath)
	if err != nil {
		return fmt.Errorf("reading embedded excludes: %w", err)
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// writeIfMissing writes data to path only if path doesn't already exist.
func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// appendGitignoreBlock appends missing `lines` to the workspace .gitignore.
// Creates the file if missing. Idempotent — repeated calls are no-ops once
// every line is present. The order matters because the block can re-include
// baseline.manifest after an older broad "/.dotfiles/" ignore.
func appendGitignoreBlock(gitignorePath string, lines []string) error {
	body, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", gitignorePath, err)
	}

	existing := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		existing[strings.TrimSpace(line)] = true
	}

	out := append([]byte{}, body...)
	if len(out) > 0 && !strings.HasSuffix(string(out), "\n") {
		out = append(out, '\n')
	}
	wrote := false
	for _, line := range lines {
		want := strings.TrimSpace(line)
		if want == "" || existing[want] {
			continue
		}
		out = append(out, []byte(want+"\n")...)
		wrote = true
	}
	if !wrote {
		return nil
	}
	if err := os.WriteFile(gitignorePath, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", gitignorePath, err)
	}
	return nil
}

// appendGitignoreLine appends `line` to the workspace .gitignore if and
// only if it isn't already present. Creates the file if missing.
// Idempotent — repeated calls are no-ops once the line is in.
func appendGitignoreLine(gitignorePath, line string) error {
	want := strings.TrimSpace(line)
	if want == "" {
		return nil
	}
	body, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", gitignorePath, err)
	}
	for _, existing := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(existing) == want {
			return nil
		}
	}
	prefix := ""
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		prefix = "\n"
	}
	out := append([]byte{}, body...)
	out = append(out, []byte(prefix+want+"\n")...)
	if err := os.WriteFile(gitignorePath, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", gitignorePath, err)
	}
	return nil
}

// LoadLocalConfig reads <localStoreDir>/config.yaml. Returns
// (config, true, nil) when the file exists and parses, (nil, false, nil)
// when missing (caller should migrate or use defaults), or an error on
// parse failure.
func LoadLocalConfig(paths *LocalPaths) (*LocalConfig, bool, error) {
	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading %s: %w", paths.ConfigFile, err)
	}
	var cfg LocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", paths.ConfigFile, err)
	}
	if err := cfg.Propagation.Validate(); err != nil {
		// Fall back to defaults when on-disk policy is unusable rather than blocking.
		fmt.Fprintf(os.Stderr, "warning: %s has invalid propagation (%v); using defaults\n", paths.ConfigFile, err)
		cfg.Propagation = DefaultPropagationPolicy()
	}
	return &cfg, true, nil
}

// SaveLocalConfig writes config atomically to <localStoreDir>/config.yaml.
func SaveLocalConfig(paths *LocalPaths, cfg *LocalConfig) error {
	if err := cfg.Propagation.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid propagation policy: %w", err)
	}
	if err := os.MkdirAll(paths.StoreDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", paths.StoreDir, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling local config: %w", err)
	}
	return atomicWrite(paths.ConfigFile, data)
}

// LoadLocalState reads state.yaml or returns a zero-value if missing.
func LoadLocalState(paths *LocalPaths) (*LocalState, error) {
	data, err := os.ReadFile(paths.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &LocalState{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", paths.StateFile, err)
	}
	var st LocalState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", paths.StateFile, err)
	}
	return &st, nil
}

// SaveLocalState writes state.yaml atomically.
func SaveLocalState(paths *LocalPaths, st *LocalState) error {
	if err := os.MkdirAll(paths.StoreDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", paths.StoreDir, err)
	}
	data, err := yaml.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshaling local state: %w", err)
	}
	return atomicWrite(paths.StateFile, data)
}

// UpdateLocalState loads state.yaml, applies mutate, and saves it back.
// Mutate runs against a non-nil pointer; the caller doesn't need to
// guard for missing files. Used by intake/push to bump timestamps
// without each call site repeating Load → mutate → Save.
func UpdateLocalState(paths *LocalPaths, mutate func(*LocalState)) error {
	st, err := LoadLocalState(paths)
	if err != nil {
		return err
	}
	mutate(st)
	return SaveLocalState(paths, st)
}

func localConfigFromGlobal(globalState *config.UserState) *LocalConfig {
	gs := globalState.Modules.GdriveSync
	return &LocalConfig{
		MirrorPath:     gs.MirrorPath,
		Propagation:    DefaultPropagationPolicy(),
		MaxDelete:      gs.MaxDelete,
		Interval:       gs.Interval,
		PullInterval:   0,
		Paused:         gs.Paused,
		SharedExcludes: append([]string(nil), gs.SharedExcludes...),
	}
}

// MigrateFromGlobal seeds <localStoreDir>/config.yaml from the legacy
// `state.modules.gdrive_sync` block when no local config exists yet.
// Returns the freshly-built LocalConfig (also persisted to disk).
//
// Subsequent calls find the local config and skip migration entirely —
// the global block is read at most once per workspace lifetime.
func MigrateFromGlobal(globalState *config.UserState, paths *LocalPaths) (*LocalConfig, error) {
	gs := globalState.Modules.GdriveSync
	cfg := localConfigFromGlobal(globalState)
	if err := EnsureLocalLayout(paths); err != nil {
		return nil, err
	}
	if err := SaveLocalConfig(paths, cfg); err != nil {
		return nil, err
	}
	// Seed state.yaml with the legacy timestamps so `status` keeps continuity.
	st := &LocalState{
		LastPull:   gs.LastPull,
		LastPush:   gs.LastPush,
		LastIntake: gs.LastPull, // legacy `Pull` retired into `intake` semantics
	}
	if err := SaveLocalState(paths, st); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadOrMigrateLocalConfig is the single entry point ResolveConfig uses:
// returns the local config, performing a one-time migration from the
// global state if no local config exists yet.
func LoadOrMigrateLocalConfig(globalState *config.UserState, paths *LocalPaths) (*LocalConfig, error) {
	if cfg, ok, err := LoadLocalConfig(paths); err != nil {
		return nil, err
	} else if ok {
		// Even when config.yaml exists, ensure the surrounding layout
		// (manifests, ignore.txt, gitignore block) is intact — operators
		// can delete these and we should heal silently.
		if err := EnsureLocalLayout(paths); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	return MigrateFromGlobal(globalState, paths)
}

// atomicWrite mirrors saveStateAt's marshal → temp → fsync → rename
// pattern from internal/config/state.go. Same filesystem guarantees,
// no torn writes.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("syncing temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
