package aisettings

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SkillStatusValid   = "valid"
	SkillStatusLegacy  = "legacy"
	SkillStatusInvalid = "invalid"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// SkillFrontmatterV1 is the schema-versioned metadata contract for SKILL.md.
type SkillFrontmatterV1 struct {
	Name          string   `json:"name" yaml:"name"`
	Description   string   `json:"description" yaml:"description"`
	Triggers      []string `json:"triggers,omitempty" yaml:"triggers"`
	AllowedTools  []string `json:"allowed-tools,omitempty" yaml:"allowed-tools"`
	Plugin        string   `json:"plugin,omitempty" yaml:"plugin"`
	Version       int      `json:"version,omitempty" yaml:"version"`
	SchemaVersion string   `json:"schema_version,omitempty" yaml:"schema_version"`
}

// SkillRoot describes one filesystem root to scan for SKILL.md files.
type SkillRoot struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
}

// SkillScanOptions controls skill inventory scanning.
type SkillScanOptions struct {
	HomeDir string
	Tools   []string
	Roots   []string
}

// SkillInventoryItem is one discovered SKILL.md plus validation status.
type SkillInventoryItem struct {
	Tool        string             `json:"tool"`
	Root        string             `json:"root"`
	Path        string             `json:"path"`
	RelPath     string             `json:"rel_path"`
	Status      string             `json:"status"`
	Frontmatter SkillFrontmatterV1 `json:"frontmatter,omitempty"`
	Errors      []string           `json:"errors,omitempty"`
}

// SkillDuplicate reports a duplicate schema-valid skill name.
type SkillDuplicate struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

// SkillScanReport aggregates a skill inventory scan.
type SkillScanReport struct {
	Roots      []SkillRoot          `json:"roots"`
	Items      []SkillInventoryItem `json:"items"`
	Duplicates []SkillDuplicate     `json:"duplicates,omitempty"`
	Counts     map[string]int       `json:"counts"`
	Errors     []string             `json:"errors,omitempty"`
}

// DefaultSkillRoots returns diagnostic scan roots for selected tools. Skill
// source creation and reconciliation remain outside dotfiles; configured
// symlink deployment lives in SkillsManager.
func DefaultSkillRoots(homeDir string, tools []string) ([]SkillRoot, error) {
	homeDir = normalizeHomeDir(homeDir)
	selected := normalizeSkillTools(tools)
	all := []SkillRoot{
		{Tool: "codex", Path: filepath.Join(homeDir, ".codex", "skills")},
		{Tool: "claude", Path: filepath.Join(homeDir, ".claude", "skills")},
		{Tool: "agents", Path: filepath.Join(homeDir, ".agents", "skills")},
		{Tool: "antigravity", Path: filepath.Join(homeDir, ".gemini", "antigravity", "skills")},
		{Tool: "gemini", Path: filepath.Join(homeDir, ".gemini", "skills")},
		{Tool: "antigravity", Path: filepath.Join(homeDir, ".gemini", "config", "plugins")},
		{Tool: "antigravity", Path: filepath.Join(homeDir, ".gemini", "antigravity-cli", "plugins")},
	}
	if len(selected) == 0 {
		return all, nil
	}
	allow := map[string]bool{}
	for _, tool := range selected {
		switch tool {
		case "codex", "claude", "agents", "gemini", "antigravity":
			allow[tool] = true
		default:
			return nil, fmt.Errorf("unknown skill tool %q", tool)
		}
	}
	var roots []SkillRoot
	for _, root := range all {
		if allow[root.Tool] {
			roots = append(roots, root)
		}
	}
	return roots, nil
}

// ScanSkills inventories SKILL.md files and validates their frontmatter.
func ScanSkills(opts SkillScanOptions) (*SkillScanReport, error) {
	homeDir := normalizeHomeDir(opts.HomeDir)
	var roots []SkillRoot
	if len(opts.Roots) > 0 {
		for _, root := range opts.Roots {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			roots = append(roots, SkillRoot{Tool: "custom", Path: expandHome(root, homeDir)})
		}
	} else {
		var err error
		roots, err = DefaultSkillRoots(homeDir, opts.Tools)
		if err != nil {
			return nil, err
		}
	}

	report := &SkillScanReport{
		Roots:  roots,
		Counts: map[string]int{SkillStatusValid: 0, SkillStatusLegacy: 0, SkillStatusInvalid: 0},
	}
	byName := map[string][]string{}
	seenPath := map[string]bool{}
	for _, root := range roots {
		items, err := scanSkillRoot(root)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		for _, item := range items {
			key := canonicalSkillPath(item.Path)
			if seenPath[key] {
				continue
			}
			seenPath[key] = true
			report.Items = append(report.Items, item)
			report.Counts[item.Status]++
			if item.Status == SkillStatusValid && item.Frontmatter.Name != "" {
				byName[item.Frontmatter.Name] = append(byName[item.Frontmatter.Name], item.Path)
			}
		}
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return report.Items[i].Path < report.Items[j].Path
	})
	for name, paths := range byName {
		if len(paths) < 2 {
			continue
		}
		sort.Strings(paths)
		report.Duplicates = append(report.Duplicates, SkillDuplicate{Name: name, Paths: paths})
	}
	sort.Slice(report.Duplicates, func(i, j int) bool {
		return report.Duplicates[i].Name < report.Duplicates[j].Name
	})
	return report, nil
}

// ValidationErrors returns user-facing validation failures for `dot ai skills validate`.
func (r *SkillScanReport) ValidationErrors(strict bool) []string {
	if r == nil {
		return []string{"skill scan report is nil"}
	}
	var errs []string
	errs = append(errs, r.Errors...)
	for _, item := range r.Items {
		if item.Status == SkillStatusInvalid || (strict && item.Status == SkillStatusLegacy) {
			if len(item.Errors) == 0 {
				name := item.Frontmatter.Name
				if name == "" {
					name = "(unnamed)"
				}
				errs = append(errs, fmt.Sprintf("%s: %s skill %s", item.Path, item.Status, name))
				continue
			}
			errs = append(errs, fmt.Sprintf("%s: %s", item.Path, strings.Join(item.Errors, "; ")))
		}
	}
	for _, dup := range r.Duplicates {
		errs = append(errs, fmt.Sprintf("duplicate skill name %q: %s", dup.Name, strings.Join(dup.Paths, ", ")))
	}
	return errs
}

func scanSkillRoot(root SkillRoot) ([]SkillInventoryItem, error) {
	info, err := os.Stat(root.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan %s: %w", root.Path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scan %s: not a directory", root.Path)
	}
	var items []SkillInventoryItem
	err = filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return appendSymlinkSkill(root, path, &items)
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		item := readSkillItem(root, path)
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root.Path, err)
	}
	return items, nil
}

func appendSymlinkSkill(root SkillRoot, path string, items *[]SkillInventoryItem) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		skillPath := filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		*items = append(*items, readSkillItem(root, skillPath))
		return nil
	}
	if filepath.Base(path) == "SKILL.md" {
		*items = append(*items, readSkillItem(root, path))
	}
	return nil
}

func readSkillItem(root SkillRoot, path string) SkillInventoryItem {
	rel, err := filepath.Rel(root.Path, path)
	if err != nil {
		rel = path
	}
	item := SkillInventoryItem{
		Tool:    root.Tool,
		Root:    root.Path,
		Path:    path,
		RelPath: rel,
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		item.Status = SkillStatusInvalid
		item.Errors = []string{fmt.Sprintf("read: %v", err)}
		return item
	}
	fm, present, errs := ParseSkillFrontmatter(raw)
	item.Frontmatter = fm
	item.Errors = errs
	switch {
	case len(errs) > 0:
		item.Status = SkillStatusInvalid
	case !present || fm.SchemaVersion == "":
		item.Status = SkillStatusLegacy
	default:
		item.Status = SkillStatusValid
	}
	return item
}

// ParseSkillFrontmatter parses and validates the leading YAML frontmatter.
func ParseSkillFrontmatter(raw []byte) (SkillFrontmatterV1, bool, []string) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return SkillFrontmatterV1{}, false, []string{}
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return SkillFrontmatterV1{}, true, []string{"frontmatter is not closed"}
	}
	block := rest[:end]
	var fm SkillFrontmatterV1
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return SkillFrontmatterV1{}, true, []string{fmt.Sprintf("frontmatter yaml: %v", err)}
	}
	errs := ValidateSkillFrontmatter(fm)
	var rawMap map[string]any
	if err := yaml.Unmarshal([]byte(block), &rawMap); err == nil {
		if _, ok := rawMap["version"]; ok && fm.Version < 1 {
			errs = append(errs, "version must be >= 1 when set")
		}
	}
	return fm, true, errs
}

// ValidateSkillFrontmatter applies the v1 metadata contract without adding a JSON Schema dependency.
func ValidateSkillFrontmatter(fm SkillFrontmatterV1) []string {
	var errs []string
	if strings.TrimSpace(fm.Name) == "" {
		errs = append(errs, "name is required")
	} else if !skillNamePattern.MatchString(fm.Name) {
		errs = append(errs, "name must match ^[a-z0-9-]+$")
	}
	if strings.TrimSpace(fm.Description) == "" {
		errs = append(errs, "description is required")
	}
	if fm.SchemaVersion != "" && fm.SchemaVersion != "v1" {
		errs = append(errs, "schema_version must be v1")
	}
	return errs
}

func (r *SkillScanReport) MarshalJSON() ([]byte, error) {
	type alias SkillScanReport
	if r.Counts == nil {
		r.Counts = map[string]int{}
	}
	return json.Marshal((*alias)(r))
}

func normalizeSkillTools(tools []string) []string {
	var out []string
	for _, tool := range tools {
		for _, part := range strings.Split(tool, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func canonicalSkillPath(path string) string {
	realPath, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = realPath
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func normalizeHomeDir(homeDir string) string {
	if homeDir != "" {
		return homeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func expandHome(path, homeDir string) string {
	if path == "~" {
		return homeDir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}
