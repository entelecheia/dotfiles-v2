package gdrivesync

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

// ConflictEntry summarizes one timestamped conflict directory under a
// tree's .sync-conflicts/ folder.
type ConflictEntry struct {
	Timestamp string
	Path      string // absolute path of the timestamp directory
	ModTime   time.Time
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
