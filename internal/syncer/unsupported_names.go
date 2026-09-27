package syncer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dropbox cannot store a file or folder whose name ends with a space (or a
// period: both are Windows-incompatible name endings it refuses). On upload it
// renames the entry in place to "<name> (Unicode Encoding Conflict)", and that
// renamed twin is mirror-only and absent from the baseline, so the next push
// plan reports conflicts and a clean push refuses in a loop. Catching these
// names before upload — in the plan and in the rsync filter — is what breaks
// that loop. The Go predicate and the rsync patterns below are twins and must
// stay side by side.

// UnsupportedPathName reports whether any slash-separated segment of a
// sync-set relative path ends with a space or a period. A trailing dot is
// treated the same as a trailing space: both are Windows-incompatible name
// endings Dropbox refuses to store.
func UnsupportedPathName(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			return true
		}
	}
	return false
}

// unsupportedNameExcludeArgs renders the rsync twin of UnsupportedPathName
// for the push transfer. A pattern without a slash matches the leaf name at
// any depth, and excluding a directory leaf prunes its whole subtree, so
// these two rules also cover files parked under a trailing-space directory.
// SSH peer targets are unaffected: the peer runs its own baseline-scoped plan
// and must not silently drop names the other machine can hold.
func unsupportedNameExcludeArgs(cfg *Config) []string {
	if cfg == nil || cfg.Target.IsSSH() {
		return nil
	}
	return []string{"--exclude=* ", "--exclude=*."}
}

// CountUnsupportedNames is the status-side probe: a name-only walk of the
// local tree honoring the same skip rules as the plan walk (sync filter, hard
// excludes, symlinks, drive metadata), without paying for fingerprints. Files
// and directories both count — an empty trailing-space directory cannot be
// stored either — and every descendant of an unsupported directory counts on
// its own, matching how the plan flags each sync-set path individually.
func CountUnsupportedNames(cfg *Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("unsupported name count: nil sync config")
	}
	local := strings.TrimRight(cfg.LocalPath, "/")
	if local == "" {
		return 0, nil
	}
	filter, err := newSyncFilter(cfg, strings.TrimRight(cfg.MirrorPath, "/"))
	if err != nil {
		return 0, fmt.Errorf("loading filters: %w", err)
	}
	count := 0
	err = filepath.WalkDir(local, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if absPath == local {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if absPath == local {
			return nil
		}
		rel, err := filepath.Rel(local, absPath)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
		isDir := d.IsDir()
		if filter.shouldSkip(absPath, rel, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !isDir && isDriveMetadata(rel) {
			return nil
		}
		if UnsupportedPathName(rel) {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
