// Package aisettings backs up and restores portable AI assistant settings.
//
// The default manifest intentionally avoids auth tokens, caches, histories,
// sessions, logs, and generated/system bundles. Use IncludeAuth only when the
// operator explicitly wants local credentials included in an archive.
package aisettings

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

const (
	archiveVersion = 1
	homePrefix     = "home"
)

// Entry describes one home-relative path managed by the AI config archive.
type Entry struct {
	Tool        string `yaml:"tool"`
	Path        string `yaml:"path"`
	Description string `yaml:"description,omitempty"`
	Auth        bool   `yaml:"auth,omitempty"`
}

// EntrySummary captures the result for one manifest entry.
type EntrySummary struct {
	Tool    string `yaml:"tool"`
	Path    string `yaml:"path"`
	Auth    bool   `yaml:"auth,omitempty"`
	Copied  int    `yaml:"copied"`
	Missing int    `yaml:"missing"`
	Files   int    `yaml:"files"`
	Bytes   int64  `yaml:"bytes"`
}

// Summary aggregates a backup, restore, export, or import operation.
type Summary struct {
	Version string
	Path    string
	Entries []EntrySummary
	Files   int
	Bytes   int64
}

// Meta records snapshot/archive provenance.
type Meta struct {
	Version     string    `yaml:"version"`
	Tag         string    `yaml:"tag,omitempty"`
	Hostname    string    `yaml:"hostname,omitempty"`
	CreatedAt   time.Time `yaml:"created_at"`
	IncludeAuth bool      `yaml:"include_auth"`
	User        string    `yaml:"user,omitempty"`
}

// ArchiveManifest records which entries were present when the snapshot was made.
type ArchiveManifest struct {
	Schema      int            `yaml:"schema"`
	IncludeAuth bool           `yaml:"include_auth"`
	Entries     []EntrySummary `yaml:"entries"`
}

// Snapshot is one versioned backup under <root>/ai-config/<host>.
type Snapshot struct {
	Version     string
	Tag         string
	CreatedAt   time.Time
	Path        string
	IsLatest    bool
	IncludeAuth bool
	Files       int
}

// Status reports live and backup presence for an entry.
type Status struct {
	Entry         Entry
	PresentLive   bool
	PresentBackup bool
}

// Engine executes AI config backup/restore/export/import operations.
type Engine struct {
	Runner   *exec.Runner
	HomeDir  string
	Root     string
	Hostname string
	User     string
}

// Entries returns the static AI config manifest.
func Entries(includeAuth bool) []Entry {
	entries := []Entry{
		{Tool: "claude", Path: ".config/claude/settings.json", Description: "dotfiles-managed Claude settings"},
		{Tool: "claude", Path: ".claude/settings.json", Description: "Claude Code settings"},
		{Tool: "claude", Path: ".claude/CLAUDE.md", Description: "Claude Code global instructions"},
		{Tool: "claude", Path: ".claude/agents", Description: "Claude agents"},
		{Tool: "claude", Path: ".claude/commands", Description: "Claude commands"},
		{Tool: "claude", Path: ".claude/hooks", Description: "Claude hooks"},
		{Tool: "claude", Path: ".claude/mcp.json", Description: "Claude MCP config"},
		{Tool: "claude", Path: ".claude/skills", Description: "Claude skills"},
		{Tool: "codex", Path: ".codex/AGENTS.md", Description: "Codex global instructions"},
		{Tool: "codex", Path: ".codex/config.toml", Description: "Codex config and MCP servers"},
		{Tool: "codex", Path: ".codex/prompts", Description: "Codex prompts"},
		{Tool: "codex", Path: ".codex/rules", Description: "Codex rules"},
		{Tool: "codex", Path: ".codex/skills", Description: "Codex user skills"},
		{Tool: "agents", Path: ".agents/.skill-lock.json", Description: "shared skill lock"},
		{Tool: "agents", Path: ".agents/skills", Description: "shared user skills"},
	}
	authEntries := []Entry{
		{Tool: "claude", Path: ".claude/settings.local.json", Description: "Claude local/auth settings", Auth: true},
		{Tool: "claude", Path: ".config/claude/settings.local.json", Description: "Claude local/auth settings", Auth: true},
		{Tool: "codex", Path: ".codex/auth.json", Description: "Codex auth credentials", Auth: true},
	}
	if includeAuth {
		entries = append(entries, authEntries...)
	}
	return entries
}

// AIConfigRoot returns the subtree containing all host snapshots.
func (e *Engine) AIConfigRoot() string {
	return filepath.Join(e.Root, "ai-config")
}

// HostRoot returns the host-specific snapshot directory.
func (e *Engine) HostRoot() string {
	return filepath.Join(e.AIConfigRoot(), e.Hostname)
}

// VersionPath returns the path for a version id.
func (e *Engine) VersionPath(version string) string {
	return filepath.Join(e.HostRoot(), version)
}

// LatestPointerPath returns <host-root>/latest.txt.
func (e *Engine) LatestPointerPath() string {
	return filepath.Join(e.HostRoot(), "latest.txt")
}

// BackupOptions controls Backup.
type BackupOptions struct {
	Tag         string
	IncludeAuth bool
}

// RestoreOptions controls Restore and Import.
type RestoreOptions struct {
	Version     string
	IncludeAuth bool
}

// Backup creates a new host-scoped versioned snapshot.
func (e *Engine) Backup(opts BackupOptions) (*Summary, error) {
	version := e.uniqueVersion(time.Now())
	dest := e.VersionPath(version)
	if err := e.Runner.MkdirAll(filepath.Join(dest, homePrefix), 0o755); err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	sum, err := e.copyFromHome(filepath.Join(dest, homePrefix), opts.IncludeAuth)
	if err != nil {
		return nil, err
	}
	sum.Version = version
	sum.Path = dest
	if err := e.writeSnapshotMetadata(dest, version, opts.Tag, opts.IncludeAuth, sum); err != nil {
		return nil, err
	}
	if err := e.Runner.WriteFile(e.LatestPointerPath(), []byte(version+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("write latest.txt: %w", err)
	}
	return sum, nil
}

// Restore restores a versioned snapshot into HomeDir.
func (e *Engine) Restore(opts RestoreOptions) (*Summary, error) {
	version := opts.Version
	if version == "" {
		v, err := e.ResolveLatest()
		if err != nil {
			return nil, err
		}
		version = v
	}
	src := e.VersionPath(version)
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("snapshot %s not found at %s", version, src)
	}
	sum, err := e.restoreFromSnapshotRoot(src, opts.IncludeAuth)
	if err != nil {
		return nil, err
	}
	sum.Version = version
	sum.Path = src
	return sum, nil
}

// Export writes a portable tar.gz archive.
func (e *Engine) Export(path string, opts BackupOptions) (*Summary, error) {
	if e.Runner.DryRun {
		e.Runner.Logger.Info("dry-run: export AI config", "path", path)
		sum, err := e.copyFromHome("", opts.IncludeAuth)
		if err != nil {
			return nil, err
		}
		sum.Path = path
		return sum, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create export dir: %w", err)
	}
	out, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	defer out.Close()
	gw := gzip.NewWriter(out)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	version := NewVersion(time.Now())
	sum, err := e.addHomeToTar(tw, opts.IncludeAuth)
	if err != nil {
		return nil, err
	}
	sum.Version = version
	sum.Path = path
	if err := addYAMLToTar(tw, "meta.yaml", Meta{
		Version:     version,
		Tag:         opts.Tag,
		Hostname:    e.Hostname,
		CreatedAt:   time.Now().UTC(),
		IncludeAuth: opts.IncludeAuth,
		User:        e.User,
	}); err != nil {
		return nil, err
	}
	if err := addYAMLToTar(tw, "manifest.yaml", ArchiveManifest{
		Schema:      archiveVersion,
		IncludeAuth: opts.IncludeAuth,
		Entries:     sum.Entries,
	}); err != nil {
		return nil, err
	}
	return sum, nil
}

// Import restores a portable tar.gz archive into HomeDir.
func (e *Engine) Import(path string, opts RestoreOptions) (*Summary, error) {
	tmp, err := os.MkdirTemp("", "dotfiles-ai-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(path, tmp); err != nil {
		return nil, err
	}
	sum, err := e.restoreFromSnapshotRoot(tmp, opts.IncludeAuth)
	if err != nil {
		return nil, err
	}
	sum.Path = path
	if meta, _ := readMeta(filepath.Join(tmp, "meta.yaml")); meta != nil {
		sum.Version = meta.Version
	}
	return sum, nil
}

// Status reports live and latest-backup presence.
func (e *Engine) Status(includeAuth bool) []Status {
	latest, _ := e.ResolveLatest()
	var out []Status
	for _, entry := range Entries(includeAuth) {
		st := Status{Entry: entry}
		if _, err := os.Lstat(filepath.Join(e.HomeDir, entry.Path)); err == nil {
			st.PresentLive = true
		}
		if latest != "" {
			if _, err := os.Lstat(filepath.Join(e.VersionPath(latest), homePrefix, entry.Path)); err == nil {
				st.PresentBackup = true
			}
		}
		out = append(out, st)
	}
	return out
}

// List enumerates snapshots newest-first.
func (e *Engine) List() ([]Snapshot, error) {
	return e.list(false)
}

func (e *Engine) list(withoutLatest bool) ([]Snapshot, error) {
	entries, err := os.ReadDir(e.HostRoot())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	latest := ""
	if !withoutLatest {
		latest, _ = e.readLatestPointer()
	}
	var out []Snapshot
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		version := entry.Name()
		meta, _ := readMeta(filepath.Join(e.HostRoot(), version, "meta.yaml"))
		manifest, _ := readArchiveManifest(filepath.Join(e.HostRoot(), version, "manifest.yaml"))
		s := Snapshot{
			Version:  version,
			Path:     filepath.Join(e.HostRoot(), version),
			IsLatest: version == latest,
		}
		if meta != nil {
			s.Tag = meta.Tag
			s.CreatedAt = meta.CreatedAt
			s.IncludeAuth = meta.IncludeAuth
		}
		if manifest != nil {
			for _, e := range manifest.Entries {
				s.Files += e.Files
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

// ResolveLatest returns the latest snapshot id.
func (e *Engine) ResolveLatest() (string, error) {
	if latest, err := e.readLatestPointer(); err == nil {
		return latest, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	all, lerr := e.list(true)
	if lerr != nil {
		return "", lerr
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no snapshots under %s", e.HostRoot())
	}
	return all[0].Version, nil
}

func (e *Engine) readLatestPointer() (string, error) {
	data, err := os.ReadFile(e.LatestPointerPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// NewVersion returns a UTC filesystem-safe version id.
func NewVersion(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

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

func (e *Engine) copyFromHome(destRoot string, includeAuth bool) (*Summary, error) {
	sum := &Summary{}
	for _, entry := range Entries(includeAuth) {
		src := filepath.Join(e.HomeDir, entry.Path)
		es := EntrySummary{Tool: entry.Tool, Path: entry.Path, Auth: entry.Auth}
		info, err := os.Lstat(src)
		if err != nil {
			es.Missing = 1
			sum.Entries = append(sum.Entries, es)
			continue
		}
		if destRoot == "" {
			files, bytes, err := countTree(src, info)
			if err != nil {
				return nil, fmt.Errorf("count %s: %w", entry.Path, err)
			}
			es.Copied = 1
			es.Files = files
			es.Bytes = bytes
		} else {
			files, bytes, err := e.copyTree(src, filepath.Join(destRoot, entry.Path), info, entry.Path)
			if err != nil {
				return nil, fmt.Errorf("copy %s: %w", entry.Path, err)
			}
			es.Copied = 1
			es.Files = files
			es.Bytes = bytes
		}
		sum.Files += es.Files
		sum.Bytes += es.Bytes
		sum.Entries = append(sum.Entries, es)
	}
	return sum, nil
}

func (e *Engine) addHomeToTar(tw *tar.Writer, includeAuth bool) (*Summary, error) {
	sum := &Summary{}
	for _, entry := range Entries(includeAuth) {
		src := filepath.Join(e.HomeDir, entry.Path)
		es := EntrySummary{Tool: entry.Tool, Path: entry.Path, Auth: entry.Auth}
		info, err := os.Lstat(src)
		if err != nil {
			es.Missing = 1
			sum.Entries = append(sum.Entries, es)
			continue
		}
		files, bytes, err := addPathToTar(tw, src, filepath.Join(homePrefix, entry.Path), info, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", entry.Path, err)
		}
		es.Copied = 1
		es.Files = files
		es.Bytes = bytes
		sum.Files += files
		sum.Bytes += bytes
		sum.Entries = append(sum.Entries, es)
	}
	return sum, nil
}

func (e *Engine) restoreFromSnapshotRoot(root string, includeAuth bool) (*Summary, error) {
	manifest, _ := readArchiveManifest(filepath.Join(root, "manifest.yaml"))
	var entries []EntrySummary
	if manifest != nil {
		entries = manifest.Entries
	} else {
		for _, entry := range Entries(includeAuth) {
			entries = append(entries, EntrySummary{Tool: entry.Tool, Path: entry.Path, Auth: entry.Auth})
		}
	}
	sum := &Summary{}
	for _, entry := range entries {
		if entry.Auth && !includeAuth {
			continue
		}
		if !isSafeRel(entry.Path) {
			return nil, fmt.Errorf("unsafe archive path %q", entry.Path)
		}
		src := filepath.Join(root, homePrefix, entry.Path)
		dst := filepath.Join(e.HomeDir, entry.Path)
		es := EntrySummary{Tool: entry.Tool, Path: entry.Path, Auth: entry.Auth}
		info, err := os.Lstat(src)
		if err != nil {
			es.Missing = 1
			sum.Entries = append(sum.Entries, es)
			continue
		}
		if err := e.backupExisting(dst, entry.Path); err != nil {
			return nil, err
		}
		if !e.Runner.DryRun {
			_ = os.RemoveAll(dst)
		}
		files, bytes, err := e.copyTree(src, dst, info, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("restore %s: %w", entry.Path, err)
		}
		es.Copied = 1
		es.Files = files
		es.Bytes = bytes
		sum.Files += files
		sum.Bytes += bytes
		sum.Entries = append(sum.Entries, es)
	}
	return sum, nil
}

func (e *Engine) writeSnapshotMetadata(dest, version, tag string, includeAuth bool, sum *Summary) error {
	if err := writeYAML(e.Runner, filepath.Join(dest, "meta.yaml"), Meta{
		Version:     version,
		Tag:         tag,
		Hostname:    e.Hostname,
		CreatedAt:   time.Now().UTC(),
		IncludeAuth: includeAuth,
		User:        e.User,
	}); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	if err := writeYAML(e.Runner, filepath.Join(dest, "manifest.yaml"), ArchiveManifest{
		Schema:      archiveVersion,
		IncludeAuth: includeAuth,
		Entries:     sum.Entries,
	}); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func (e *Engine) backupExisting(path, rel string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(e.HomeDir, ".local", "share", "dotfiles", "backup", "ai", ts, rel)
	_, _, err = e.copyTree(path, dst, info, rel)
	if err != nil {
		return fmt.Errorf("backup existing %s: %w", path, err)
	}
	return nil
}

func (e *Engine) copyTree(src, dst string, info os.FileInfo, relRoot string) (int, int64, error) {
	if isExcluded(relRoot) {
		return 0, 0, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return 0, 0, err
		}
		if err := e.Runner.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, 0, err
		}
		if e.Runner.DryRun {
			return 1, 0, nil
		}
		_ = os.Remove(dst)
		return 1, 0, e.Runner.Symlink(target, dst)
	}
	if info.IsDir() {
		var files int
		var bytes int64
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			entryRel := relRoot
			if rel != "." {
				entryRel = filepath.Join(relRoot, rel)
			}
			if isExcluded(entryRel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			target := filepath.Join(dst, rel)
			if rel == "." {
				return e.Runner.MkdirAll(target, info.Mode()&0o777)
			}
			di, err := d.Info()
			if err != nil {
				return err
			}
			if di.Mode()&os.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				if err != nil {
					return nil
				}
				if err := e.Runner.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				if !e.Runner.DryRun {
					_ = os.Remove(target)
					if err := e.Runner.Symlink(link, target); err != nil {
						return err
					}
				}
				files++
				return nil
			}
			if di.IsDir() {
				return e.Runner.MkdirAll(target, di.Mode()&0o777)
			}
			n, err := copyFile(e.Runner, path, target, di.Mode())
			if err != nil {
				return err
			}
			files++
			bytes += n
			return nil
		})
		return files, bytes, err
	}
	if err := e.Runner.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, 0, err
	}
	n, err := copyFile(e.Runner, src, dst, info.Mode())
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
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
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

func countTree(src string, info os.FileInfo) (int, int64, error) {
	if isExcluded(src) {
		return 0, 0, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 1, 0, nil
	}
	if !info.IsDir() {
		return 1, info.Size(), nil
	}
	var files int
	var bytes int64
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel != "." && isExcluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files++
		if info.Mode()&os.ModeSymlink == 0 {
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes, err
}

func addPathToTar(tw *tar.Writer, src, name string, info os.FileInfo, relRoot string) (int, int64, error) {
	if isExcluded(relRoot) {
		return 0, 0, nil
	}
	if info.IsDir() {
		var files int
		var bytes int64
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			entryRel := relRoot
			if rel != "." {
				entryRel = filepath.Join(relRoot, rel)
			}
			if isExcluded(entryRel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			archiveName := filepath.ToSlash(filepath.Join(name, rel))
			di, err := d.Info()
			if err != nil {
				return err
			}
			f, b, err := addOneToTar(tw, path, archiveName, di)
			files += f
			bytes += b
			return err
		})
		return files, bytes, err
	}
	return addOneToTar(tw, src, filepath.ToSlash(name), info)
}

func addOneToTar(tw *tar.Writer, src, name string, info os.FileInfo) (int, int64, error) {
	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return 0, 0, err
		}
		link = target
	}
	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return 0, 0, err
	}
	header.Name = strings.TrimPrefix(name, "./")
	if err := tw.WriteHeader(header); err != nil {
		return 0, 0, err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if info.IsDir() {
			return 0, 0, nil
		}
		return 1, 0, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, 0, err
	}
	defer in.Close()
	n, err := io.Copy(tw, in)
	return 1, n, err
}

func addYAMLToTar(tw *tar.Writer, name string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	header := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}

func extractTarGz(path, dest string) error {
	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer in.Close()
	gr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("read gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}

func safeJoin(root, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(root, clean)
	rootClean := filepath.Clean(root)
	if target != rootClean && !strings.HasPrefix(target, rootClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

func writeYAML(runner *exec.Runner, path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := runner.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return runner.WriteFile(path, data, 0o644)
}

func readMeta(path string) (*Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta Meta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func readArchiveManifest(path string) (*ArchiveManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest ArchiveManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func isSafeRel(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func isExcluded(rel string) bool {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case ".ds_store", ".system", ".tmp", "tmp", "cache", "caches", "logs", "log",
			"sessions", "session-env", "projects", "file-history", "telemetry", "statsig":
			return true
		}
		if strings.Contains(lower, "cache") {
			return true
		}
		if strings.HasPrefix(part, "Singleton") {
			return true
		}
	}
	switch {
	case strings.HasSuffix(rel, ".log"),
		strings.HasSuffix(rel, ".lock"),
		strings.HasSuffix(rel, ".sqlite"),
		strings.HasSuffix(rel, ".sqlite-shm"),
		strings.HasSuffix(rel, ".sqlite-wal"),
		strings.HasSuffix(rel, ".jsonl"):
		return true
	}
	return false
}
