// Package gsync implements the local→mirror rsync flow that backs
// `dot gsync`. Git owns tracked source files, baseline.manifest is the
// Git-shared Drive payload index for untracked artifacts, and push propagates
// local artifact creates/updates by default while deletes are opt-in.
package gsync

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// excludesTemplatePath is the path inside the embedded templates FS.
const excludesTemplatePath = "gsync/excludes.txt"

// includesTemplatePath is the path inside the embedded templates FS.
const includesTemplatePath = "gsync/includes.txt"

// excludesDiskName is the on-disk filename for the materialized excludes
// file (rsync needs a real file path for --exclude-from).
const excludesDiskName = "gdrive-sync-excludes.conf"

// includesDiskName is the on-disk filename for materialized default includes.
const includesDiskName = "gdrive-sync-includes.conf"

// MaterializeExcludesFile writes the embedded excludes to disk under the
// dotfiles config dir and returns its path. Callers pass the path to
// rsync via --exclude-from. Idempotent: overwrites if content differs.
func MaterializeExcludesFile(configDir string) (string, error) {
	engine := template.NewEngine()
	content, err := engine.ReadStatic(excludesTemplatePath)
	if err != nil {
		return "", fmt.Errorf("reading embedded excludes: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("creating config dir %q: %w", configDir, err)
	}
	path := filepath.Join(configDir, excludesDiskName)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return path, nil
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return "", fmt.Errorf("writing excludes file %q: %w", path, err)
	}
	return path, nil
}

// MaterializeIncludesFile writes the embedded default include list to disk.
// It is mainly used by tests and callers that need a concrete include file
// outside the per-workspace store.
func MaterializeIncludesFile(configDir string) (string, error) {
	engine := template.NewEngine()
	content, err := engine.ReadStatic(includesTemplatePath)
	if err != nil {
		return "", fmt.Errorf("reading embedded includes: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("creating config dir %q: %w", configDir, err)
	}
	path := filepath.Join(configDir, includesDiskName)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return path, nil
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return "", fmt.Errorf("writing includes file %q: %w", path, err)
	}
	return path, nil
}

// LoadExcludePatterns returns the parsed exclude patterns from the embedded
// file (one per non-comment, non-blank line). Used by tests and callers
// that need to introspect rules without going through rsync.
func LoadExcludePatterns() ([]string, error) {
	engine := template.NewEngine()
	content, err := engine.ReadStatic(excludesTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("reading embedded excludes: %w", err)
	}
	return parsePatternLines(content)
}

// LoadDefaultIncludePatterns returns the parsed default include patterns from
// the embedded file.
func LoadDefaultIncludePatterns() ([]string, error) {
	engine := template.NewEngine()
	content, err := engine.ReadStatic(includesTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("reading embedded includes: %w", err)
	}
	return parsePatternLines(content)
}

func loadPatternFileOrDefault(path string, defaults func() ([]string, error)) ([]string, error) {
	if path == "" {
		return defaults()
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaults()
	}
	if err != nil {
		return nil, err
	}
	patterns, err := parsePatternLines(content)
	if err != nil {
		return nil, err
	}
	return patterns, nil
}

func parsePatternLines(content []byte) ([]string, error) {
	var patterns []string
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning patterns: %w", err)
	}
	return patterns, nil
}

// commonArgs returns the rsync flags shared between pull and push.
// dynExcludesFile must be a real path on disk; empty paths are skipped.
//
// Filter order is safety first: always-on state paths, static excludes,
// user ignore.txt, runtime excludes (shared folders + Git-tracked relpaths),
// then include mode's case-insensitive allowlist and final catch-all exclude.
// .gitignore is intentionally not a sync filter because gitignored binaries
// are a primary gsync use case.
func commonArgs(cfg *Config, dynExcludesFile string) []string {
	args := []string{
		"-a",
		"--human-readable",
		"--stats",
		"--no-links",
	}
	args = append(args, alwaysExcludeArgs()...)
	excludeFiles := []string{cfg.ExcludesFile, cfg.IgnoreFile, dynExcludesFile}
	for _, f := range excludeFiles {
		if f == "" {
			continue
		}
		args = append(args, "--exclude-from="+f)
	}
	if normalizeFilterMode(cfg.FilterMode) == FilterModeInclude {
		args = append(args, includeArgs(cfg.IncludePatterns)...)
	}
	if cfg.Verbose {
		args = append(args, "--progress")
	}
	return args
}

func alwaysExcludeArgs() []string {
	return []string{
		"--exclude=/.dotfiles/",
		"--exclude=/inbox/gdrive/",
	}
}

func includeArgs(patterns []string) []string {
	args := []string{"--include=*/"}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		args = append(args, "--include="+rsyncCaseFoldPattern(p))
	}
	args = append(args, "--exclude=*")
	return args
}

func rsyncCaseFoldPattern(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteRune(r - 'a' + 'A')
			b.WriteByte(']')
		case r >= 'A' && r <= 'Z':
			b.WriteByte('[')
			b.WriteRune(r + 'a' - 'A')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
