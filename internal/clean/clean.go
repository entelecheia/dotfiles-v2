package clean

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MatchKind classifies a match target.
type MatchKind int

const (
	KindDirectory MatchKind = iota
	KindFile
)

// RiskLevel gates whether a pattern requires --all.
type RiskLevel int

const (
	RiskSafe RiskLevel = iota
	RiskHigh
)

// Pattern describes one cleanup target type.
type Pattern struct {
	Name      string
	Kind      MatchKind
	Risk      RiskLevel
	NeedProbe bool   // env/ needs pyvenv.cfg check
	Category  string // node, python, build, cache, misc
}

// Match represents a single discovered cleanup target.
type Match struct {
	Path      string
	Pattern   Pattern
	Size      int64
	RelPath   string
	Protected bool
}

// Result holds the complete scan output.
type Result struct {
	Root      string
	Matches   []Match
	Protected []Match
}

// TotalSize returns the sum of all match sizes.
func (r *Result) TotalSize() int64 {
	var total int64
	for _, m := range r.Matches {
		total += m.Size
	}
	return total
}

// DefaultPatterns defines cleanup targets.
var DefaultPatterns = []Pattern{
	// Node
	{Name: "node_modules", Kind: KindDirectory, Risk: RiskSafe, Category: "node"},
	// Python caches
	{Name: "__pycache__", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	{Name: ".pytest_cache", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	{Name: ".mypy_cache", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	{Name: ".ruff_cache", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	// Python venvs
	{Name: ".venv", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	{Name: "venv", Kind: KindDirectory, Risk: RiskSafe, Category: "python"},
	{Name: "env", Kind: KindDirectory, Risk: RiskSafe, NeedProbe: true, Category: "python"},
	// Build caches
	{Name: ".next", Kind: KindDirectory, Risk: RiskSafe, Category: "cache"},
	{Name: ".cache", Kind: KindDirectory, Risk: RiskSafe, Category: "cache"},
	// macOS junk
	{Name: ".DS_Store", Kind: KindFile, Risk: RiskSafe, Category: "misc"},
	// Risky build outputs
	{Name: "dist", Kind: KindDirectory, Risk: RiskHigh, Category: "build"},
	{Name: "build", Kind: KindDirectory, Risk: RiskHigh, Category: "build"},
	{Name: "out", Kind: KindDirectory, Risk: RiskHigh, Category: "build"},
	{Name: "target", Kind: KindDirectory, Risk: RiskHigh, Category: "build"},
}

// Scanner walks a root directory finding cleanup targets.
type Scanner struct {
	Root             string
	Patterns         []Pattern
	IncludeRisky     bool
	ProtectedPrefixes []string
}

// NewScanner creates a Scanner with default patterns and _sys/ protection.
func NewScanner(root string, includeRisky bool) *Scanner {
	absRoot, _ := filepath.Abs(root)
	return &Scanner{
		Root:         absRoot,
		Patterns:     DefaultPatterns,
		IncludeRisky: includeRisky,
		ProtectedPrefixes: []string{
			filepath.Join(absRoot, "_sys"),
		},
	}
}

// Scan walks the root and finds cleanup targets.
func (s *Scanner) Scan() (*Result, error) {
	root := s.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
		// Rebuild protected prefixes with the resolved root so symlink
		// targets (e.g. macOS /private/var/...) match correctly.
		s.ProtectedPrefixes = []string{filepath.Join(root, "_sys")}
	}

	// Build lookup sets
	dirPatterns := make(map[string]Pattern)
	filePatterns := make(map[string]Pattern)
	for _, p := range s.Patterns {
		if !s.IncludeRisky && p.Risk == RiskHigh {
			continue
		}
		if p.Kind == KindFile {
			filePatterns[p.Name] = p
		} else {
			dirPatterns[p.Name] = p
		}
	}

	result := &Result{Root: root}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()

		// Skip .git directories
		if name == ".git" && d.IsDir() {
			return fs.SkipDir
		}

		// File-level matches (e.g. .DS_Store)
		if !d.IsDir() && d.Type()&fs.ModeSymlink == 0 {
			if p, ok := filePatterns[name]; ok {
				var size int64
				if info, err := d.Info(); err == nil {
					size = info.Size()
				}
				m := Match{
					Path:    path,
					Pattern: p,
					Size:    size,
					RelPath: relPath(s.Root, path),
				}
				if s.isProtected(path) {
					m.Protected = true
					result.Protected = append(result.Protected, m)
				} else {
					result.Matches = append(result.Matches, m)
				}
			}
			return nil
		}

		// Directory matches
		if !d.IsDir() {
			return nil // skip symlinks
		}

		p, ok := dirPatterns[name]
		if !ok {
			return nil
		}

		// Probe check: env/ must contain pyvenv.cfg
		if p.NeedProbe {
			if _, err := os.Stat(filepath.Join(path, "pyvenv.cfg")); err != nil {
				return nil // not a venv, continue walking into it
			}
		}

		m := Match{
			Path:    path,
			Pattern: p,
			Size:    dirSize(path),
			RelPath: relPath(s.Root, path),
		}

		if s.isProtected(path) {
			m.Protected = true
			result.Protected = append(result.Protected, m)
		} else {
			result.Matches = append(result.Matches, m)
		}
		return fs.SkipDir
	})

	return result, err
}

// isProtected checks if a path is inside a protected prefix.
func (s *Scanner) isProtected(path string) bool {
	abs, _ := filepath.Abs(path)
	for _, prefix := range s.ProtectedPrefixes {
		prefixAbs, _ := filepath.Abs(prefix)
		if abs == prefixAbs || strings.HasPrefix(abs, prefixAbs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Delete removes all non-protected matches.
func Delete(matches []Match) (deleted int, freed int64, errors []error) {
	for _, m := range matches {
		if m.Protected {
			continue
		}
		var err error
		if m.Pattern.Kind == KindFile {
			err = os.Remove(m.Path)
		} else {
			err = os.RemoveAll(m.Path)
		}
		if err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", m.RelPath, err))
			continue
		}
		deleted++
		freed += m.Size
	}
	return
}

// FormatSize returns a human-readable size string.
func FormatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%dM", bytes/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%dK", bytes/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
