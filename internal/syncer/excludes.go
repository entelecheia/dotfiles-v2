// Package gsync implements the local→mirror rsync flow that backs
// `dot sync`. Git owns tracked source files, baseline.manifest is the
// Git-shared Drive payload index for untracked artifacts, and push propagates
// local artifact creates/updates by default while deletes are opt-in.
package syncer

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/template"
)

// excludesTemplatePath is the path inside the embedded templates FS.
const excludesTemplatePath = "sync/excludes.txt"

// includesTemplatePath is the path inside the embedded templates FS.
const includesTemplatePath = "sync/includes.txt"

// excludesDiskName is kept for legacy global path reporting; runtime gsync
// uses the workspace-local exclude file under .dotfiles/sync/.
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

// runtimeFilters bundles the per-run generated filter files that layer into
// the rsync argv alongside the static exclude/ignore/allow files.
type runtimeFilters struct {
	SharedDyn     string // shared-folder excludes (operator-curated)
	SubmodulesDyn string // git submodule paths — synced via Git, never rsync
	TrackedDyn    string // include layer: tracked relpaths ∪ baseline keys
}

// secretExcludePatterns is the deny-by-default secrets layer. These paths
// never sync unless the operator explicitly re-includes them in allow.txt.
var secretExcludePatterns = []string{
	"/.secrets",
	"/.secrets/**",
	"/.maru/secrets",
	"/.maru/secrets/**",
	"/_sys/mcp.local.json",
	".env",
	".env.*",
}

// secretAllowBuiltins re-include harmless env templates ahead of the
// `.env.*` exclude — they are documentation, not credentials.
var secretAllowBuiltins = []string{
	".env.example",
	".env.sample",
	".env.template",
}

// commonArgs returns the rsync flags shared between pull and push.
// Empty paths inside rf are skipped.
//
// Filter order is first-match-wins, safety first:
//  1. always-on state paths (/.dotfiles/, /inbox/gdrive/)
//  2. submodule excludes (submodules sync through Git)
//  3. allow.txt re-includes — the only way secrets sync — plus the
//     env-template builtins
//  4. hardcoded secrets excludes
//  5. exclude.txt (junk), 6. ignore.txt (user), 7. shared-folder excludes
//  8. --include=*/ (traversal), 9. tracked ∪ baseline includes,
//  10. binary-extension allowlist, 11. --exclude=* catch-all
//
// .gitignore is intentionally not a sync filter because gitignored binaries
// are a primary sync payload. Exclude mode stops after layer 7 (everything
// not excluded syncs).
func commonArgs(cfg *Config, rf runtimeFilters) []string {
	args := []string{
		"-a",
		"--human-readable",
		"--stats",
		"--no-links",
		// A source directory without owner-write is recreated with that mode on
		// the receiver, and rsync then cannot mkstemp inside it: "Permission
		// denied (13)" and exit 23. Measured: one dr-xr-xr-x directory in an 88 GB
		// tree held the only two files that failed a full transfer. Adding
		// owner-write to received directories costs one mode bit and removes the
		// whole failure class; file modes are untouched.
		"--chmod=Du+w",
	}
	args = append(args, alwaysExcludeArgs()...)
	if rf.SubmodulesDyn != "" {
		args = append(args, "--exclude-from="+rf.SubmodulesDyn)
	}
	args = append(args, secretsFilterArgs(cfg.AllowPatterns)...)
	excludeFiles := []string{cfg.ExcludesFile, cfg.IgnoreFile, rf.SharedDyn}
	for _, f := range excludeFiles {
		if f == "" {
			continue
		}
		args = append(args, "--exclude-from="+f)
	}
	if normalizeFilterMode(cfg.FilterMode) == FilterModeInclude {
		args = append(args, "--include=*/")
		if rf.TrackedDyn != "" {
			args = append(args, "--include-from="+rf.TrackedDyn)
		}
		for _, p := range cfg.IncludePatterns {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			args = append(args, "--include="+rsyncCaseFoldPattern(p))
		}
		args = append(args, "--exclude=*")
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
		// .git (dir or gitlink file, any depth) is enforced in code, not just
		// the editable exclude.txt — an operator edit must never leak VCS
		// internals to the target.
		"--exclude=.git",
	}
}

// secretsFilterArgs renders the allow.txt re-includes (with parent-dir
// includes so rsync can descend into otherwise-excluded directories),
// the env-template builtins, then the hardcoded secrets excludes.
func secretsFilterArgs(allowPatterns []string) []string {
	var args []string
	for _, p := range allowPatterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		for _, dir := range allowParentDirs(p) {
			args = append(args, "--include="+dir)
		}
		args = append(args, "--include="+p)
	}
	for _, p := range secretAllowBuiltins {
		args = append(args, "--include="+p)
	}
	for _, p := range secretExcludePatterns {
		args = append(args, "--exclude="+p)
	}
	return args
}

// allowParentDirs returns anchored directory includes for every literal
// parent of an anchored allow pattern, so `/​.maru/secrets/app.token` also
// re-includes `/.maru/` and `/.maru/secrets/`. Parents containing wildcards
// are skipped (rsync would treat them literally).
func allowParentDirs(pattern string) []string {
	if !strings.HasPrefix(pattern, "/") {
		return nil
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(pattern, "/"), "/")
	segs := strings.Split(trimmed, "/")
	var dirs []string
	prefix := ""
	for _, seg := range segs[:len(segs)-1] {
		if strings.ContainsAny(seg, "*?[") {
			break
		}
		prefix += "/" + seg
		dirs = append(dirs, prefix+"/")
	}
	return dirs
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
