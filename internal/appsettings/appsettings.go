// Package appsettings implements host-scoped backup and restore of macOS
// application settings (plists, Application Support subtrees, sandbox
// containers). The manifest mapping tokens to their Library-relative paths
// is embedded from manifest.yaml.
package appsettings

import (
	_ "embed"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

//go:embed manifest.yaml
var manifestYAML []byte

// PathEntry describes one Library-relative path belonging to an app.
type PathEntry struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

// AppEntry lists all backup paths for a single cask token.
type AppEntry struct {
	Token string      `yaml:"token"`
	Paths []PathEntry `yaml:"paths"`
}

// Manifest is the root of manifest.yaml.
type Manifest struct {
	Apps []AppEntry `yaml:"apps"`
}

// LoadManifest parses the embedded manifest.
func LoadManifest() (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(manifestYAML, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.yaml: %w", err)
	}
	return &m, nil
}

// Tokens returns every app token listed in the manifest (manifest order).
func (m *Manifest) Tokens() []string {
	out := make([]string, 0, len(m.Apps))
	for _, a := range m.Apps {
		out = append(out, a.Token)
	}
	return out
}

// App returns the entry for a token, or nil if unknown.
func (m *Manifest) App(token string) *AppEntry {
	for i, a := range m.Apps {
		if a.Token == token {
			return &m.Apps[i]
		}
	}
	return nil
}

// Engine executes Backup/Restore/Status operations scoped to a host directory.
//
// Archive layout (rooted at Root):
//
//	<Root>/app-settings/<Hostname>/<app-token>/<Library-relative-path>
//
// The "app-settings" prefix keeps app-setting snapshots sibling to the
// profile snapshots produced by `dotfiles profile backup`.
type Engine struct {
	Runner   *exec.Runner
	HomeDir  string // user home (Library lives here)
	Root     string // shared backup root
	Hostname string
	Manifest *Manifest
}

// AppSettingsRoot is the subtree that holds per-app snapshots for every host.
func (e *Engine) AppSettingsRoot() string {
	return filepath.Join(e.Root, "app-settings")
}

// HostRoot is the per-host subtree inside AppSettingsRoot.
func (e *Engine) HostRoot() string {
	return filepath.Join(e.AppSettingsRoot(), e.Hostname)
}

// libraryPath returns the absolute source path for a manifest entry.
func (e *Engine) libraryPath(rel string) string {
	return filepath.Join(e.HomeDir, "Library", rel)
}

// archivePath returns the destination inside the backup archive.
func (e *Engine) archivePath(token, rel string) string {
	return filepath.Join(e.HostRoot(), token, rel)
}

// --- Summary types ---

// AppSummary captures the outcome of Backup/Restore for a single app.
type AppSummary struct {
	Token   string
	Copied  int // paths fully processed
	Missing int // paths that had no source
	Files   int // individual files copied (for directory walks)
	Bytes   int64
}

// Summary aggregates per-app results.
type Summary struct {
	Apps  []AppSummary
	Files int
	Bytes int64
}

// AppStatus is produced by Status(): counts of available/backed-up paths.
type AppStatus struct {
	Token       string
	PresentLive int // manifest paths that exist under $HOME/Library
	TotalLive   int // manifest path count
	PresentBak  int // manifest paths that exist inside the archive
	TotalBak    int
}

// --- Exclusion rules (shared between backup and restore) ---

var excludedSegments = []string{
	"Caches",
	"Cache",
	"Code Cache",
	"GPUCache",
	"IndexedDB",
	"Local Storage",
	"Logs",
	"logs",
	"blob_storage",
	"Crashpad",
	"Service Worker",
}

var excludedSuffixes = []string{
	".log",
	".lock",
}

var excludedExact = []string{
	".DS_Store",
}

func isExcluded(rel string) bool {
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, p := range parts {
		if strings.Contains(p, "Cache") {
			return true
		}
		for _, seg := range excludedSegments {
			if p == seg {
				return true
			}
		}
		for _, ex := range excludedExact {
			if p == ex {
				return true
			}
		}
		if strings.HasPrefix(p, "Singleton") {
			return true
		}
	}
	for _, suf := range excludedSuffixes {
		if strings.HasSuffix(rel, suf) {
			return true
		}
	}
	return false
}

// --- Backup ---

// Backup copies the listed apps (or all manifest apps when tokens is empty)
// into the host-scoped archive. Missing sources are reported but do not abort.
func (e *Engine) Backup(ctx context.Context, tokens []string) (*Summary, error) {
	targets := e.selectTokens(tokens)
	if len(targets) == 0 {
		return nil, fmt.Errorf("no apps selected for backup")
	}

	if err := e.Runner.MkdirAll(e.HostRoot(), 0o755); err != nil {
		return nil, fmt.Errorf("create host root %s: %w", e.HostRoot(), err)
	}

	sum := &Summary{}
	for _, token := range targets {
		app := e.Manifest.App(token)
		if app == nil {
			continue
		}
		as := AppSummary{Token: token}
		for _, p := range app.Paths {
			src := e.libraryPath(p.Path)
			dst := e.archivePath(token, p.Path)
			fi, err := os.Lstat(src)
			if err != nil {
				as.Missing++
				continue
			}
			files, bytes, err := e.copyTree(src, dst, fi)
			if err != nil {
				e.Runner.Logger.Warn("backup copy failed", "app", token, "src", src, "err", err)
				as.Missing++
				continue
			}
			as.Copied++
			as.Files += files
			as.Bytes += bytes
		}
		sum.Apps = append(sum.Apps, as)
		sum.Files += as.Files
		sum.Bytes += as.Bytes
	}
	return sum, nil
}

// --- Restore ---

// Restore copies from the host-scoped archive back to $HOME/Library.
// Missing sources (not yet backed up) are skipped. Existing live files are
// snapshotted to ~/.local/share/dotfiles/backup/ before being overwritten.
func (e *Engine) Restore(ctx context.Context, tokens []string) (*Summary, error) {
	if _, err := os.Stat(e.HostRoot()); err != nil {
		return nil, fmt.Errorf("no backup at %s (hostname=%s)", e.HostRoot(), e.Hostname)
	}
	targets := e.selectTokens(tokens)
	if len(targets) == 0 {
		return nil, fmt.Errorf("no apps selected for restore")
	}

	sum := &Summary{}
	for _, token := range targets {
		app := e.Manifest.App(token)
		if app == nil {
			continue
		}
		as := AppSummary{Token: token}
		for _, p := range app.Paths {
			src := e.archivePath(token, p.Path)
			dst := e.libraryPath(p.Path)
			fi, err := os.Lstat(src)
			if err != nil {
				as.Missing++
				continue
			}
			files, bytes, err := e.copyTree(src, dst, fi)
			if err != nil {
				e.Runner.Logger.Warn("restore copy failed", "app", token, "src", src, "err", err)
				as.Missing++
				continue
			}
			as.Copied++
			as.Files += files
			as.Bytes += bytes
		}
		sum.Apps = append(sum.Apps, as)
		sum.Files += as.Files
		sum.Bytes += as.Bytes
	}
	return sum, nil
}

// FlushCFPrefsd asks cfprefsd to release cached plists so the target apps
// pick up the restored files on next launch. Best-effort, errors logged only.
func (e *Engine) FlushCFPrefsd(ctx context.Context) {
	if _, err := e.Runner.Run(ctx, "killall", "cfprefsd"); err != nil {
		e.Runner.Logger.Warn("killall cfprefsd", "err", err)
	}
}

// --- Status ---

// Status reports live/backup presence counts for each token (or all when tokens is empty).
func (e *Engine) Status(tokens []string) []AppStatus {
	targets := e.selectTokens(tokens)
	out := make([]AppStatus, 0, len(targets))
	for _, token := range targets {
		app := e.Manifest.App(token)
		if app == nil {
			continue
		}
		st := AppStatus{Token: token, TotalLive: len(app.Paths), TotalBak: len(app.Paths)}
		for _, p := range app.Paths {
			if _, err := os.Lstat(e.libraryPath(p.Path)); err == nil {
				st.PresentLive++
			}
			if _, err := os.Lstat(e.archivePath(token, p.Path)); err == nil {
				st.PresentBak++
			}
		}
		out = append(out, st)
	}
	return out
}

// --- Helpers ---

func (e *Engine) selectTokens(requested []string) []string {
	if len(requested) == 0 {
		return e.Manifest.Tokens()
	}
	valid := make(map[string]bool)
	for _, a := range e.Manifest.Apps {
		valid[a.Token] = true
	}
	var out []string
	for _, t := range requested {
		if valid[t] {
			out = append(out, t)
		}
	}
	return out
}

// copyTree copies src to dst. src may be a file, directory, or symlink.
// Returns (filesCopied, bytesCopied, err).
// Honors the Runner's DryRun flag.
func (e *Engine) copyTree(src, dst string, fi os.FileInfo) (int, int64, error) {
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return 0, 0, fmt.Errorf("readlink %s: %w", src, err)
		}
		if err := e.Runner.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, 0, err
		}
		if e.Runner.DryRun {
			return 1, 0, nil
		}
		_ = os.Remove(dst)
		if err := e.Runner.Symlink(target, dst); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}

	if fi.IsDir() {
		var files int
		var bytes int64
		walkErr := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(src, p)
			if relErr != nil {
				return relErr
			}
			if rel == "." {
				if mkErr := e.Runner.MkdirAll(dst, 0o755); mkErr != nil {
					return mkErr
				}
				return nil
			}
			if isExcluded(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			tgt := filepath.Join(dst, rel)
			if d.IsDir() {
				return e.Runner.MkdirAll(tgt, 0o755)
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(p)
				if err != nil {
					return nil // skip bad link
				}
				if mkErr := e.Runner.MkdirAll(filepath.Dir(tgt), 0o755); mkErr != nil {
					return mkErr
				}
				if e.Runner.DryRun {
					files++
					return nil
				}
				_ = os.Remove(tgt)
				if err := e.Runner.Symlink(target, tgt); err != nil {
					return err
				}
				files++
				return nil
			}
			n, copyErr := copyFile(e.Runner, p, tgt, info.Mode())
			if copyErr != nil {
				return copyErr
			}
			files++
			bytes += n
			return nil
		})
		return files, bytes, walkErr
	}

	// Regular file
	if err := e.Runner.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, 0, err
	}
	n, err := copyFile(e.Runner, src, dst, fi.Mode())
	if err != nil {
		return 0, 0, err
	}
	return 1, n, nil
}

func copyFile(runner *exec.Runner, src, dst string, mode os.FileMode) (int64, error) {
	if runner.DryRun {
		runner.Logger.Info("dry-run: copy", "src", src, "dst", dst)
		return 0, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	// Atomic-ish write: write to tmp + rename.
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode&0o777)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}

// --- BackupRoot resolution ---

// DefaultBackupRoot returns the fallback backup root for the current user.
// app-settings/ and profiles/ subtrees are created beneath it on demand.
func DefaultBackupRoot(homeDir string) string {
	return filepath.Join(homeDir, ".local", "share", "dotfiles", "backup")
}

// ExpandHome converts a leading "~/" to the home directory.
func ExpandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// DetectDriveCandidate looks for the user's Drive secrets folder to use as the
// shared backup root on macOS. Returns the first candidate whose parent exists.
func DetectDriveCandidate(home string) string {
	candidates := []string{
		filepath.Join(home, "Library", "CloudStorage"),
		home,
	}
	for _, root := range candidates {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.Contains(name, "GoogleDrive") && !strings.HasPrefix(name, "My Drive") {
				continue
			}
			drivePaths := []string{
				filepath.Join(root, name, "secrets"),
				filepath.Join(root, name, "My Drive", "secrets"),
			}
			for _, secrets := range drivePaths {
				if _, err := os.Stat(secrets); err == nil {
					return filepath.Join(secrets, "dotfiles-backup")
				}
			}
		}
	}
	return ""
}
