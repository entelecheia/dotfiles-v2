package gdrivesync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SharedReason classifies how an entry was identified as shared.
type SharedReason int

const (
	// SharedAuto means the entry was detected by a Drive Desktop
	// filesystem property (a symlink resolving into the
	// .shortcut-targets-by-id store, or a Shared drives/ root).
	SharedAuto SharedReason = iota
	// SharedManual means the entry came from
	// state.modules.gdrive_sync.shared_excludes (operator-curated for
	// owned-but-shared-out folders that have no filesystem signal).
	SharedManual
)

// String returns a stable two-letter token for the source column of
// `dot gdrive-sync shared list` output.
func (r SharedReason) String() string {
	switch r {
	case SharedAuto:
		return "auto"
	case SharedManual:
		return "manual"
	default:
		return "?"
	}
}

// SharedEntry is one row returned by ScanShared.
type SharedEntry struct {
	RelPath string
	Reason  SharedReason
	Detail  string // human-readable detail (e.g. realpath suffix); "" for plain manual
}

// scanMaxDepth limits how deep ScanShared walks before giving up.
// Drive shortcuts realistically appear within the first few levels
// under MirrorPath; deeper recursion is paid for by every Pull/Push.
const scanMaxDepth = 3

// shortcutTargetsBasename is the canonical hidden directory the
// Google Drive Desktop client uses to store the contents of "shared
// with me" shortcuts that have been added to the user's My Drive.
const shortcutTargetsBasename = ".shortcut-targets-by-id"

// sharedDrivesBasename is the top-level directory the Drive Desktop
// client mounts shared drives under, parallel to "My Drive".
const sharedDrivesBasename = "Shared drives"

// DetectSharedEntry returns true (and a human-readable detail) if
// path is a Drive shortcut surfaced from outside the user's My Drive.
// Property-based — never inspects the basename for name patterns.
//
// The two real-world cases we catch:
//
//	A. path is a symlink whose realpath crosses a directory whose
//	   basename is ".shortcut-targets-by-id" — i.e. the user added a
//	   shared-with-me shortcut to My Drive, and Drive Desktop is
//	   surfacing it as a symlink.
//	B. path is a symlink whose realpath has a "Shared drives"
//	   ancestor — i.e. shared-drive content surfaced into My Drive.
//
// Returns (false, "") for everything else, including plain symlinks
// to sibling directories. Callers can ignore the detail string.
func DetectSharedEntry(path string) (bool, string) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false, ""
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Broken symlink — skip but report so the operator notices.
		raw, _ := os.Readlink(path)
		return true, fmt.Sprintf("broken symlink → %s", raw)
	}
	if pathHasComponent(target, shortcutTargetsBasename) {
		return true, fmt.Sprintf("symlink → %s", shortcutTargetsBasename)
	}
	if pathHasComponent(target, sharedDrivesBasename) {
		return true, fmt.Sprintf("symlink → %s", sharedDrivesBasename)
	}
	return false, ""
}

// IsSharedDriveMount reports whether mirrorPath itself resolves under
// a Drive Desktop "Shared drives" root. Pull/Push refuse to run in
// that case — the workspace-authoritative semantics make no sense for
// content the user does not own.
func IsSharedDriveMount(mirrorPath string) bool {
	target, err := filepath.EvalSymlinks(strings.TrimRight(mirrorPath, "/"))
	if err != nil {
		return false
	}
	return pathHasComponent(target, sharedDrivesBasename)
}

// ScanShared walks root up to scanMaxDepth, calls DetectSharedEntry
// on every symlink it encounters, and merges the results with the
// operator's manual list. Each manual entry is reported even if it
// is missing on disk so the user can prune via `shared remove`.
//
// Output is sorted by RelPath. Auto and manual entries for the same
// path collapse to a single row tagged Manual (operator intent wins).
func ScanShared(root string, manual []string) ([]SharedEntry, error) {
	root = strings.TrimRight(root, "/")
	if root == "" {
		return nil, nil
	}

	entries := make(map[string]SharedEntry)

	// Phase 1: walk the real tree (depth-limited) for symlinks.
	if _, err := os.Stat(root); err == nil {
		if err := walkForShortcuts(root, entries); err != nil {
			return nil, err
		}
	}

	// Phase 2: layer the manual list on top.
	for _, rel := range manual {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		entry := SharedEntry{RelPath: rel, Reason: SharedManual}
		full := filepath.Join(root, rel)
		if _, err := os.Lstat(full); err != nil {
			entry.Detail = "(missing)"
		}
		entries[rel] = entry // overrides any auto match — operator intent wins
	}

	out := make([]SharedEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

// walkForShortcuts is a depth-limited filesystem walk that records
// any symlink resolving into a Drive shared location.
func walkForShortcuts(root string, out map[string]SharedEntry) error {
	type frame struct {
		path  string
		depth int
	}
	stack := []frame{{path: root, depth: 0}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.depth > scanMaxDepth {
			continue
		}
		dirents, err := os.ReadDir(cur.path)
		if err != nil {
			// Permission denied or transient — skip silently; the
			// operator can re-run with the appropriate privileges.
			continue
		}
		for _, d := range dirents {
			full := filepath.Join(cur.path, d.Name())
			info, err := d.Info()
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if shared, detail := DetectSharedEntry(full); shared {
					rel, err := filepath.Rel(root, full)
					if err == nil {
						out[rel] = SharedEntry{
							RelPath: rel,
							Reason:  SharedAuto,
							Detail:  detail,
						}
					}
				}
				continue
			}
			if info.IsDir() && cur.depth+1 <= scanMaxDepth {
				stack = append(stack, frame{path: full, depth: cur.depth + 1})
			}
		}
	}
	return nil
}

// pathHasComponent returns true if any path component of p (split by
// the OS separator) equals name. Used so we match
// ".shortcut-targets-by-id" as a directory in the path even when it
// appears mid-string.
func pathHasComponent(p, name string) bool {
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if part == name {
			return true
		}
	}
	return false
}

// MaterializeSharedExcludesFile writes the merged exclude list (auto
// + manual) to a per-run file under configDir. Returns the file path.
// An empty list still writes an empty file so the caller can pass it
// to rsync via --exclude-from unconditionally.
func MaterializeSharedExcludesFile(configDir string, entries []SharedEntry) (string, error) {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("creating config dir %q: %w", configDir, err)
	}
	path := filepath.Join(configDir, "gdrive-sync-shared.dyn.conf")

	var b strings.Builder
	b.WriteString("# Auto-generated by `dot gdrive-sync` — do not edit.\n")
	b.WriteString("# Property-detected shortcuts + state.modules.gdrive_sync.shared_excludes.\n")
	for _, e := range entries {
		// rsync exclude syntax: `/path` matches at the source root.
		// Trailing slash makes it match a directory specifically.
		fmt.Fprintf(&b, "/%s\n", strings.TrimSuffix(e.RelPath, "/"))
		fmt.Fprintf(&b, "/%s/\n", strings.TrimSuffix(e.RelPath, "/"))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", fmt.Errorf("writing shared excludes %q: %w", path, err)
	}
	return path, nil
}
