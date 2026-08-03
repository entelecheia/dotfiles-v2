package syncer

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type syncFilter struct {
	mode            FilterMode
	submodules      []string         // sorted relpaths — excluded wholesale, synced via Git
	allowPatterns   []excludePattern // allow.txt + env-template builtins — win over every exclude
	allowDirs       map[string]bool  // literal parent dirs of anchored allow patterns
	secretPatterns  []excludePattern // deny-by-default secrets layer
	excludePatterns []excludePattern
	tracked         map[string]bool // tracked ∪ baseline — include layer
	includePatterns []excludePattern
}

type excludePattern struct {
	raw  string
	base string
}

// newSyncFilter builds the Go-side twin of commonArgs' rsync filter chain.
// The two MUST agree layer for layer — PlanPush previews what rsync will do.
func newSyncFilter(cfg *Config, _ string) (*syncFilter, error) {
	f := &syncFilter{mode: normalizeFilterMode(cfg.FilterMode)}

	local := strings.TrimRight(cfg.LocalPath, "/")
	f.submodules = gitSubmodulePaths(local)
	if cfg.IncludeSubmodules {
		f.submodules = nil
	}

	f.allowDirs = map[string]bool{}
	for _, p := range cfg.AllowPatterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		for _, dir := range allowParentDirs(p) {
			f.allowDirs[normalizeRel(dir)] = true
		}
		f.allowPatterns = append(f.allowPatterns, excludePattern{raw: p})
	}
	for _, p := range secretAllowBuiltins {
		f.allowPatterns = append(f.allowPatterns, excludePattern{raw: p})
	}
	for _, p := range secretExcludePatterns {
		f.secretPatterns = append(f.secretPatterns, excludePattern{raw: p})
	}

	for _, path := range []string{cfg.ExcludesFile, cfg.IgnoreFile} {
		patterns, err := loadExcludeFile(path, "")
		if err != nil {
			return nil, err
		}
		f.excludePatterns = append(f.excludePatterns, patterns...)
	}

	if f.mode == FilterModeInclude {
		patterns := cfg.IncludePatterns
		if len(patterns) == 0 {
			var err error
			patterns, err = loadPatternFileOrDefault(cfg.IncludeFile, LoadDefaultIncludePatterns)
			if err != nil {
				return nil, err
			}
		}
		for _, p := range patterns {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			f.includePatterns = append(f.includePatterns, excludePattern{raw: strings.ToLower(p)})
		}
		// Tracked ∪ baseline include layer (mirrors tracked-includes.dyn.conf).
		f.tracked = gitTrackedForSync(local)
		if cfg.LocalPaths != nil {
			if baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile); err == nil {
				for rel := range baseline {
					f.tracked[rel] = true
				}
			}
		}
	}

	shared, err := ScanShared(strings.TrimRight(cfg.MirrorPath, "/"), cfg.SharedExcludes)
	if err != nil {
		return nil, err
	}
	for _, e := range shared {
		rel := normalizeRel(e.RelPath)
		if rel == "" {
			continue
		}
		f.excludePatterns = append(f.excludePatterns,
			excludePattern{raw: "/" + rel},
			excludePattern{raw: "/" + rel + "/"},
		)
	}
	return f, nil
}

func loadExcludeFile(path, base string) ([]excludePattern, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []excludePattern
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, excludePattern{raw: line, base: base})
	}
	return out, sc.Err()
}

// shouldSkip mirrors the rsync filter chain in commonArgs, layer for layer:
// always-excluded state paths, submodules, allow re-includes, secrets
// deny-by-default, static/shared excludes, then (include mode) directory
// traversal, tracked ∪ baseline includes, the binary allowlist, and the
// final catch-all.
func (f *syncFilter) shouldSkip(_ string, rel string, isDir bool) bool {
	rel = normalizeRel(rel)
	if rel == "" || rel == "." {
		return false
	}
	if isAlwaysExcluded(rel) {
		return true
	}
	for _, sub := range f.submodules {
		if rel == sub || strings.HasPrefix(rel, sub+"/") {
			return true
		}
	}
	// Allow layer: explicit re-includes beat everything below, and their
	// literal parent dirs stay traversable even when a later layer would
	// exclude them (matching the rsync parent-dir includes).
	if isDir && f.allowDirs[rel] {
		return false
	}
	for _, p := range f.allowPatterns {
		if p.matches(rel, isDir) {
			return false
		}
	}
	for _, p := range f.secretPatterns {
		if p.matches(rel, isDir) {
			return true
		}
	}
	for _, p := range f.excludePatterns {
		if p.matches(rel, isDir) {
			return true
		}
	}
	if f.mode == FilterModeExclude {
		return false
	}
	if isDir {
		return false
	}
	if f.tracked[rel] {
		return false
	}
	lowerRel := strings.ToLower(rel)
	for _, p := range f.includePatterns {
		if p.matches(lowerRel, false) {
			return false
		}
	}
	return true
}

// shouldSkipFileOrAncestor checks a file together with every directory rsync
// must traverse to reach it. A directory-only rule such as /archive/ does not
// match archive/old.bin when evaluated as a file, but rsync never visits that
// file because the parent directory was excluded first.
func (f *syncFilter) shouldSkipFileOrAncestor(rel string) bool {
	rel = normalizeRel(rel)
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if f.shouldSkip("", strings.Join(parts[:i], "/"), true) {
			return true
		}
	}
	return f.shouldSkip("", rel, false)
}

func (p excludePattern) matches(rel string, isDir bool) bool {
	rel = normalizeRel(rel)
	base := normalizeRel(p.base)
	subRel := rel
	if base != "" {
		if rel == base {
			subRel = ""
		} else if strings.HasPrefix(rel, base+"/") {
			subRel = strings.TrimPrefix(rel, base+"/")
		} else {
			return false
		}
	}

	raw := strings.TrimSpace(filepath.ToSlash(p.raw))
	if raw == "" {
		return false
	}
	anchored := strings.HasPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "/")
	dirOnly := strings.HasSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		return false
	}
	if dirOnly && !isDir {
		return false
	}
	if strings.HasSuffix(raw, "/**") {
		prefix := strings.TrimSuffix(raw, "/**")
		return subRel == prefix || strings.HasPrefix(subRel, prefix+"/")
	}

	candidates := []string{subRel}
	if !anchored && !strings.Contains(raw, "/") {
		candidates = strings.Split(subRel, "/")
	}
	for _, c := range candidates {
		if c == raw {
			return true
		}
		if ok, _ := filepath.Match(raw, c); ok {
			return true
		}
	}
	if !anchored && strings.Contains(raw, "/") {
		return strings.HasSuffix(subRel, "/"+raw)
	}
	return false
}

func normalizeRel(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.Trim(rel, "/")
	if rel == "." {
		return ""
	}
	return rel
}
