// Package gsync implements the local→mirror rsync flow that backs
// `dot gsync`. Git owns tracked source files, baseline.manifest is the
// Git-shared Drive payload index for untracked artifacts, and push propagates
// local artifact creates/updates by default while deletes are opt-in.
package gsync

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// excludesTemplatePath is the path inside the embedded templates FS.
const excludesTemplatePath = "gsync/excludes.txt"

// includesTemplatePath is the path inside the embedded templates FS.
const includesTemplatePath = "gsync/includes.txt"

// excludesDiskName is kept for legacy global path reporting; runtime gsync
// uses the workspace-local exclude file under .dotfiles/gdrive-sync/.
const excludesDiskName = "gdrive-sync-excludes.conf"

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
