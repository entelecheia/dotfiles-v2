package syncer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

const (
	tombstoneListName     = "tombstones.list.dyn"
	tombstoneExcludesName = "tombstones.excludes.dyn"
)

// ComputeTombstones returns the relpaths that this machine deleted since the
// last successful push: present in baseline.manifest, absent from the local
// tree. Sorted, so callers and tests see a stable order.
//
// It must run BEFORE the pull. The pull uses --update, which recreates any
// path missing locally, so afterwards a deletion is indistinguishable from a
// file the peer holds and this machine never had.
//
// Only the local tree and the baseline are read, never the target. That is
// what lets this work against an SSH peer, whose tree cannot be walked from
// here.
//
// Two guards return an empty set rather than an error, so callers can invoke
// this unconditionally:
//
//   - Non-SSH targets. For a mirror the baseline records the MIRROR tree
//     (RefreshBaseline walks the mirror), so baseline-minus-local would be
//     nonsense. Only SSH profiles keep a local-tree baseline.
//   - propagation.delete off. Nothing may be removed on the target.
func ComputeTombstones(cfg *Config) ([]string, error) {
	if !cfg.Target.IsSSH() || !cfg.Propagation.Delete {
		return nil, nil
	}
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("compute tombstones: local paths unresolved")
	}
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	if len(baseline) == 0 {
		return nil, nil
	}

	local := strings.TrimRight(cfg.LocalPath, "/")
	// Same filter the baseline was built with, so a path that is merely
	// excluded never reads as deleted.
	filter, err := newSyncFilter(cfg, strings.TrimRight(cfg.MirrorPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("loading filters: %w", err)
	}

	present := map[string]bool{}
	err = filepath.WalkDir(local, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable root would present the whole tree as deleted.
			// Refuse rather than propagate that to the peer.
			if absPath == local {
				return err
			}
			fmt.Fprintf(os.Stderr, "warning: tombstone walk skipping %s: %v\n", absPath, err)
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
		if filter.shouldSkip(absPath, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			present[rel] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning local tree: %w", err)
	}

	var out []string
	for rel := range baseline {
		if present[rel] {
			continue
		}
		if filter.shouldSkip(filepath.Join(local, rel), rel, false) {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// checkTombstoneCap refuses a delete pass whose set exceeds MaxDelete. A
// failed mount or a broken filter presents the entire tree as deleted; the
// cap turns that into a refusal instead of a mass removal on the peer.
func checkTombstoneCap(cfg *Config, tombstones []string) error {
	if cfg.MaxDelete > 0 && len(tombstones) > cfg.MaxDelete {
		return fmt.Errorf(
			"refusing to propagate %d deletions: over max_delete=%d. "+
				"Inspect with `dot peer diff`; raise max_delete if this is genuine",
			len(tombstones), cfg.MaxDelete)
	}
	return nil
}

// writeTombstoneList writes the paths NUL-delimited for rsync --files-from
// --from0. This workspace has filenames with spaces and Korean text, which a
// newline-delimited list handles only by accident.
func writeTombstoneList(dir string, rels []string) (string, error) {
	path := filepath.Join(dir, tombstoneListName)
	var b strings.Builder
	for _, rel := range rels {
		b.WriteString(rel)
		b.WriteByte(0)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("writing tombstone list: %w", err)
	}
	return path, nil
}

// MaterializeTombstoneExcludesFile writes the pull's protective layer: one
// anchored exclude per tombstoned path, so the pull cannot restore what this
// machine just deleted. Always written, even empty, matching how the other
// runtime filter layers behave.
//
// Paths are anchored with a leading slash (relative to the transfer root) so
// `inbox/drop/x.csv` cannot also match `elsewhere/inbox/drop/x.csv`.
func MaterializeTombstoneExcludesFile(dir string, rels []string) (string, error) {
	path := filepath.Join(dir, tombstoneExcludesName)
	var b strings.Builder
	b.WriteString("# Auto-generated per run by `dot peer sync` - do not edit.\n")
	b.WriteString("# Paths deleted locally, protected from being pulled back.\n")
	for _, rel := range rels {
		b.WriteString("/" + rel + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("writing tombstone excludes: %w", err)
	}
	return path, nil
}

// deletePassArgs builds the rsync argv that removes exactly the listed paths
// from the target and quarantines them.
//
// --files-from scopes the run to the list, and --delete-missing-args removes
// those entries that are missing on the sender. Plain --delete is deliberately
// absent: it would remove every target path absent locally, including files
// the peer created that this machine has not pulled yet.
//
// --backup with --backup-dir moves each removal under .sync-conflicts/<ts>/
// on the target instead of unlinking it, preserving content and relative path
// until `dot sync conflicts prune` expires it.
func deletePassArgs(cfg *Config, conflict *ConflictDir, listFile string, dryRun bool) []string {
	args := []string{
		"-a",
		"--human-readable",
		"--stats",
		"--no-links",
		"--files-from=" + listFile,
		"--from0",
		"--delete-missing-args",
		"--backup",
		"--backup-dir=" + conflict.PushBackupRel(),
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, rsyncTransportArgs(cfg)...)
	args = append(args, cfg.LocalPath, cfg.Target.RsyncDest())
	return args
}

// PropagateDeletes removes tombstoned paths from the target, into quarantine.
//
// Runs after the pull and BEFORE the push. Push ends with RefreshBaseline,
// which walks the local tree and therefore drops these paths from the
// baseline; if the pass ran after that and failed, the tombstone evidence
// would be gone and the peer's copy would come back on the next pull. Failing
// here returns before the baseline is touched, so the deletion is retried on
// the next run.
//
// A successful pass needs no baseline surgery: the subsequent RefreshBaseline
// retires these keys on its own.
func PropagateDeletes(ctx context.Context, runner *exec.Runner, cfg *Config, conflict *ConflictDir, tombstones []string, dryRun bool) error {
	if len(tombstones) == 0 {
		return nil
	}
	if err := checkTombstoneCap(cfg, tombstones); err != nil {
		return err
	}
	if cfg.LocalPaths == nil {
		return fmt.Errorf("propagate deletes: local paths unresolved")
	}
	listFile, err := writeTombstoneList(cfg.ConfigDir, tombstones)
	if err != nil {
		return err
	}
	args := deletePassArgs(cfg, conflict, listFile, dryRun)
	fmt.Printf("  Delete: %d path(s) → %s (quarantined under %s)\n",
		len(tombstones), cfg.Target.RsyncDest(), conflictsDirName)
	if err := runRsync(ctx, runner, cfg, args); err != nil {
		// Every path in the list is missing from the sender by construction -
		// that is what marks it for deletion. rsync reports absent source args
		// as a partial transfer (exit 23/24), so for this pass that code is the
		// success signal, not a failure. Any other error is real.
		if !IsPartialTransfer(err) {
			return err
		}
	}
	if dryRun {
		return nil
	}
	// Audit trail only. Nothing reads this back; it is the sole local record
	// of a destructive change made on the other machine. The baseline is
	// re-read so each row carries the fingerprint of what was removed - the
	// push that follows retires those keys.
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return fmt.Errorf("loading baseline for audit record: %w", err)
	}
	entries := make([]Tombstone, 0, len(tombstones))
	now := time.Now().UTC()
	for _, rel := range tombstones {
		entries = append(entries, Tombstone{RelPath: rel, BaselineFP: baseline[rel], DetectedAt: now})
	}
	if err := AppendTombstones(cfg.LocalPaths.TombstonesFile, entries); err != nil {
		return fmt.Errorf("recording tombstones: %w", err)
	}
	return nil
}
