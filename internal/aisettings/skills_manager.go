package aisettings

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

const (
	SkillsProviderAnchor = "anchor"
	SkillsProviderPath   = "path"

	DefaultAnchorSkillsRoot = "~/.anchor/skills"

	SkillLinkStatusInSync        = "in-sync"
	SkillLinkStatusMissing       = "missing"
	SkillLinkStatusConflict      = "conflict"
	SkillLinkStatusSourceMissing = "source-missing"
)

// SkillsManager applies a configured skills SSOT to per-tool skill roots.
// The SSOT itself remains external: Anchor or the configured path owns source
// directories; dotfiles only deploys consumer-facing symlinks.
type SkillsManager struct {
	Runner  *dotexec.Runner
	HomeDir string
	Tools   []SkillTool
}

// SkillTool describes one tool root that can consume Markdown skills.
type SkillTool struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	RootPath    string   `json:"root_path"`
	Aliases     []string `json:"aliases,omitempty"`
}

// SkillsOptions controls status and apply operations.
type SkillsOptions struct {
	Provider string
	SSOTPath string
	Tools    []string
	DryRun   bool
	Force    bool
}

// SkillSourceItem is a single source skill directory under the SSOT root.
type SkillSourceItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// SkillTargetStatus describes one desired source->tool symlink.
type SkillTargetStatus struct {
	ToolID     string `json:"tool_id"`
	ToolRoot   string `json:"tool_root"`
	SkillName  string `json:"skill_name"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// SkillsStatusReport aggregates source inventory and target drift.
type SkillsStatusReport struct {
	Provider string              `json:"provider"`
	SSOTPath string              `json:"ssot_path"`
	Tools    []string            `json:"tools"`
	Sources  []SkillSourceItem   `json:"sources"`
	Items    []SkillTargetStatus `json:"items"`
	Warnings []string            `json:"warnings,omitempty"`
}

// SkillApplyItem captures one attempted target update.
type SkillApplyItem struct {
	ToolID     string `json:"tool_id"`
	SkillName  string `json:"skill_name"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	Changed    bool   `json:"changed"`
	Conflict   bool   `json:"conflict,omitempty"`
	BackedUp   bool   `json:"backed_up,omitempty"`
	BackupPath string `json:"backup_path,omitempty"`
	Message    string `json:"message,omitempty"`
}

// SkillsApplyResult summarizes a skills apply operation.
type SkillsApplyResult struct {
	Status   *SkillsStatusReport `json:"status"`
	Items    []SkillApplyItem    `json:"items"`
	Warnings []string            `json:"warnings,omitempty"`
	DryRun   bool                `json:"dry_run"`
}

// RegisteredSkillTools returns built-in skill target roots. Gemini and
// Antigravity are intentionally separate because they use separate skills dirs.
func RegisteredSkillTools() []SkillTool {
	return []SkillTool{
		{
			ID:          "claude",
			DisplayName: "Claude Code",
			RootPath:    "~/.claude/skills",
		},
		{
			ID:          "codex",
			DisplayName: "Codex CLI",
			RootPath:    "~/.codex/skills",
		},
		{
			ID:          "agents",
			DisplayName: "Agents",
			RootPath:    "~/.agents/skills",
		},
		{
			ID:          "gemini",
			DisplayName: "Gemini CLI",
			RootPath:    "~/.gemini/skills",
		},
		{
			ID:          "antigravity",
			DisplayName: "Antigravity",
			RootPath:    "~/.gemini/antigravity/skills",
			Aliases:     []string{"agy"},
		},
	}
}

// NewSkillsManager returns a skills manager rooted at homeDir.
func NewSkillsManager(runner *dotexec.Runner, homeDir string) *SkillsManager {
	return &SkillsManager{
		Runner:  runner,
		HomeDir: homeDir,
		Tools:   RegisteredSkillTools(),
	}
}

// DefaultAnchorSSOTPath returns Anchor's runtime skills root.
func (m *SkillsManager) DefaultAnchorSSOTPath() string {
	return expandHome(DefaultAnchorSkillsRoot, m.homeDir())
}

// Tool returns a registered skill target tool by ID or alias.
func (m *SkillsManager) Tool(id string) (SkillTool, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, tool := range m.registry() {
		if tool.ID == id {
			return tool, true
		}
		for _, alias := range tool.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == id {
				return tool, true
			}
		}
	}
	return SkillTool{}, false
}

// TargetRoot returns the absolute skills root for one tool.
func (m *SkillsManager) TargetRoot(toolID string) (string, error) {
	tool, ok := m.Tool(toolID)
	if !ok {
		return "", fmt.Errorf("unknown skills tool %q", toolID)
	}
	return expandHome(tool.RootPath, m.homeDir()), nil
}

// Status reports target symlink drift for all configured source skills.
func (m *SkillsManager) Status(opts SkillsOptions) (*SkillsStatusReport, error) {
	resolved, err := m.resolveOptions(opts)
	if err != nil {
		return nil, err
	}
	sources, warnings, err := m.listSources(resolved.SSOTPath)
	if err != nil {
		return nil, err
	}
	report := &SkillsStatusReport{
		Provider: resolved.Provider,
		SSOTPath: resolved.SSOTPath,
		Tools:    append([]string(nil), resolved.Tools...),
		Sources:  sources,
		Warnings: warnings,
	}
	if len(sources) == 0 && len(warnings) > 0 {
		report.Items = append(report.Items, SkillTargetStatus{
			Status:  SkillLinkStatusSourceMissing,
			Message: strings.Join(warnings, "; "),
		})
		return report, nil
	}
	for _, id := range resolved.Tools {
		root, err := m.TargetRoot(id)
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			target := filepath.Join(root, source.Name)
			status := SkillTargetStatus{
				ToolID:     id,
				ToolRoot:   root,
				SkillName:  source.Name,
				SourcePath: source.Path,
				TargetPath: target,
			}
			m.applyTargetStatus(&status)
			report.Items = append(report.Items, status)
		}
	}
	return report, nil
}

// Apply creates or repairs selected tool symlinks. Conflicts are skipped unless
// Force is set; forced replacements are backed up first.
func (m *SkillsManager) Apply(opts SkillsOptions) (*SkillsApplyResult, error) {
	status, err := m.Status(opts)
	if err != nil {
		return nil, err
	}
	effectiveDryRun := opts.DryRun || m.runner().DryRun
	result := &SkillsApplyResult{
		Status:   status,
		Warnings: append([]string(nil), status.Warnings...),
		DryRun:   effectiveDryRun,
	}
	for _, st := range status.Items {
		if st.Status == SkillLinkStatusSourceMissing {
			continue
		}
		item := SkillApplyItem{
			ToolID:     st.ToolID,
			SkillName:  st.SkillName,
			SourcePath: st.SourcePath,
			TargetPath: st.TargetPath,
			Message:    st.Message,
		}
		switch st.Status {
		case SkillLinkStatusInSync:
			item.Message = "in sync"
		case SkillLinkStatusMissing:
			item.Changed = true
			item.Message = "created symlink"
			if !effectiveDryRun {
				if err := m.createSymlink(st.SourcePath, st.TargetPath); err != nil {
					return nil, err
				}
			}
		case SkillLinkStatusConflict:
			item.Conflict = true
			if !opts.Force {
				item.Message = "conflict skipped; rerun with --force to back up and replace"
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%s conflicts at %s; skipped", st.ToolID, st.SkillName, st.TargetPath))
				break
			}
			item.Changed = true
			item.Message = "replaced conflict with symlink"
			if !effectiveDryRun {
				backup, err := m.backupTarget(st.ToolID, st.SkillName, st.TargetPath)
				if err != nil {
					return nil, err
				}
				item.BackedUp = true
				item.BackupPath = backup
				if err := m.runner().RemoveAll(st.TargetPath); err != nil {
					return nil, fmt.Errorf("remove conflict %s: %w", st.TargetPath, err)
				}
				if err := m.createSymlink(st.SourcePath, st.TargetPath); err != nil {
					return nil, err
				}
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%s was backed up to %s", st.ToolID, st.SkillName, backup))
			}
		default:
			item.Message = st.Message
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (m *SkillsManager) applyTargetStatus(status *SkillTargetStatus) {
	info, err := os.Lstat(status.TargetPath)
	if os.IsNotExist(err) {
		status.Status = SkillLinkStatusMissing
		return
	}
	if err != nil {
		status.Status = SkillLinkStatusConflict
		status.Message = err.Error()
		return
	}
	if info.Mode()&os.ModeSymlink == 0 {
		status.Status = SkillLinkStatusConflict
		status.Message = "target exists and is not a symlink"
		return
	}
	if m.symlinkPointsTo(status.TargetPath, status.SourcePath) {
		status.Status = SkillLinkStatusInSync
		return
	}
	target, err := os.Readlink(status.TargetPath)
	if err != nil {
		status.Message = err.Error()
	} else {
		status.Message = fmt.Sprintf("symlink points to %s", target)
	}
	status.Status = SkillLinkStatusConflict
}

func (m *SkillsManager) resolveOptions(opts SkillsOptions) (SkillsOptions, error) {
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	ssot := strings.TrimSpace(opts.SSOTPath)
	if provider == "" && ssot != "" {
		provider = SkillsProviderPath
	}
	if provider == "" {
		return SkillsOptions{}, fmt.Errorf("skills provider is required (anchor or path)")
	}
	switch provider {
	case SkillsProviderAnchor:
		if ssot == "" {
			ssot = DefaultAnchorSkillsRoot
		}
	case SkillsProviderPath:
		if ssot == "" {
			return SkillsOptions{}, fmt.Errorf("skills ssot path is required for provider path")
		}
	default:
		return SkillsOptions{}, fmt.Errorf("unknown skills provider %q", opts.Provider)
	}
	tools, err := m.resolveToolIDs(opts.Tools)
	if err != nil {
		return SkillsOptions{}, err
	}
	return SkillsOptions{
		Provider: provider,
		SSOTPath: filepath.Clean(expandHome(ssot, m.homeDir())),
		Tools:    tools,
		DryRun:   opts.DryRun,
		Force:    opts.Force,
	}, nil
}

func (m *SkillsManager) resolveToolIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("skills tools must be explicit")
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		for _, part := range strings.Split(id, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			tool, ok := m.Tool(part)
			if !ok {
				return nil, fmt.Errorf("unknown skills tool %q", part)
			}
			if seen[tool.ID] {
				continue
			}
			seen[tool.ID] = true
			out = append(out, tool.ID)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("skills tools must be explicit")
	}
	return out, nil
}

func (m *SkillsManager) listSources(root string) ([]SkillSourceItem, []string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, []string{fmt.Sprintf("skills SSOT root %s does not exist", root)}, nil
		}
		return nil, nil, fmt.Errorf("read skills SSOT root %s: %w", root, err)
	}
	var out []SkillSourceItem
	var warnings []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if !skillNamePattern.MatchString(name) {
			warnings = append(warnings, fmt.Sprintf("ignored invalid skill directory name %s", name))
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Stat(filepath.Join(path, "SKILL.md"))
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("ignored %s: %v", path, err))
			}
			continue
		}
		if info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("ignored %s: SKILL.md is a directory", path))
			continue
		}
		out = append(out, SkillSourceItem{Name: name, Path: path})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, warnings, nil
}

func (m *SkillsManager) symlinkPointsTo(link, desired string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	target = filepath.Clean(target)
	desired = filepath.Clean(desired)
	if target == desired {
		return true
	}
	targetReal, targetErr := filepath.EvalSymlinks(target)
	desiredReal, desiredErr := filepath.EvalSymlinks(desired)
	if targetErr == nil && desiredErr == nil {
		return filepath.Clean(targetReal) == filepath.Clean(desiredReal)
	}
	return false
}

func (m *SkillsManager) createSymlink(source, target string) error {
	if err := m.runner().MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create skills target dir: %w", err)
	}
	if err := m.runner().Symlink(source, target); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", target, source, err)
	}
	return nil
}

func (m *SkillsManager) backupTarget(toolID, skillName, path string) (string, error) {
	dst := m.backupTargetPath(toolID, skillName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	eng := &Engine{Runner: m.runner(), HomeDir: m.homeDir()}
	_, _, err = eng.copyTree(path, dst, info, filepath.Join("skills", toolID, skillName))
	if err != nil {
		return "", err
	}
	return dst, nil
}

func (m *SkillsManager) backupTargetPath(toolID, skillName string) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	return filepath.Join(m.homeDir(), ".local", "share", "dotfiles", "backup", "skills", ts, toolID, skillName)
}

func (m *SkillsManager) registry() []SkillTool {
	if len(m.Tools) > 0 {
		return m.Tools
	}
	return RegisteredSkillTools()
}

func (m *SkillsManager) runner() *dotexec.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return dotexec.NewRunner(false, slog.Default())
}

func (m *SkillsManager) homeDir() string {
	return normalizeHomeDir(m.HomeDir)
}
