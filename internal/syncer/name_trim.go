package syncer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// NameTrimPreflightError reports every blocker found while planning a
// trailing-whitespace trim. It mirrors NameNormalizationPreflightError but
// carries trim wording so an operator is not told about "NFD collisions"
// while running `dot sync names trim`.
type NameTrimPreflightError struct {
	InvalidUTF8  []string
	EmptyTargets []string // names made only of trailing whitespace; trimming leaves nothing
	Collisions   []NameNormalizationCollision
}

func (e *NameTrimPreflightError) Error() string {
	if e == nil {
		return "trailing-whitespace name trim preflight failed"
	}
	var parts []string
	if len(e.InvalidUTF8) > 0 {
		parts = append(parts, fmt.Sprintf("%d invalid UTF-8 name(s)%s", len(e.InvalidUTF8), formatNameDetails(e.InvalidUTF8)))
	}
	if len(e.EmptyTargets) > 0 {
		parts = append(parts, fmt.Sprintf("%d name(s) made only of whitespace%s", len(e.EmptyTargets), formatNameDetails(e.EmptyTargets)))
	}
	if len(e.Collisions) > 0 {
		parts = append(parts, fmt.Sprintf("%d trim collision(s)%s", len(e.Collisions), formatCollisionDetails(e.Collisions)))
	}
	if len(parts) == 0 {
		return "trailing-whitespace name trim preflight failed"
	}
	return "trailing-whitespace name trim preflight failed: " + strings.Join(parts, "; ")
}

// trimNameTrailingWhitespace is the only mapping `names trim` performs:
// trailing spaces and tabs come off, everything else about the name stays.
func trimNameTrailingWhitespace(name string) string {
	return strings.TrimRight(name, " \t")
}

func trimRelTrailingWhitespace(rel string) string {
	segments := strings.Split(rel, "/")
	for i, segment := range segments {
		segments[i] = trimNameTrailingWhitespace(segment)
	}
	return strings.Join(segments, "/")
}

// PlanWorkspaceNameTrim performs the complete read-only preflight for a
// trailing-whitespace trim. The walk rules match PlanWorkspaceNameNormalization:
// the sync profile's effective filters and the hard safety paths are honored,
// symlinks are never followed or renamed, and invalid UTF-8 names are
// collected into the preflight rather than failing the walk.
func PlanWorkspaceNameTrim(cfg *Config) (*NameNormalizationPlan, error) {
	if cfg == nil {
		return nil, fmt.Errorf("trailing-whitespace name trim: nil sync config")
	}
	root, err := configWorkspaceRoot(cfg)
	if err != nil {
		return nil, err
	}

	filter, err := newSyncFilter(cfg, strings.TrimRight(cfg.MirrorPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("loading sync filters for name trim: %w", err)
	}

	plan := &NameNormalizationPlan{WorkspaceRoot: root}
	var invalid []string
	var emptyTargets []string
	var collisions []NameNormalizationCollision
	collisionKeys := map[string]struct{}{}
	// Sibling names are collected before filter pruning. An excluded sibling
	// still occupies its trimmed destination and must block an overwrite, and
	// an untouched sibling is what a trimmed name most often collides with.
	siblings := map[string]map[string][]string{}

	err = filepath.WalkDir(root, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walking %s: %w", absPath, walkErr)
		}
		if d == nil {
			return fmt.Errorf("walking %s: missing directory entry", absPath)
		}
		if absPath == root {
			return nil
		}

		name := d.Name()
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", absPath, err)
		}
		rel = filepath.ToSlash(rel)
		if !utf8.ValidString(name) {
			invalid = append(invalid, rel)
			// Continue the scan so the error reports every invalid name. There
			// is no safe trimmed key for this component.
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		parent := filepath.Dir(absPath)
		trimmedName := trimNameTrailingWhitespace(name)
		if byTarget, ok := siblings[parent]; ok {
			if existing := byTarget[trimmedName]; len(existing) > 0 {
				duplicate := false
				for _, prior := range existing {
					if prior == name {
						duplicate = true
						break
					}
				}
				if !duplicate {
					sort.Strings(existing)
					names := append(append([]string(nil), existing...), name)
					sort.Strings(names)
					dirRel, _ := filepath.Rel(root, parent)
					dirRel = filepath.ToSlash(dirRel)
					if dirRel == "." {
						dirRel = ""
					}
					key := dirRel + "\x00" + trimmedName
					if _, seen := collisionKeys[key]; !seen {
						collisionKeys[key] = struct{}{}
						collisions = append(collisions, NameNormalizationCollision{
							Directory: dirRel,
							Target:    trimmedName,
							Names:     names,
						})
					}
				}
			}
			byTarget[trimmedName] = append(byTarget[trimmedName], name)
		} else {
			siblings[parent] = map[string][]string{trimmedName: {name}}
		}

		// Never follow or rename links. Checking the DirEntry type before Info
		// also handles dangling links without turning them into walk errors.
		if d.Type()&os.ModeSymlink != 0 {
			plan.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", absPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			plan.Skipped++
			return nil
		}
		isDir := info.IsDir()

		if isNFDHardExcluded(rel) || filter.shouldSkip(absPath, rel, isDir) {
			plan.Skipped++
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}

		// A whitespace-only name has no safe trim target, but only inside the
		// sync set: a filtered-out entry is irrelevant to the transfer and must
		// not abort the trim (codex P2 on #144).
		if trimmedName == "" {
			emptyTargets = append(emptyTargets, rel)
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}

		if trimmedName == name {
			return nil
		}
		newAbs := filepath.Join(filepath.Dir(absPath), trimmedName)
		// NewRel reports the final workspace-relative spelling, including any
		// parent components that will be renamed later in this deepest-first
		// plan; NewPath keeps the current parent spelling so the child move can
		// happen before that parent directory moves.
		newRel := trimRelTrailingWhitespace(rel)
		plan.Renames = append(plan.Renames, NameRename{
			OldPath: absPath,
			NewPath: newAbs,
			OldRel:  rel,
			NewRel:  newRel,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(invalid) > 0 || len(emptyTargets) > 0 || len(collisions) > 0 {
		sort.Strings(invalid)
		sort.Strings(emptyTargets)
		sort.Slice(collisions, func(i, j int) bool {
			if collisions[i].Directory != collisions[j].Directory {
				return collisions[i].Directory < collisions[j].Directory
			}
			return collisions[i].Target < collisions[j].Target
		})
		return nil, &NameTrimPreflightError{InvalidUTF8: invalid, EmptyTargets: emptyTargets, Collisions: collisions}
	}

	// Deeper paths must move before their parent directory. For equal-depth
	// entries, lexical order keeps plans stable and output reproducible.
	sort.Slice(plan.Renames, func(i, j int) bool {
		depthI := strings.Count(plan.Renames[i].OldRel, "/")
		depthJ := strings.Count(plan.Renames[j].OldRel, "/")
		if depthI != depthJ {
			return depthI > depthJ
		}
		return plan.Renames[i].OldRel < plan.Renames[j].OldRel
	})
	return plan, nil
}

// TrimWorkspaceNames applies a complete trim plan. A dry run performs the same
// preflight and returns the plan without renaming. Any mutation failure rolls
// back already completed moves in reverse order. Unlike NFD normalization
// there is no migration marker: a trim is idempotent hygiene, not an opt-in
// boundary a push must check.
func TrimWorkspaceNames(cfg *Config, dryRun bool) (*NameNormalizationResult, error) {
	plan, err := PlanWorkspaceNameTrim(cfg)
	if err != nil {
		return nil, err
	}
	result := &NameNormalizationResult{Plan: plan, DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	if err := applyNameNormalizationPlan(plan); err != nil {
		return nil, err
	}
	result.Applied = len(plan.Renames)
	return result, nil
}
