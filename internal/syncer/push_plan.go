package syncer

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
	// Placeholders counts mirror files whose content lives only in the
	// provider's cloud. They are reported because a mirror that is mostly
	// placeholders explains conflicts an operator cannot otherwise see.
	Placeholders int
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
	// dehydrated marks the files above that are cloud placeholders, probed
	// once during the walk so the plan never pays for a second stat.
	dehydrated map[string]bool
}

func PlanPush(cfg *Config) (*PushPlan, error) {
	if cfg.LocalPaths == nil {
		return nil, fmt.Errorf("push plan: local paths unresolved")
	}
	if cfg.Target.IsSSH() {
		return nil, fmt.Errorf("push plan requires a local target; ssh targets push directly")
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
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("loading baseline: %w", err)
	}
	localInv, err := collectPlanInventory(local, filter, FingerprintFast)
	if err != nil {
		return nil, fmt.Errorf("scanning local: %w", err)
	}
	mirrorInv, err := collectPlanInventory(mirror, filter, FingerprintFast)
	if err != nil {
		return nil, fmt.Errorf("scanning mirror: %w", err)
	}

	plan := &PushPlan{Propagation: cfg.Propagation, Placeholders: len(mirrorInv.dehydrated)}
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
				// Deleting here is destructive and the baseline cannot prove
				// the path came from local, so this stays a conflict whether
				// or not the mirror copy is a placeholder. A placeholder is
				// only named so the operator can tell an evicted file from a
				// mirror that genuinely diverged.
				reason := "mirror-only file is not in baseline"
				if mirrorInv.dehydrated[rel] {
					reason = "mirror-only cloud placeholder is not in baseline"
				}
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: reason,
				})
				continue
			}
			if !FingerprintsCompatible(base, mirrorFP, mirrorAbs) {
				// A placeholder never matches its baseline entry, but the
				// verdict here is a deletion, so it keeps the conservative
				// answer and only says which of the two it is.
				reason := "mirror changed after baseline while local deleted"
				if mirrorInv.dehydrated[rel] {
					reason = "mirror is a cloud placeholder and local deleted the file"
				}
				plan.Conflicts = append(plan.Conflicts, PushConflict{
					RelPath: rel, LocalPath: localAbs, MirrorPath: mirrorAbs,
					Reason: reason,
				})
				continue
			}
			plan.Deletes = append(plan.Deletes, rel)
		case localOK && mirrorOK:
			// An evicted twin carries no usable fingerprint, so it is
			// classified before any comparison below - including the
			// equal-fast-fingerprint shortcut, which an empty local file can
			// otherwise take against a stub and skip the transfer entirely.
			// The mirror is a derived copy, so local content rehydrates it.
			if mirrorInv.dehydrated[rel] {
				if !cfg.Propagation.Update {
					plan.SkippedPolicy = append(plan.SkippedPolicy, rel)
					continue
				}
				plan.Updates = append(plan.Updates, rel)
				continue
			}
			if fingerprintsSame(localFP, mirrorFP) {
				base, ok := baseline[rel]
				if !ok || fingerprintsSameFast(base, localFP) {
					continue
				}
				localFP, err = FingerprintFile(localAbs, FingerprintStrict)
				if err != nil {
					return nil, fmt.Errorf("fingerprinting local: %w", err)
				}
				mirrorFP, err = FingerprintFile(mirrorAbs, FingerprintStrict)
				if err != nil {
					return nil, fmt.Errorf("fingerprinting mirror: %w", err)
				}
				if fingerprintsSame(localFP, mirrorFP) {
					continue
				}
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

func collectPlanInventory(root string, filter *syncFilter, mode FingerprintMode) (*planInventory, error) {
	inv := &planInventory{
		files:      map[string]Fingerprint{},
		nonFile:    map[string]string{},
		dehydrated: map[string]bool{},
	}
	err := filepath.WalkDir(root, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable root: abort (an empty inventory would misplan the
			// whole tree). Deeper failures — cloud-placeholder dirs timing
			// out under load — skip the subtree; affected files replan as
			// creates/updates on a later cycle.
			if absPath == root {
				return err
			}
			fmt.Fprintf(os.Stderr, "warning: plan walk skipping %s: %v\n", absPath, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if absPath == root {
			return nil
		}
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
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
		fp, err := FingerprintFile(absPath, mode)
		if err != nil {
			return err
		}
		inv.files[rel] = fp
		if dehydratedFile(absPath, info) {
			inv.dehydrated[rel] = true
		}
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
