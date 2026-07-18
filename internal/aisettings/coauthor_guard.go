package aisettings

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

const (
	CoauthorGuardOff   = "off"
	CoauthorGuardWarn  = "warn"
	CoauthorGuardBlock = "block"

	coauthorGuardStart = "<!-- dotfiles:coauthor-guard:start -->"
	coauthorGuardEnd   = "<!-- dotfiles:coauthor-guard:end -->"
)

// CoauthorGuardManager manages the AGENTS instruction and Git commit-msg guard
// that discourage or block unwanted Co-authored trailers.
type CoauthorGuardManager struct {
	Runner  *dotexec.Runner
	HomeDir string
}

// CoauthorGuardOptions controls guard application.
type CoauthorGuardOptions struct {
	Mode           string
	DryRun         bool
	ForceHooksPath bool
	ApplyAgents    bool
}

// CoauthorGuardStatus describes the live guard state.
type CoauthorGuardStatus struct {
	Mode           string
	HookPath       string
	GitConfigPath  string
	AgentsPath     string
	HookDrift      string
	HooksPath      string
	HooksPathDrift string
	AgentsDrift    string
	Conflict       string
}

// CoauthorGuardResult summarizes guard application.
type CoauthorGuardResult struct {
	Status        CoauthorGuardStatus
	HookChanged   bool
	ConfigChanged bool
	AgentsChanged bool
	AgentsApplied bool
	DryRun        bool
}

// NewCoauthorGuardManager returns a manager rooted at homeDir.
func NewCoauthorGuardManager(runner *dotexec.Runner, homeDir string) *CoauthorGuardManager {
	return &CoauthorGuardManager{Runner: runner, HomeDir: homeDir}
}

// NormalizeCoauthorGuardMode returns the effective guard mode.
func NormalizeCoauthorGuardMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = CoauthorGuardWarn
	}
	switch mode {
	case CoauthorGuardOff, CoauthorGuardWarn, CoauthorGuardBlock:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid coauthor guard mode %q (must be off, warn, or block)", mode)
	}
}

// Status reports whether the guard is installed and active.
func (m *CoauthorGuardManager) Status(mode string) (CoauthorGuardStatus, error) {
	mode, err := NormalizeCoauthorGuardMode(mode)
	if err != nil {
		return CoauthorGuardStatus{}, err
	}
	st := CoauthorGuardStatus{
		Mode:           mode,
		HookPath:       m.hookPath(),
		GitConfigPath:  m.gitConfigPath(),
		AgentsPath:     m.SSOTPath(),
		HookDrift:      "off",
		HooksPathDrift: "off",
		AgentsDrift:    "off",
	}
	if mode == CoauthorGuardOff {
		return st, nil
	}
	if data, err := os.ReadFile(st.HookPath); err == nil {
		if string(data) == coauthorGuardHookScript(mode) {
			st.HookDrift = "in-sync"
		} else {
			st.HookDrift = "out-of-sync"
		}
	} else if os.IsNotExist(err) {
		st.HookDrift = "missing"
	} else {
		return st, fmt.Errorf("read %s: %w", st.HookPath, err)
	}
	hooksPath, hooksDrift, conflict, err := m.hooksPathStatus()
	if err != nil {
		return st, err
	}
	st.HooksPath = hooksPath
	st.HooksPathDrift = hooksDrift
	st.Conflict = conflict
	st.AgentsDrift = m.agentsInstructionDrift()
	return st, nil
}

// Apply installs or updates the guard.
func (m *CoauthorGuardManager) Apply(opts CoauthorGuardOptions) (*CoauthorGuardResult, error) {
	mode, err := NormalizeCoauthorGuardMode(opts.Mode)
	if err != nil {
		return nil, err
	}
	effectiveDryRun := opts.DryRun || m.runner().DryRun
	result := &CoauthorGuardResult{DryRun: effectiveDryRun}
	st, err := m.Status(mode)
	if err != nil {
		return nil, err
	}
	result.Status = st
	if mode == CoauthorGuardOff {
		return result, nil
	}
	if st.Conflict != "" && !opts.ForceHooksPath {
		return nil, fmt.Errorf("%s; rerun with --force-hooks-path to replace it", st.Conflict)
	}

	hookContent := []byte(coauthorGuardHookScript(mode))
	if st.HookDrift != "in-sync" {
		result.HookChanged = true
		if !effectiveDryRun {
			if _, err := fileutil.EnsureFile(m.runner(), st.HookPath, hookContent, 0o755); err != nil {
				return nil, err
			}
			if err := os.Chmod(st.HookPath, 0o755); err != nil {
				return nil, fmt.Errorf("chmod %s: %w", st.HookPath, err)
			}
		}
	}
	if st.HooksPathDrift != "in-sync" {
		result.ConfigChanged = true
		if !effectiveDryRun {
			current := ""
			if data, err := os.ReadFile(st.GitConfigPath); err == nil {
				current = string(data)
			} else if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("read %s: %w", st.GitConfigPath, err)
			}
			next := patchGitHooksPath(current)
			if _, err := fileutil.EnsureFile(m.runner(), st.GitConfigPath, []byte(next), 0o644); err != nil {
				return nil, err
			}
		}
	}
	if st.AgentsDrift != "in-sync" {
		result.AgentsChanged = true
		if !effectiveDryRun {
			if err := m.ensureAgentsInstruction(); err != nil {
				return nil, err
			}
		}
	}
	if opts.ApplyAgents && !effectiveDryRun {
		agents := NewAgentsManager(m.runner(), m.homeDir())
		apply, err := agents.Apply(ApplyOptions{Tools: agents.DefaultApplyTools()})
		if err != nil {
			return nil, err
		}
		for _, item := range apply.Items {
			if item.Changed {
				result.AgentsApplied = true
				break
			}
		}
	}
	if latest, err := m.Status(mode); err == nil {
		result.Status = latest
	}
	return result, nil
}

func (m *CoauthorGuardManager) hooksPathStatus() (value, drift, conflict string, err error) {
	data, readErr := os.ReadFile(m.gitConfigPath())
	if os.IsNotExist(readErr) {
		return "", "missing", "", nil
	}
	if readErr != nil {
		return "", "", "", readErr
	}
	value = gitConfigValue(string(data), "core", "hooksPath")
	if value == "" {
		return "", "missing", "", nil
	}
	if normalizeGitPath(value, m.homeDir()) == normalizeGitPath("~/.config/git/hooks", m.homeDir()) {
		return value, "in-sync", "", nil
	}
	return value, "conflict", fmt.Sprintf("existing core.hooksPath %q is not dot-managed", value), nil
}

func (m *CoauthorGuardManager) agentsInstructionDrift() string {
	data, err := os.ReadFile(m.SSOTPath())
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		return "error"
	}
	if strings.Contains(string(data), coauthorGuardStart) &&
		strings.Contains(string(data), "Co-authored-by") &&
		strings.Contains(string(data), "commit messages in English") {
		return "in-sync"
	}
	return "out-of-sync"
}

func (m *CoauthorGuardManager) ensureAgentsInstruction() error {
	path := m.SSOTPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, err := NewAgentsManager(m.runner(), m.homeDir()).Init(InitOptions{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next := patchAgentsCoauthorInstruction(string(data))
	if next == string(data) {
		return nil
	}
	return m.runner().WriteFile(path, []byte(next), 0o644)
}

func patchAgentsCoauthorInstruction(content string) string {
	block := coauthorGuardStart + "\n" +
		"- Do not add `Co-authored by` or `Co-authored-by:` commit trailers unless the user explicitly requests them. If another hook or tool proposes one, surface it before committing.\n" +
		"- Always write git commit messages in English, regardless of the conversation language.\n" +
		coauthorGuardEnd
	content = strings.TrimRight(content, "\r\n")
	start := strings.Index(content, coauthorGuardStart)
	end := strings.Index(content, coauthorGuardEnd)
	if start >= 0 && end >= start {
		end += len(coauthorGuardEnd)
		return strings.TrimRight(content[:start]+block+content[end:], "\n") + "\n"
	}
	lines := strings.Split(content, "\n")
	sectionStart, sectionEnd := findMarkdownSection(lines, "Tool-Specific Notes")
	if sectionStart >= 0 {
		next := append([]string{}, lines[:sectionEnd]...)
		if sectionEnd > sectionStart+1 && strings.TrimSpace(lines[sectionEnd-1]) != "" {
			next = append(next, "")
		}
		next = append(next, block)
		next = append(next, lines[sectionEnd:]...)
		return strings.TrimRight(strings.Join(next, "\n"), "\n") + "\n"
	}
	if content == "" {
		return "## Tool-Specific Notes\n\n" + block + "\n"
	}
	return content + "\n\n## Tool-Specific Notes\n\n" + block + "\n"
}

func coauthorGuardHookScript(mode string) string {
	mode, _ = NormalizeCoauthorGuardMode(mode)
	return fmt.Sprintf(`#!/bin/sh
# Managed by dot ai coauthor-guard. Edit dotfiles config, not this file.
msg_file="$1"
[ -n "$DOTFILES_COAUTHOR_GUARD_ALLOW" ] && exit 0
[ -n "$msg_file" ] && [ -f "$msg_file" ] || exit 0

if grep -Eiq '^[[:space:]]*Co-authored[ -]by[[:space:]]*:?' "$msg_file"; then
  cat >&2 <<'EOF'
dotfiles coauthor guard: commit message contains a Co-authored trailer.
AI agents and hooks should not add Co-authored by / Co-authored-by trailers unless explicitly requested.
Set DOTFILES_COAUTHOR_GUARD_ALLOW=1 for a one-off bypass.
EOF
  [ %q = "block" ] && exit 1
fi
exit 0
`, mode)
}

func patchGitHooksPath(content string) string {
	desired := "    hooksPath = ~/.config/git/hooks"
	content = strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return "[core]\n" + desired + "\n"
	}
	lines := strings.Split(trimmed, "\n")
	start, end := findTOMLTable(lines, "core")
	if start < 0 {
		return trimmed + "\n\n[core]\n" + desired + "\n"
	}
	keyStart, keyEnd := findTOMLKey(lines, start+1, end, "hooksPath")
	if keyStart < 0 {
		keyStart, keyEnd = findTOMLKey(lines, start+1, end, "hookspath")
	}
	if keyStart >= 0 {
		next := append([]string{}, lines[:keyStart]...)
		next = append(next, desired)
		next = append(next, lines[keyEnd:]...)
		return strings.Join(next, "\n") + "\n"
	}
	next := append([]string{}, lines[:end]...)
	next = append(next, desired)
	next = append(next, lines[end:]...)
	return strings.Join(next, "\n") + "\n"
}

func gitConfigValue(content, table, key string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	start, end := findTOMLTable(lines, table)
	if start < 0 {
		return ""
	}
	pattern := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*(.+?)\s*(#.*)?$`)
	for i := start + 1; i < end; i++ {
		match := pattern.FindStringSubmatch(lines[i])
		if len(match) > 1 {
			return strings.Trim(strings.TrimSpace(match[1]), `"'`)
		}
	}
	return ""
}

func normalizeGitPath(path, home string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}
	return filepath.Clean(path)
}

func (m *CoauthorGuardManager) runner() *dotexec.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return dotexec.NewRunner(false, slog.Default())
}

func (m *CoauthorGuardManager) homeDir() string {
	if m.HomeDir != "" {
		return m.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (m *CoauthorGuardManager) SSOTPath() string {
	return filepath.Join(m.homeDir(), AgentsSSOTRelPath, AgentsSSOTName)
}

func (m *CoauthorGuardManager) hookPath() string {
	return filepath.Join(m.homeDir(), ".config", "git", "hooks", "commit-msg")
}

func (m *CoauthorGuardManager) gitConfigPath() string {
	return filepath.Join(m.homeDir(), ".config", "git", "config")
}
