package gdrivesync

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type syncFilter struct {
	mode            FilterMode
	excludePatterns []excludePattern
	includePatterns []excludePattern
}

type excludePattern struct {
	raw  string
	base string
}

func newSyncFilter(cfg *Config, _ string) (*syncFilter, error) {
	f := &syncFilter{mode: normalizeFilterMode(cfg.FilterMode)}
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

func (f *syncFilter) shouldSkip(_ string, rel string, isDir bool) bool {
	rel = normalizeRel(rel)
	if rel == "" || rel == "." {
		return false
	}
	if isAlwaysExcluded(rel) {
		return true
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
	lowerRel := strings.ToLower(rel)
	for _, p := range f.includePatterns {
		if p.matches(lowerRel, false) {
			return false
		}
	}
	return true
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
