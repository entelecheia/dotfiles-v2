package driveexclude

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DirStatus represents the exclusion state of a directory.
type DirStatus int

const (
	StatusExcluded DirStatus = iota // xattr already set
	StatusPending                   // needs xattr
	StatusSymlink                   // legacy symlink, needs migration
	StatusOutside                   // not in Drive, skip
)

func (s DirStatus) String() string {
	switch s {
	case StatusExcluded:
		return "EXCLUDED"
	case StatusPending:
		return "PENDING"
	case StatusSymlink:
		return "SYMLINK"
	case StatusOutside:
		return "OUTSIDE"
	default:
		return "UNKNOWN"
	}
}

// ScanResult describes a single discovered directory.
type ScanResult struct {
	Path       string
	Pattern    string // which pattern matched
	Size       int64
	Status     DirStatus
	LinkTarget string // non-empty if symlink
}

// DefaultExcludePatterns are directory names to exclude from Drive sync.
var DefaultExcludePatterns = []string{
	// Node.js
	"node_modules",
	".pnpm",
	// Build caches
	".astro",
	".next",
	".nuxt",
	".svelte-kit",
	".parcel-cache",
	".turbo",
	// Python
	".venv",
	"__pycache__",
	".mypy_cache",
	".pytest_cache",
	// Misc
	".angular",
	".webpack",
}

// Scanner finds excludable directories within a Drive-synced path.
type Scanner struct {
	Root     string
	Patterns []string
}

// NewScanner creates a Scanner with default patterns.
func NewScanner(root string) *Scanner {
	return &Scanner{
		Root:     root,
		Patterns: DefaultExcludePatterns,
	}
}

// Scan walks the root and finds directories matching exclude patterns.
func (s *Scanner) Scan() ([]ScanResult, error) {
	// Resolve symlinks in root — Go WalkDir does not reliably traverse
	// Google Drive FUSE mounts accessed through a symlink.
	root := s.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	patternSet := make(map[string]bool, len(s.Patterns))
	for _, p := range s.Patterns {
		patternSet[p] = true
	}

	var results []ScanResult

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

		// Check symlinks to directories separately — WalkDir reports
		// symlinks as non-directories even when the target is a dir.
		isSymlink := d.Type()&fs.ModeSymlink != 0
		if !d.IsDir() && !isSymlink {
			return nil
		}

		if !patternSet[name] {
			if isSymlink {
				return nil // don't follow symlinks for traversal
			}
			return nil
		}

		result := ScanResult{
			Path:    path,
			Pattern: name,
		}

		if isSymlink {
			target, _ := os.Readlink(path)
			result.Status = StatusSymlink
			result.LinkTarget = target
			result.Size = dirSize(path)
			results = append(results, result)
			return nil // symlink — no SkipDir needed
		}

		// Real directory — check xattr status
		excluded, _ := hasIgnoreContent(path)
		if excluded {
			result.Status = StatusExcluded
		} else {
			result.Status = StatusPending
		}

		result.Size = dirSize(path)
		results = append(results, result)
		return fs.SkipDir
	})

	return results, err
}

// ApplyXattr sets the Drive ignore xattr on a directory.
func ApplyXattr(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return setIgnoreContent(path)
}

// CheckXattr checks if the Drive ignore xattr is set.
func CheckXattr(path string) (bool, error) {
	return hasIgnoreContent(path)
}

// RemoveXattr removes the Drive ignore xattr.
func RemoveXattr(path string) error {
	return removeIgnoreContent(path)
}

// RelPath returns path relative to root, or the original path on error.
func RelPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
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

// SumPendingSize totals the size of all pending results.
func SumPendingSize(results []ScanResult) int64 {
	var total int64
	for _, r := range results {
		if r.Status == StatusPending {
			total += r.Size
		}
	}
	return total
}

// CountByStatus returns counts grouped by DirStatus.
func CountByStatus(results []ScanResult) map[DirStatus]int {
	counts := make(map[DirStatus]int)
	for _, r := range results {
		counts[r.Status]++
	}
	return counts
}

// dirSize computes total size of a directory tree.
// Returns 0 on error (best-effort).
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

// IsDarwin reports whether we're on macOS.
func IsDarwin() bool {
	return runtime.GOOS == "darwin"
}

// FilterByPattern splits results by pattern category.
func FilterByPattern(results []ScanResult, patterns []string) []ScanResult {
	pset := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		pset[p] = true
	}
	var filtered []ScanResult
	for _, r := range results {
		if pset[r.Pattern] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// IsInDrive checks if a path appears to be inside a Google Drive mount.
func IsInDrive(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	// Common Google Drive mount paths on macOS
	return strings.Contains(abs, "Google Drive") ||
		strings.Contains(abs, "My Drive") ||
		strings.Contains(abs, "GoogleDrive")
}
