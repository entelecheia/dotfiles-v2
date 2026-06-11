package gsync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// conflictsDirName is the directory under each tree where rsync --backup
// stashes overwritten/deleted files for later inspection.
const conflictsDirName = ".sync-conflicts"

// Backup subdirectory names under <ts>/, naming the source side that
// caused the overwrite. `from-gdrive` lands inside the workspace tree
// (pull pass); `from-workspace` lands inside the mirror tree (push pass).
const (
	backupSubFromGdrive    = "from-gdrive"
	backupSubFromWorkspace = "from-workspace"
)

// ConflictDir holds the timestamp shared between the pull and push passes
// of a single Sync invocation. Pull-only or push-only invocations mint
// their own.
type ConflictDir struct {
	Timestamp string
}

// NewConflictDir mints a fresh filesystem-safe RFC3339 timestamp.
func NewConflictDir() *ConflictDir {
	return &ConflictDir{Timestamp: newTimestamp()}
}

// PullBackupRel returns the --backup-dir argument for the pull pass,
// relative to the destination (workspace).
func (c *ConflictDir) PullBackupRel() string {
	return filepath.Join(conflictsDirName, c.Timestamp, backupSubFromGdrive)
}

// PushBackupRel returns the --backup-dir argument for the push pass,
// relative to the destination (mirror).
func (c *ConflictDir) PushBackupRel() string {
	return filepath.Join(conflictsDirName, c.Timestamp, backupSubFromWorkspace)
}

// PullLocalBackupRel returns the backup path for a pull that overwrites a
// local file after the operator selected source-wins semantics.
func (c *ConflictDir) PullLocalBackupRel() string {
	return filepath.Join(conflictsDirName, c.Timestamp, backupSubFromWorkspace)
}

// ConflictEntry summarizes one timestamped conflict directory under a
// tree's .sync-conflicts/ folder.
type ConflictEntry struct {
	Timestamp string
	Path      string // absolute path of the timestamp directory
	ModTime   time.Time
}

// PrunedEntry is one conflict directory selected for removal, with its
// on-disk size captured before deletion so dry-run and real runs report
// identical numbers.
type PrunedEntry struct {
	ConflictEntry
	Size int64
}

// PruneResult summarizes one PruneConflicts pass over a single tree.
type PruneResult struct {
	Root      string        // <treeRoot>/.sync-conflicts
	Pruned    []PrunedEntry // removed (or would be, in dry-run), oldest-first
	Kept      int           // entries at/after the cutoff
	Reclaimed int64         // bytes across Pruned
	DryRun    bool
}

// PruneConflicts deletes timestamped backup directories under
// <treeRoot>/.sync-conflicts/ whose ModTime is before olderThan. dryRun
// computes the identical plan (entries + sizes) without removing anything.
// A missing conflicts root yields an empty result, not an error. When the
// pass empties the conflicts root entirely, the root itself is removed.
func PruneConflicts(treeRoot string, olderThan time.Time, dryRun bool) (*PruneResult, error) {
	root := filepath.Join(treeRoot, conflictsDirName)
	res := &PruneResult{Root: root, DryRun: dryRun}

	entries, err := ListConflicts(treeRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.ModTime.Before(olderThan) {
			res.Kept++
			continue
		}
		size := conflictDirSize(entry.Path)
		res.Reclaimed += size
		res.Pruned = append(res.Pruned, PrunedEntry{ConflictEntry: entry, Size: size})
		if dryRun {
			continue
		}
		if err := os.RemoveAll(entry.Path); err != nil {
			return nil, fmt.Errorf("pruning %s: %w", entry.Path, err)
		}
	}

	// Best-effort: drop the now-empty root. Fails silently when stray
	// files remain beside the timestamped dirs, which is correct.
	if !dryRun && len(res.Pruned) > 0 && res.Kept == 0 {
		_ = os.Remove(root)
	}
	return res, nil
}

// conflictDirSize sums regular-file sizes under dir, best-effort: entries
// vanishing mid-walk are skipped rather than failing the prune plan.
func conflictDirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort sizing
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ListConflicts enumerates timestamped backup directories under
// <treeRoot>/.sync-conflicts/, sorted oldest-first. Returns an empty
// slice (not error) if the conflicts root doesn't exist.
func ListConflicts(treeRoot string) ([]ConflictEntry, error) {
	root := filepath.Join(treeRoot, conflictsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}
	out := make([]ConflictEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, ConflictEntry{
			Timestamp: e.Name(),
			Path:      filepath.Join(root, e.Name()),
			ModTime:   info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.Before(out[j].ModTime)
	})
	return out, nil
}
