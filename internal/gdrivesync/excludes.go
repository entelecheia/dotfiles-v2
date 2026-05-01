// Package gdrivesync implements the local↔local rsync mirror that backs
// `dot gdrive-sync`. It keeps ~/workspace/work and ~/gdrive-workspace/work
// in sync via plain rsync (no SSH), with workspace-authoritative semantics:
// pull only fetches newer files; push uses --delete-after to propagate
// workspace deletions. Drive-side deletions never silently propagate.
package gdrivesync

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// excludesTemplatePath is the path inside the embedded templates FS.
const excludesTemplatePath = "gdrivesync/excludes.txt"

// excludesDiskName is the on-disk filename for the materialized excludes
// file (rsync needs a real file path for --exclude-from).
const excludesDiskName = "gdrive-sync-excludes.conf"

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

// LoadExcludePatterns returns the parsed exclude patterns from the embedded
// file (one per non-comment, non-blank line). Used by tests and callers
// that need to introspect rules without going through rsync.
func LoadExcludePatterns() ([]string, error) {
	engine := template.NewEngine()
	content, err := engine.ReadStatic(excludesTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("reading embedded excludes: %w", err)
	}
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
		return nil, fmt.Errorf("scanning excludes: %w", err)
	}
	return patterns, nil
}

// commonArgs returns the rsync flags shared between pull and push.
// excludeFiles must be real paths on disk (use MaterializeExcludesFile
// + MaterializeSharedExcludesFile). Empty paths are skipped.
//
// Layered exclusions: static baseline (embedded excludes) + dynamic
// shared excludes (auto-detected Drive shortcuts + operator manual
// list) + --filter=:- .gitignore (per-directory gitignore) +
// --no-links (skip symlinks entirely). All four are always-on.
func commonArgs(excludeFiles []string, verbose bool) []string {
	args := []string{
		"-a",
		"--human-readable",
		"--info=stats2,progress2",
		"--no-links",
		"--filter=:- .gitignore",
	}
	for _, f := range excludeFiles {
		if f == "" {
			continue
		}
		args = append(args, "--exclude-from="+f)
	}
	if verbose {
		args = append(args, "--progress")
	}
	return args
}
