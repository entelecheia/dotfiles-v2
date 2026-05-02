package gdrivesync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// PullOptions controls one tracked pull run.
type PullOptions struct {
	DryRun bool
}

// PullConflict captures a baseline-tracked file that changed on both sides.
type PullConflict struct {
	RelPath    string
	LocalPath  string
	MirrorPath string
	BackupPath string
}

// PullResult summarizes one tracked pull run.
type PullResult struct {
	Pulled        []string
	Restored      []string
	LocalModified []string
	SkippedBase   []string
	Tombstones    []Tombstone
	Conflicts     []PullConflict
	DryRun        bool
}

// HasChanges reports whether the run did anything worth recording in state.
func (r *PullResult) HasChanges() bool {
	if r == nil {
		return false
	}
	return len(r.Pulled) > 0 || len(r.Restored) > 0 || len(r.Tombstones) > 0 || len(r.Conflicts) > 0
}

// PullTracked applies Drive-side changes only for paths already present in
// baseline.manifest. Baseline is the Git-shared Drive payload index; files not
// listed there are deliberately left for Intake to stage under inbox/gdrive.
func PullTracked(_ context.Context, _ *exec.Runner, cfg *Config, opts PullOptions) (*PullResult, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("pull: local paths unresolved")
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return nil, err
	}

	paths := cfg.LocalPaths
	local := strings.TrimRight(cfg.LocalPath, "/")
	mirror := strings.TrimRight(cfg.MirrorPath, "/")

	baseline, err := LoadBaselineManifest(paths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	filter, err := newSyncFilter(cfg, mirror)
	if err != nil {
		return nil, fmt.Errorf("loading filters: %w", err)
	}
	tracked := gitTrackedRelPaths(local)

	now := time.Now().UTC()
	conflict := NewConflictDir()
	result := &PullResult{DryRun: opts.DryRun}
	nextBaseline := make(map[string]Fingerprint, len(baseline))
	for rel, fp := range baseline {
		nextBaseline[rel] = fp
	}

	rels := make([]string, 0, len(baseline))
	for rel := range baseline {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	baselineChanged := false
	for _, rel := range rels {
		rel = normalizeRel(rel)
		if rel == "" || tracked[rel] {
			continue
		}
		base := baseline[rel]
		mirrorAbs := filepath.Join(mirror, rel)
		localAbs := filepath.Join(local, rel)

		mirrorInfo, err := os.Lstat(mirrorAbs)
		if errors.Is(err, fs.ErrNotExist) {
			result.Tombstones = append(result.Tombstones, Tombstone{
				RelPath: rel, BaselineFP: base, DetectedAt: now,
			})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat mirror %s: %w", rel, err)
		}
		if shouldSkipPullCandidate(filter, mirrorAbs, rel, mirrorInfo) {
			continue
		}

		mirrorFP, err := FingerprintFile(mirrorAbs, FingerprintStrict)
		if err != nil {
			return nil, fmt.Errorf("fingerprint mirror %s: %w", rel, err)
		}
		mirrorMatchesBase := FingerprintsCompatible(base, mirrorFP, mirrorAbs)

		localInfo, err := os.Lstat(localAbs)
		localMissing := errors.Is(err, fs.ErrNotExist)
		if err != nil && !localMissing {
			return nil, fmt.Errorf("stat local %s: %w", rel, err)
		}
		if localMissing {
			if err := pullCopy(mirrorAbs, localAbs, opts.DryRun); err != nil {
				return nil, fmt.Errorf("restore %s: %w", rel, err)
			}
			result.Restored = append(result.Restored, rel)
			result.Pulled = append(result.Pulled, rel)
			if needsBaselineUpdate(base, mirrorFP) {
				nextBaseline[rel] = mirrorFP
				baselineChanged = true
			}
			continue
		}
		if localInfo.IsDir() || localInfo.Mode()&os.ModeSymlink != 0 {
			backup, err := backupPullConflict(local, mirrorAbs, rel, conflict, opts.DryRun)
			if err != nil {
				return nil, fmt.Errorf("backup conflict %s: %w", rel, err)
			}
			result.Conflicts = append(result.Conflicts, PullConflict{
				RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs, BackupPath: backup,
			})
			continue
		}

		localFP, err := FingerprintFile(localAbs, FingerprintStrict)
		if err != nil {
			return nil, fmt.Errorf("fingerprint local %s: %w", rel, err)
		}
		localMatchesBase := FingerprintsCompatible(base, localFP, localAbs)

		switch {
		case mirrorMatchesBase && localMatchesBase:
			if needsBaselineUpdate(base, mirrorFP) {
				nextBaseline[rel] = mirrorFP
				baselineChanged = true
			}
			result.SkippedBase = append(result.SkippedBase, rel)
		case mirrorMatchesBase && !localMatchesBase:
			result.LocalModified = append(result.LocalModified, rel)
		case !mirrorMatchesBase && localMatchesBase:
			if err := pullCopy(mirrorAbs, localAbs, opts.DryRun); err != nil {
				return nil, fmt.Errorf("pull %s: %w", rel, err)
			}
			result.Pulled = append(result.Pulled, rel)
			nextBaseline[rel] = mirrorFP
			baselineChanged = true
		case fingerprintsSame(localFP, mirrorFP):
			nextBaseline[rel] = mirrorFP
			baselineChanged = true
			result.SkippedBase = append(result.SkippedBase, rel)
		default:
			backup, err := backupPullConflict(local, mirrorAbs, rel, conflict, opts.DryRun)
			if err != nil {
				return nil, fmt.Errorf("backup conflict %s: %w", rel, err)
			}
			result.Conflicts = append(result.Conflicts, PullConflict{
				RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs, BackupPath: backup,
			})
		}
	}

	sort.Strings(result.Pulled)
	sort.Strings(result.Restored)
	sort.Strings(result.LocalModified)
	sort.Strings(result.SkippedBase)
	sort.Slice(result.Tombstones, func(i, j int) bool { return result.Tombstones[i].RelPath < result.Tombstones[j].RelPath })
	sort.Slice(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].RelPath < result.Conflicts[j].RelPath })

	if opts.DryRun {
		return result, nil
	}
	if baselineChanged {
		if err := SaveBaselineManifest(paths.BaselineFile, nextBaseline); err != nil {
			return nil, fmt.Errorf("saving baseline: %w", err)
		}
	}
	if len(result.Tombstones) > 0 {
		if err := AppendTombstones(paths.TombstonesFile, result.Tombstones); err != nil {
			return nil, fmt.Errorf("appending tombstones: %w", err)
		}
	}
	if result.HasChanges() || baselineChanged {
		if err := UpdateLocalState(paths, func(s *LocalState) {
			s.LastPull = now
		}); err != nil {
			return nil, fmt.Errorf("updating local state: %w", err)
		}
	}
	return result, nil
}

func shouldSkipPullCandidate(filter *syncFilter, abs, rel string, info os.FileInfo) bool {
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if isDriveMetadata(rel) {
		return true
	}
	return filter.shouldSkip(abs, rel, false)
}

func pullCopy(src, dst string, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFilePreservingMtime(src, dst)
}

func backupPullConflict(localRoot, mirrorAbs, rel string, conflict *ConflictDir, dryRun bool) (string, error) {
	backup := filepath.Join(localRoot, conflict.PullBackupRel(), rel)
	if dryRun {
		return backup, nil
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		return "", err
	}
	return backup, copyFilePreservingMtime(mirrorAbs, backup)
}

func needsBaselineUpdate(base, current Fingerprint) bool {
	if !fingerprintsSame(base, current) {
		return true
	}
	return base.Sha == "" && current.Sha != ""
}

func fingerprintsSame(a, b Fingerprint) bool {
	if a.Sha != "" && b.Sha != "" {
		return a.Sha == b.Sha
	}
	return a.Size == b.Size && sameMtime(a.Mtime, b.Mtime)
}
