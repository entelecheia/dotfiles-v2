package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckPath decides whether a file mutation is allowed under the freeze
// boundary. An empty boundary, empty path, or unresolvable input allows
// (fail open, matching gstack).
func CheckPath(filePath, cwd, boundary, homeDir string) Decision {
	boundary = strings.TrimSpace(boundary)
	filePath = strings.TrimSpace(filePath)
	if boundary == "" || filePath == "" {
		return Decision{}
	}
	abs := filePath
	if !filepath.IsAbs(abs) {
		if cwd == "" {
			return Decision{} // cannot resolve relative path; fail open
		}
		abs = filepath.Join(cwd, abs)
	}
	// Resolve the target fully when it exists so a symlink file inside the
	// boundary cannot smuggle writes outside it; fall back to parent-only
	// resolution for not-yet-existing files.
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		abs = resolveParentSymlinks(abs)
	}
	// The boundary is an existing directory, so resolve it fully; fall back
	// to parent-only resolution if it has gone missing.
	root := filepath.Clean(boundary)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	} else {
		root = resolveParentSymlinks(root)
	}

	for _, exempt := range exemptRootsFn(homeDir) {
		if within(abs, exempt) {
			return Decision{}
		}
	}
	if within(abs, root) {
		return Decision{}
	}
	return Decision{
		Permission: "deny",
		Reason:     fmt.Sprintf("Blocked: %s is outside the freeze boundary (%s). Only edits within the frozen directory are allowed.", abs, root),
		Pattern:    "freeze_boundary",
	}
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// resolveParentSymlinks normalizes a path by resolving symlinks on its
// nearest existing ancestor and re-appending the rest, so not-yet-existing
// files (any depth) still normalize consistently with the boundary
// (improves on gstack's `cd dir && pwd -P`, which only handled an existing
// direct parent).
func resolveParentSymlinks(path string) string {
	dir := filepath.Dir(path)
	rest := []string{filepath.Base(path)}
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			parts := append([]string{resolved}, reverseStrings(rest)...)
			return filepath.Join(parts...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path // reached filesystem root without resolving
		}
		rest = append(rest, filepath.Base(dir))
		dir = parent
	}
}

func reverseStrings(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// exemptRootsFn is swappable in tests (t.TempDir lives under os.TempDir(),
// which would otherwise exempt every test path from boundary checks).
var exemptRootsFn = exemptRoots

// exemptRoots lists always-writable locations. Deliberate deviation from
// gstack: its hooks were session-scoped, dot's persist across sessions, so
// scratch dirs and Claude Code plan files must stay writable or every
// planning session breaks while frozen.
func exemptRoots(homeDir string) []string {
	roots := []string{"/tmp", "/private/tmp"}
	if tmp := os.TempDir(); tmp != "" {
		tmp = filepath.Clean(tmp)
		if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
			tmp = resolved
		}
		roots = append(roots, tmp)
	}
	if homeDir != "" {
		plans := filepath.Join(homeDir, ".claude", "plans")
		if resolved, err := filepath.EvalSymlinks(plans); err == nil {
			plans = resolved
		}
		roots = append(roots, plans)
	}
	return roots
}
