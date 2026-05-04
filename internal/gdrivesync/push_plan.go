package gdrivesync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PushConflict struct {
	RelPath    string
	LocalPath  string
	MirrorPath string
	Reason     string
}

type PushPlan struct {
	Creates       []string
	Updates       []string
	Deletes       []string
	SkippedPolicy []string
	Conflicts     []PushConflict
	Propagation   PropagationPolicy
}

func (p *PushPlan) HasChanges() bool {
	if p == nil {
		return false
	}
	return len(p.Creates) > 0 || len(p.Updates) > 0 || len(p.Deletes) > 0
}

func (p *PushPlan) HasConflicts() bool {
	return p != nil && len(p.Conflicts) > 0
}

type planInventory struct {
	files   map[string]Fingerprint
	nonFile map[string]string
}

func PlanPush(cfg *Config) (*PushPlan, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("push plan: local paths unresolved")
	}
	if err := cfg.Propagation.Validate(); err != nil {
		return nil, fmt.Errorf("push refused: %w", err)
	}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		return nil, err
	}

	local := strings.TrimRight(cfg.LocalPath, "/")
	mirror := strings.TrimRight(cfg.MirrorPath, "/")
	filter, err := newSyncFilter(cfg, mirror)
	if err != nil {
		return nil, fmt.Errorf("loading filters: %w", err)
	}
	tracked := gitTrackedRelPaths(local)
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	localInv, err := collectPlanInventory(local, filter, tracked)
	if err != nil {
		return nil, fmt.Errorf("scanning local: %w", err)
	}
	mirrorInv, err := collectPlanInventory(mirror, filter, tracked)
	if err != nil {
		return nil, fmt.Errorf("scanning mirror: %w", err)
	}

	plan := &PushPlan{Propagation: cfg.Propagation}
	rels := unionKeys(localInv.files, mirrorInv.files)
	for _, rel := range rels {
		localFP, localOK := localInv.files[rel]
		mirrorFP, mirrorOK := mirrorInv.files[rel]
		localAbs := filepath.Join(local, rel)
		mirrorAbs := filepath.Join(mirror, rel)

		if localOK {
			if kind := mirrorInv.nonFile[rel]; kind != "" {
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: "mirror has non-file entry: " + kind,
				})
				continue
			}
		}
		if mirrorOK {
			if kind := localInv.nonFile[rel]; kind != "" {
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: "local has non-file entry: " + kind,
				})
				continue
			}
		}

		switch {
		case localOK && !mirrorOK:
			if cfg.Propagation.Create {
				plan.Creates = append(plan.Creates, rel)
			} else {
				plan.SkippedPolicy = append(plan.SkippedPolicy, rel)
			}
		case !localOK && mirrorOK:
			if !cfg.Propagation.Delete {
				plan.SkippedPolicy = append(plan.SkippedPolicy, rel)
				continue
			}
			base, ok := baseline[rel]
			if !ok {
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: "mirror-only file is not in baseline",
				})
				continue
			}
			if !FingerprintsCompatible(base, mirrorFP, mirrorAbs) {
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: "mirror changed after baseline while local deleted",
				})
				continue
			}
			plan.Deletes = append(plan.Deletes, rel)
		case localOK && mirrorOK:
			if fingerprintsSame(localFP, mirrorFP) {
				continue
			}
			if !cfg.Propagation.Update {
				plan.SkippedPolicy = append(plan.SkippedPolicy, rel)
				continue
			}
			base, ok := baseline[rel]
			if !ok {
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: "local and mirror differ without a baseline",
				})
				continue
			}
			localMatchesBase := FingerprintsCompatible(base, localFP, localAbs)
			mirrorMatchesBase := FingerprintsCompatible(base, mirrorFP, mirrorAbs)
			if mirrorMatchesBase && !localMatchesBase {
				plan.Updates = append(plan.Updates, rel)
				continue
			}
			plan.Conflicts = append(plan.Conflicts, PushConflict{
				RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
				Reason: "mirror changed after baseline",
			})
		}
	}

	sort.Strings(plan.Creates)
	sort.Strings(plan.Updates)
	sort.Strings(plan.Deletes)
	sort.Strings(plan.SkippedPolicy)
	sort.Slice(plan.Conflicts, func(i, j int) bool { return plan.Conflicts[i].RelPath < plan.Conflicts[j].RelPath })
	return plan, nil
}

func collectPlanInventory(root string, filter *syncFilter, tracked map[string]bool) (*planInventory, error) {
	inv := &planInventory{
		files:   map[string]Fingerprint{},
		nonFile: map[string]string{},
	}
	err := filepath.WalkDir(root, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if absPath == root {
			return nil
		}
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
		if tracked[rel] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		isDir := info.IsDir()
		if filter.shouldSkip(absPath, rel, isDir) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir {
			inv.nonFile[rel] = "directory"
			return nil
		}
		if isDriveMetadata(rel) {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			inv.nonFile[rel] = "symlink"
			return nil
		}
		if !info.Mode().IsRegular() {
			inv.nonFile[rel] = info.Mode().String()
			return nil
		}
		fp, err := FingerprintFile(absPath, FingerprintStrict)
		if err != nil {
			return err
		}
		inv.files[rel] = fp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}

func unionKeys(a, b map[string]Fingerprint) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
