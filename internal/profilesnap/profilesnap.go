// Package profilesnap snapshots user-level dotfiles profile artefacts
// (`~/.config/dotfiles/` state, optional secret identities, extracted cask
// install/backup lists) into a host-scoped, version-directory layout under a
// shared backup root.
//
// Layout:
//
//	<Root>/profiles/<Hostname>/<Version>/
//	    meta.yaml            — snapshot metadata
//	    config.yaml          — copy of ~/.config/dotfiles/config.yaml
//	    apps/install.yaml    — extracted install list (Casks + CasksExtra)
//	    apps/backup.yaml     — extracted backup scope (BackupApps)
//	    secrets/             — optional copy of ~/.ssh/age_key* (omitted unless IncludeSecrets)
//	<Root>/profiles/<Hostname>/latest.txt  — contains the current version id
package profilesnap

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Meta records the human-readable provenance of a snapshot.
type Meta struct {
	Version        string    `yaml:"version"`
	Tag            string    `yaml:"tag,omitempty"`
	Hostname       string    `yaml:"hostname"`
	CreatedAt      time.Time `yaml:"created_at"`
	IncludeSecrets bool      `yaml:"include_secrets"`
	StateFile      string    `yaml:"state_file,omitempty"`
	User           string    `yaml:"user,omitempty"`
}

// Snapshot is one concrete version in the archive.
type Snapshot struct {
	Version    string
	Tag        string
	CreatedAt  time.Time
	Path       string // absolute path to the version directory
	IsLatest   bool
	WithSecret bool
}

// Engine executes backup/restore/list operations scoped to a host.
type Engine struct {
	Runner     *exec.Runner
	HomeDir    string
	Root       string // shared backup root (same as appsettings.Engine.Root)
	Hostname   string
	User       string
	StatePath  string // typically config.StatePath()
	SecretsDir string // typically ~/.ssh
}

// HostRoot returns <root>/profiles/<host>.
func (e *Engine) HostRoot() string {
	return filepath.Join(e.Root, "profiles", e.Hostname)
}

// VersionPath returns the absolute path for a given version id.
func (e *Engine) VersionPath(version string) string {
	return filepath.Join(e.HostRoot(), version)
}

// LatestPointerPath returns <host-root>/latest.txt.
func (e *Engine) LatestPointerPath() string {
	return filepath.Join(e.HostRoot(), "latest.txt")
}

// NewVersion produces a filesystem-safe UTC version id.
func NewVersion(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// uniqueVersion returns an unused version id near t; suffix "-N" is appended
// when an earlier snapshot already occupies the natural slot. Protects against
// back-to-back calls within the same 1s window.
func (e *Engine) uniqueVersion(t time.Time) string {
	base := NewVersion(t)
	candidate := base
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(e.VersionPath(candidate)); err != nil && os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

// BackupOptions tune Backup behaviour.
type BackupOptions struct {
	Tag            string
	IncludeSecrets bool
}

// Backup writes a new version directory and advances latest.txt.
// Returns the created Snapshot.
func (e *Engine) Backup(opts BackupOptions) (*Snapshot, error) {
	version := e.uniqueVersion(time.Now())
	dest := e.VersionPath(version)
	if err := e.Runner.MkdirAll(dest, 0o755); err != nil {
		return nil, fmt.Errorf("create version dir: %w", err)
	}

	// 1. state file
	if _, err := os.Stat(e.StatePath); err == nil {
		if err := copyFile(e.Runner, e.StatePath, filepath.Join(dest, "config.yaml")); err != nil {
			return nil, fmt.Errorf("copy state: %w", err)
		}

		// Extract install/backup lists into readable siblings.
		if state, err := config.LoadStateFrom(e.StatePath); err == nil {
			if err := e.writeInstallList(dest, state); err != nil {
				e.Runner.Logger.Warn("write install list", "err", err)
			}
			if err := e.writeBackupList(dest, state); err != nil {
				e.Runner.Logger.Warn("write backup list", "err", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat state: %w", err)
	}

	// 2. secrets (age identities)
	if opts.IncludeSecrets {
		if err := e.copySecrets(filepath.Join(dest, "secrets")); err != nil {
			return nil, fmt.Errorf("copy secrets: %w", err)
		}
	}

	// 3. meta
	meta := Meta{
		Version:        version,
		Tag:            opts.Tag,
		Hostname:       e.Hostname,
		CreatedAt:      time.Now().UTC(),
		IncludeSecrets: opts.IncludeSecrets,
		StateFile:      e.StatePath,
		User:           e.User,
	}
	if err := writeMeta(e.Runner, filepath.Join(dest, "meta.yaml"), meta); err != nil {
		return nil, fmt.Errorf("write meta: %w", err)
	}

	// 4. latest pointer
	if err := e.Runner.WriteFile(e.LatestPointerPath(), []byte(version+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write latest.txt: %w", err)
	}

	return &Snapshot{
		Version:    version,
		Tag:        opts.Tag,
		CreatedAt:  meta.CreatedAt,
		Path:       dest,
		IsLatest:   true,
		WithSecret: opts.IncludeSecrets,
	}, nil
}

// RestoreOptions selects which snapshot and which artefacts are restored.
type RestoreOptions struct {
	Version        string // "" means use latest.txt
	IncludeSecrets bool   // copy back secrets/* when present
	IncludeState   bool   // copy back config.yaml (defaults to true via caller)
}

// Restore copies state + optional secrets from a snapshot back to $HOME.
// Returns the Snapshot that was applied.
func (e *Engine) Restore(opts RestoreOptions) (*Snapshot, error) {
	version := opts.Version
	if version == "" {
		v, err := e.readLatest()
		if err != nil {
			return nil, err
		}
		version = v
	}
	src := e.VersionPath(version)
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("snapshot %s not found at %s", version, src)
	}

	meta, _ := readMeta(filepath.Join(src, "meta.yaml"))

	if opts.IncludeState {
		srcCfg := filepath.Join(src, "config.yaml")
		if _, err := os.Stat(srcCfg); err == nil {
			if err := e.Runner.MkdirAll(filepath.Dir(e.StatePath), 0o755); err != nil {
				return nil, err
			}
			if err := copyFile(e.Runner, srcCfg, e.StatePath); err != nil {
				return nil, fmt.Errorf("restore state: %w", err)
			}
		}
	}

	if opts.IncludeSecrets {
		secSrc := filepath.Join(src, "secrets")
		if _, err := os.Stat(secSrc); err == nil {
			if err := copyDir(e.Runner, secSrc, e.SecretsDir); err != nil {
				return nil, fmt.Errorf("restore secrets: %w", err)
			}
		}
	}

	latest, _ := e.readLatest()
	snap := &Snapshot{
		Version:    version,
		Path:       src,
		IsLatest:   version == latest,
		WithSecret: meta != nil && meta.IncludeSecrets,
	}
	if meta != nil {
		snap.Tag = meta.Tag
		snap.CreatedAt = meta.CreatedAt
	}
	return snap, nil
}

// List enumerates snapshots for the host, newest-first.
func (e *Engine) List() ([]Snapshot, error) {
	entries, err := os.ReadDir(e.HostRoot())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	latest, _ := e.readLatest()
	var out []Snapshot
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		v := en.Name()
		meta, _ := readMeta(filepath.Join(e.HostRoot(), v, "meta.yaml"))
		snap := Snapshot{Version: v, Path: filepath.Join(e.HostRoot(), v), IsLatest: v == latest}
		if meta != nil {
			snap.Tag = meta.Tag
			snap.CreatedAt = meta.CreatedAt
			snap.WithSecret = meta.IncludeSecrets
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// Prune removes older snapshots, keeping the newest `keep` (including latest).
func (e *Engine) Prune(keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	all, err := e.List()
	if err != nil {
		return nil, err
	}
	if len(all) <= keep {
		return nil, nil
	}
	latest, _ := e.readLatest()
	removed := make([]string, 0, len(all)-keep)
	kept := 0
	for _, s := range all {
		if kept < keep || s.Version == latest {
			kept++
			continue
		}
		if err := e.Runner.RemoveAll(s.Path); err != nil {
			return removed, err
		}
		removed = append(removed, s.Version)
	}
	return removed, nil
}

// ResolveLatest returns the current "latest" version id, or empty string
// when no pointer exists.
func (e *Engine) ResolveLatest() (string, error) {
	return e.readLatest()
}

// --- internals ---

func (e *Engine) readLatest() (string, error) {
	data, err := os.ReadFile(e.LatestPointerPath())
	if err != nil {
		if os.IsNotExist(err) {
			// Fall back to newest directory.
			all, lerr := e.List()
			if lerr != nil {
				return "", lerr
			}
			if len(all) == 0 {
				return "", fmt.Errorf("no snapshots under %s", e.HostRoot())
			}
			return all[0].Version, nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (e *Engine) copySecrets(destDir string) error {
	if _, err := os.Stat(e.SecretsDir); err != nil {
		return nil // no ~/.ssh yet — nothing to copy
	}
	if err := e.Runner.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(e.SecretsDir)
	if err != nil {
		return err
	}
	for _, en := range entries {
		name := en.Name()
		if !strings.HasPrefix(name, "age_key") {
			continue
		}
		if err := copyFile(e.Runner, filepath.Join(e.SecretsDir, name), filepath.Join(destDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) writeInstallList(dest string, state *config.UserState) error {
	installDir := filepath.Join(dest, "apps")
	if err := e.Runner.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	doc := map[string]any{
		"casks":       state.Modules.MacApps.Casks,
		"casks_extra": state.Modules.MacApps.CasksExtra,
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return e.Runner.WriteFile(filepath.Join(installDir, "install.yaml"), data, 0o644)
}

func (e *Engine) writeBackupList(dest string, state *config.UserState) error {
	installDir := filepath.Join(dest, "apps")
	if err := e.Runner.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	doc := map[string]any{
		"backup_apps": state.Modules.MacApps.BackupApps,
		"backup_root": state.Modules.MacApps.BackupRoot,
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return e.Runner.WriteFile(filepath.Join(installDir, "backup.yaml"), data, 0o644)
}

func writeMeta(runner *exec.Runner, path string, m Meta) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return runner.WriteFile(path, data, 0o644)
}

func readMeta(path string) (*Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func copyFile(runner *exec.Runner, src, dst string) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: copy", "src", src, "dst", dst)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode()&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func copyDir(runner *exec.Runner, src, dst string) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: copy dir", "src", src, "dst", dst)
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, en := range entries {
		sp := filepath.Join(src, en.Name())
		dp := filepath.Join(dst, en.Name())
		info, err := en.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(sp)
			if err != nil {
				continue
			}
			_ = os.Remove(dp)
			if err := os.Symlink(target, dp); err != nil {
				return err
			}
			continue
		}
		if info.IsDir() {
			if err := copyDir(runner, sp, dp); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(runner, sp, dp); err != nil {
			return err
		}
	}
	return nil
}
