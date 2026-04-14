package ws

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ignoreDirs lists directory names that should never be scanned or flagged.
var ignoreDirs = map[string]struct{}{
	".git":          {},
	"node_modules":  {},
	".venv":         {},
	"venv":          {},
	"__pycache__":   {},
	".pytest_cache": {},
	".mypy_cache":   {},
	".ruff_cache":   {},
	".next":         {},
	".cache":        {},
	"_sys":          {},
}

// Mismatch describes a directory present on one side but missing on the other.
type Mismatch struct {
	RelPath string
	OnlyOn  Side  // SideWork or SideGdrive (never SideBoth/SideNone)
	IsEmpty bool  // whether the existing side's dir is empty
	Size    int64 // total bytes of contents (capped at 10 MiB sampling)
}

// AuditOptions controls the audit scope.
type AuditOptions struct {
	Scope string // rel path to limit scan; "" = whole tree
}

// Audit walks both trees (directories only) and returns mismatches sorted by rel path.
func Audit(roots Roots, opts AuditOptions) ([]Mismatch, error) {
	scope := ""
	if opts.Scope != "" {
		cleaned, err := ValidateRelPath(opts.Scope)
		if err != nil {
			return nil, fmt.Errorf("invalid scope: %w", err)
		}
		scope = cleaned
	}

	workDirs, err := scanDirs(roots.Work, scope)
	if err != nil {
		return nil, fmt.Errorf("scan work: %w", err)
	}
	gdriveDirs, err := scanDirs(roots.Gdrive, scope)
	if err != nil {
		return nil, fmt.Errorf("scan gdrive: %w", err)
	}

	var mismatches []Mismatch
	for rel := range workDirs {
		if _, ok := gdriveDirs[rel]; !ok {
			mismatches = append(mismatches, enrich(Mismatch{RelPath: rel, OnlyOn: SideWork}, filepath.Join(roots.Work, rel)))
		}
	}
	for rel := range gdriveDirs {
		if _, ok := workDirs[rel]; !ok {
			mismatches = append(mismatches, enrich(Mismatch{RelPath: rel, OnlyOn: SideGdrive}, filepath.Join(roots.Gdrive, rel)))
		}
	}
	sort.Slice(mismatches, func(i, j int) bool {
		return mismatches[i].RelPath < mismatches[j].RelPath
	})
	return mismatches, nil
}

// scanDirs returns a set of relative directory paths (relative to root) under scope.
// Rules:
//   - Skip anything whose name is in ignoreDirs (no descent)
//   - Skip symlinks entirely (no descent, no record)
//   - Only directories are recorded
//   - An absent root or absent scope directory yields an empty set (not an error)
func scanDirs(root, scope string) (map[string]struct{}, error) {
	start := root
	if scope != "" {
		start = filepath.Join(root, scope)
	}
	result := make(map[string]struct{})
	fi, err := os.Lstat(start)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return result, nil
	}
	if !fi.IsDir() {
		return result, nil
	}

	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // don't record the root itself
		}
		name := d.Name()
		if _, skip := ignoreDirs[name]; skip {
			return filepath.SkipDir
		}
		// Symlinks are never traversed or recorded
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		result[rel] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// enrich populates IsEmpty and Size for a mismatch by inspecting the existing side.
func enrich(m Mismatch, abs string) Mismatch {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return m
	}
	m.IsEmpty = len(entries) == 0
	if !m.IsEmpty {
		const cap = int64(10 * 1024 * 1024)
		var size int64
		_ = filepath.WalkDir(abs, func(_ string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || size >= cap {
				return filepath.SkipAll
			}
			if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err == nil {
				size += info.Size()
			}
			return nil
		})
		m.Size = size
	}
	return m
}

// FormatSize returns a human-readable byte size.
func FormatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	suf := []string{"KiB", "MiB", "GiB", "TiB"}[exp]
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), suf)
}

// String returns a human-readable one-line summary for display.
func (m Mismatch) String() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("only on %s", m.OnlyOn.Name()))
	if m.IsEmpty {
		parts = append(parts, "empty")
	} else {
		parts = append(parts, FormatSize(m.Size))
	}
	return fmt.Sprintf("%s (%s)", m.RelPath, strings.Join(parts, ", "))
}
