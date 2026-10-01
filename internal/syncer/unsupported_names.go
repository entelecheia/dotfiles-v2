package syncer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// MoveMirrorLeftovers moves plan leftovers (mirror-only paths with a name
// Dropbox cannot store) into the workspace's .sync-conflicts/<ts>/from-mirror/
// and returns that directory. Callers gate it on PushPlan.MoveLeftovers: in
// Dropbox no cloud copy of these names can exist, and the workspace no longer
// has them. The backup sits outside the mirror so it does not keep an
// unstorable name in the provider folder. Leftovers have their own max_delete
// budget, apart from rsync's deletes (#225).
func MoveMirrorLeftovers(cfg *Config, rels []string) (string, error) {
	if len(rels) == 0 {
		return "", nil
	}
	if len(rels) > cfg.MaxDelete {
		return "", fmt.Errorf("%d mirror leftover(s) exceed max_delete %d; raise max_delete or move them out of %s by hand", len(rels), cfg.MaxDelete, cfg.MirrorPath)
	}
	mirror := strings.TrimRight(cfg.MirrorPath, "/")
	backup := filepath.Join(strings.TrimRight(cfg.LocalPath, "/"), NewConflictDir().LeftoverBackupRel())
	for _, rel := range rels {
		dst := filepath.Join(backup, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return backup, err
		}
		if err := moveFile(filepath.Join(mirror, rel), dst); err != nil {
			return backup, fmt.Errorf("moving mirror leftover %s: %w", rel, err)
		}
		pruneUnsupportedDirs(mirror, filepath.Dir(rel))
	}
	return backup, nil
}

// moveFile renames src to dst, copying then removing when they are on
// different volumes.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFilePreservingMtime(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// pruneUnsupportedDirs removes rel and its parents while each is an empty
// directory inside a name Dropbox cannot store (old./sub/ as well as old./).
// Finder's .DS_Store does not keep one alive.
func pruneUnsupportedDirs(mirror, rel string) {
	for rel != "." && rel != "" && UnsupportedPathName(rel) {
		abs := filepath.Join(mirror, rel)
		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		if len(entries) == 1 && entries[0].Name() == ".DS_Store" {
			_ = os.Remove(filepath.Join(abs, ".DS_Store"))
		} else if len(entries) > 0 {
			return
		}
		if os.Remove(abs) != nil {
			return
		}
		rel = filepath.Dir(rel)
	}
}

// mirrorUnderDropbox reports whether the mirror resolves inside a Dropbox
// root: a Dropbox* folder in Library/CloudStorage, or directly in a home
// directory (/Users/<name>, /home/<name>), the roots dot's cloud detection
// offers. A Dropbox folder anywhere else is not recognized, which leaves its
// leftovers listed rather than moved.
func mirrorUnderDropbox(mirror string) bool {
	p, err := filepath.EvalSymlinks(strings.TrimRight(mirror, "/"))
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i := 1; i < len(parts); i++ {
		if !strings.HasPrefix(parts[i], "Dropbox") {
			continue
		}
		if parts[i-1] == "CloudStorage" || (i >= 2 && (parts[i-2] == "Users" || parts[i-2] == "home")) {
			return true
		}
	}
	return false
}
