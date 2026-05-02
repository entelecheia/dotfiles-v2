package gdrivesync

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type syncFilter struct {
	root     string
	patterns []excludePattern
	gitCache map[string][]excludePattern
}

type excludePattern struct {
	raw  string
	base string
}

func newSyncFilter(cfg *Config, root string) (*syncFilter, error) {
	f := &syncFilter{
		root:     strings.TrimRight(root, "/"),
		gitCache: map[string][]excludePattern{},
	}
	for _, path := range []string{cfg.ExcludesFile, cfg.IgnoreFile} {
		patterns, err := loadExcludeFile(path, "")
		if err != nil {
			return nil, err
		}
		f.patterns = append(f.patterns, patterns...)
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
		f.patterns = append(f.patterns,
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

func (f *syncFilter) shouldSkip(absPath, rel string, isDir bool) bool {
	rel = normalizeRel(rel)
	if rel == "" || rel == "." {
		return false
	}
	if isAlwaysExcluded(rel) {
		return true
	}
	for _, p := range f.patterns {
		if p.matches(rel, isDir) {
			return true
		}
	}
	for _, p := range f.gitignorePatterns(absPath, rel) {
		if p.matches(rel, isDir) {
			return true
		}
	}
	return false
}

func (f *syncFilter) gitignorePatterns(absPath, rel string) []excludePattern {
	if f == nil || f.root == "" {
		return nil
	}
	dir := filepath.Dir(absPath)

	var dirs []string
	for {
		if !strings.HasPrefix(dir, f.root) {
			break
		}
		dirs = append(dirs, dir)
		if dir == f.root {
			break
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}

	var out []excludePattern
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		patterns, ok := f.gitCache[d]
		if !ok {
			base := ""
			if d != f.root {
				if relBase, err := filepath.Rel(f.root, d); err == nil {
					base = normalizeRel(relBase)
				}
			}
			patterns, _ = loadExcludeFile(filepath.Join(d, ".gitignore"), base)
			f.gitCache[d] = patterns
		}
		out = append(out, patterns...)
	}
	return out
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
