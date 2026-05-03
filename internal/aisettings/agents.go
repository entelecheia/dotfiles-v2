package aisettings

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
	dottemplate "github.com/entelecheia/dotfiles-v2/internal/template"
)

const (
	AgentsSSOTRelPath = ".config/dotfiles/agents"
	AgentsSSOTName    = "AGENTS.md"

	agentsStateName      = ".state.json"
	agentsManagedHeader  = "<!-- managed by dotfiles ai agents - edit ~/.config/dotfiles/agents/AGENTS.md -->"
	agentsOverlayPattern = "<!-- overlay:%s -->"
)

var defaultAgentSections = []string{
	"Identity",
	"How I Work",
	"Operating Principles",
	"Project Conventions",
	"Tool-Specific Notes",
	"Custom",
}

// AgentsManager manages the shared AI agents instruction SSOT and deployed
// per-tool copies.
type AgentsManager struct {
	Runner  *dotexec.Runner
	HomeDir string
	SSOTDir string
	Tools   []AgentTool
}

// AgentStatus describes one rendered target's sync state.
type AgentStatus struct {
	Tool          AgentTool
	TargetPath    string
	TargetExists  bool
	SSOTExists    bool
	SSOTHash      string
	RenderedHash  string
	TargetHash    string
	Drift         string
	OverlayExists bool
}

// ApplyOptions controls AgentsManager.Apply.
type ApplyOptions struct {
	Tools  []string
	DryRun bool
	Force  bool
	Yes    bool
}

// ApplyResult summarizes an agents apply operation.
type ApplyResult struct {
	Items    []AgentApplyItem
	Warnings []string
	DryRun   bool
}

// AgentApplyItem captures one tool target write.
type AgentApplyItem struct {
	ToolID     string
	TargetPath string
	Changed    bool
	BackedUp   bool
	BackupPath string
	Diff       string
}

// PullOptions controls copying a live tool target back into the SSOT.
type PullOptions struct {
	FromTool string
	Yes      bool
	Force    bool
}

// PullResult summarizes a pull operation.
type PullResult struct {
	FromTool   string
	SourcePath string
	SSOTPath   string
	BackupPath string
	Changed    bool
}

// InitOptions controls SSOT initialization.
type InitOptions struct {
	FromCurrent string
	Yes         bool
	Force       bool
}

// InitResult summarizes SSOT initialization.
type InitResult struct {
	Path       string
	Created    bool
	FromTool   string
	BackupPath string
}

// AuthorOptions controls assisted SSOT authoring.
type AuthorOptions struct {
	FromCurrent    string
	NonInteractive bool
	Section        string
	Value          string
	Yes            bool
}

// AuthorResult summarizes assisted authoring.
type AuthorResult struct {
	Path     string
	Changed  bool
	Sections []string
}

// ShowOptions controls SSOT display.
type ShowOptions struct {
	RenderedTool    string
	WithLineNumbers bool
}

type agentsState struct {
	LastApplied map[string]string `json:"lastApplied"`
}

// NewAgentsManager returns an agents manager rooted at homeDir.
func NewAgentsManager(runner *dotexec.Runner, homeDir string) *AgentsManager {
	return &AgentsManager{
		Runner:  runner,
		HomeDir: homeDir,
		Tools:   RegisteredAgentTools(),
	}
}

// SSOTPath returns the absolute path to the shared AGENTS.md file.
func (m *AgentsManager) SSOTPath() string {
	return filepath.Join(m.ssotDir(), AgentsSSOTName)
}

// SSOTDirPath returns the absolute path to the agents SSOT directory.
func (m *AgentsManager) SSOTDirPath() string {
	return m.ssotDir()
}

// StatePath returns the absolute path to the apply-state file.
func (m *AgentsManager) StatePath() string {
	return filepath.Join(m.ssotDir(), agentsStateName)
}

// DefaultApplyTools returns non-optional tools plus optional tools whose target
// file already exists.
func (m *AgentsManager) DefaultApplyTools() []string {
	var ids []string
	for _, tool := range m.registry() {
		target, err := m.TargetPath(tool.ID)
		if err != nil {
			continue
		}
		if !tool.Optional {
			ids = append(ids, tool.ID)
			continue
		}
		if _, err := os.Lstat(target); err == nil {
			ids = append(ids, tool.ID)
		}
	}
	return ids
}

// Tool returns a registered tool by id.
func (m *AgentsManager) Tool(id string) (AgentTool, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, tool := range m.registry() {
		if tool.ID == id {
			return tool, true
		}
	}
	return AgentTool{}, false
}

// TargetPath returns a registered tool target path as an absolute path.
func (m *AgentsManager) TargetPath(toolID string) (string, error) {
	tool, ok := m.Tool(toolID)
	if !ok {
		return "", fmt.Errorf("unknown agents tool %q", toolID)
	}
	return m.expandHome(tool.TargetPath), nil
}

// Status reports SSOT drift for every registered tool.
func (m *AgentsManager) Status() ([]AgentStatus, error) {
	ssotPath := m.SSOTPath()
	ssotBytes, ssotErr := os.ReadFile(ssotPath)
	ssotExists := ssotErr == nil
	if ssotErr != nil && !os.IsNotExist(ssotErr) {
		return nil, fmt.Errorf("read SSOT: %w", ssotErr)
	}

	var out []AgentStatus
	for _, tool := range m.registry() {
		target, err := m.TargetPath(tool.ID)
		if err != nil {
			return nil, err
		}
		st := AgentStatus{
			Tool:       tool,
			TargetPath: target,
			SSOTExists: ssotExists,
		}
		if ssotExists {
			st.SSOTHash = normalizedHash(ssotBytes)
			rendered, overlayExists, err := m.renderTool(tool, ssotBytes)
			if err != nil {
				return nil, err
			}
			st.RenderedHash = normalizedHash([]byte(rendered))
			st.OverlayExists = overlayExists
		}
		targetBytes, err := os.ReadFile(target)
		if err == nil {
			st.TargetExists = true
			st.TargetHash = normalizedHash(targetBytes)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read target %s: %w", target, err)
		}

		switch {
		case !ssotExists:
			st.Drift = "ssot-missing"
		case !st.TargetExists:
			st.Drift = "target-missing"
		case st.TargetHash == st.RenderedHash:
			st.Drift = "in-sync"
		default:
			st.Drift = "out-of-sync"
		}
		out = append(out, st)
	}
	return out, nil
}

// Render returns the SSOT rendered for one tool, including its optional overlay.
func (m *AgentsManager) Render(toolID string) (string, error) {
	tool, ok := m.Tool(toolID)
	if !ok {
		return "", fmt.Errorf("unknown agents tool %q", toolID)
	}
	ssotBytes, err := os.ReadFile(m.SSOTPath())
	if err != nil {
		return "", fmt.Errorf("read SSOT: %w", err)
	}
	rendered, _, err := m.renderTool(tool, ssotBytes)
	return rendered, err
}

// Apply renders the SSOT to the selected tool targets. An empty Tools list
// applies to every registered tool; command callers can pass DefaultApplyTools
// when they want a narrower CLI default.
func (m *AgentsManager) Apply(opts ApplyOptions) (*ApplyResult, error) {
	ids, err := m.resolveToolIDs(opts.Tools)
	if err != nil {
		return nil, err
	}
	state, err := m.readState()
	if err != nil {
		return nil, err
	}
	result := &ApplyResult{DryRun: opts.DryRun || m.runner().DryRun}
	for _, id := range ids {
		target, err := m.TargetPath(id)
		if err != nil {
			return nil, err
		}
		rendered, err := m.Render(id)
		if err != nil {
			return nil, err
		}
		renderedHash := normalizedHash([]byte(rendered))
		targetBytes, targetErr := os.ReadFile(target)
		targetExists := targetErr == nil
		if targetErr != nil && !os.IsNotExist(targetErr) {
			return nil, fmt.Errorf("read target %s: %w", target, targetErr)
		}
		targetHash := normalizedHash(targetBytes)
		changed := !targetExists || targetHash != renderedHash

		item := AgentApplyItem{
			ToolID:     id,
			TargetPath: target,
			Changed:    changed,
		}
		if changed {
			item.Diff = unifiedTextDiff("target/"+id, "rendered/"+id, string(targetBytes), rendered)
		}

		if changed && targetExists && targetHash != "" && targetHash != state.LastApplied[id] {
			backupPath := m.backupTargetPath(id)
			if !result.DryRun {
				var err error
				backupPath, err = m.backupTarget(id, target)
				if err != nil {
					return nil, err
				}
			}
			item.BackedUp = true
			item.BackupPath = backupPath
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s target was changed outside agents SSOT; backed up to %s", id, backupPath))
		}

		if changed && !result.DryRun {
			if err := m.runner().MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("create target dir: %w", err)
			}
			if err := m.runner().WriteFile(target, []byte(rendered), 0o644); err != nil {
				return nil, fmt.Errorf("write target %s: %w", target, err)
			}
		}
		if !result.DryRun && (!changed || targetExists || renderedHash != "") {
			state.LastApplied[id] = renderedHash
		}
		result.Items = append(result.Items, item)
	}
	if !result.DryRun {
		if err := m.writeState(state); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Pull copies one live tool target back into the shared SSOT.
func (m *AgentsManager) Pull(opts PullOptions) (*PullResult, error) {
	if opts.FromTool == "" {
		return nil, fmt.Errorf("--from is required")
	}
	source, err := m.TargetPath(opts.FromTool)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	ssot := m.SSOTPath()
	result := &PullResult{
		FromTool:   opts.FromTool,
		SourcePath: source,
		SSOTPath:   ssot,
	}
	if old, err := os.ReadFile(ssot); err == nil {
		if normalizedHash(old) == normalizedHash(data) {
			return result, nil
		}
		if !opts.Yes && !opts.Force {
			return nil, fmt.Errorf("%s already exists; rerun with --yes to overwrite after backing it up", ssot)
		}
		backup, err := m.backupSSOT()
		if err != nil {
			return nil, err
		}
		result.BackupPath = backup
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read SSOT: %w", err)
	}
	if err := m.runner().MkdirAll(filepath.Dir(ssot), 0o755); err != nil {
		return nil, err
	}
	if err := m.runner().WriteFile(ssot, data, 0o644); err != nil {
		return nil, err
	}
	result.Changed = true
	return result, nil
}

// Init creates the shared SSOT from a live target or the embedded template.
func (m *AgentsManager) Init(opts InitOptions) (*InitResult, error) {
	if opts.FromCurrent != "" {
		pulled, err := m.Pull(PullOptions{FromTool: opts.FromCurrent, Yes: opts.Yes, Force: opts.Force})
		if err != nil {
			return nil, err
		}
		return &InitResult{
			Path:       pulled.SSOTPath,
			Created:    pulled.Changed,
			FromTool:   pulled.FromTool,
			BackupPath: pulled.BackupPath,
		}, nil
	}
	path := m.SSOTPath()
	if _, err := os.Stat(path); err == nil && !opts.Force {
		return &InitResult{Path: path}, nil
	}
	result := &InitResult{Path: path, Created: true}
	if _, err := os.Stat(path); err == nil && opts.Force {
		backup, err := m.backupSSOT()
		if err != nil {
			return nil, err
		}
		result.BackupPath = backup
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	tmpl := dottemplate.NewEngine()
	data, err := tmpl.ReadStatic("agents/AGENTS.md.tmpl")
	if err != nil {
		return nil, err
	}
	if err := m.runner().MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := m.runner().WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return result, nil
}

// Author updates the SSOT through either a non-interactive section set or a
// simple terminal prompt.
func (m *AgentsManager) Author(opts AuthorOptions) (*AuthorResult, error) {
	if opts.FromCurrent != "" {
		if _, err := m.Pull(PullOptions{FromTool: opts.FromCurrent, Yes: opts.Yes, Force: opts.Yes}); err != nil {
			return nil, err
		}
	}
	if opts.NonInteractive {
		if strings.TrimSpace(opts.Section) == "" {
			return nil, fmt.Errorf("--section is required with --non-interactive")
		}
		return m.authorSection(opts.Section, opts.Value)
	}
	if !stdinIsTerminal() {
		return nil, fmt.Errorf("agents author requires a TTY; use `dotfiles ai agents init` plus `dotfiles ai agents edit`, or pass --non-interactive --section --value")
	}
	if _, err := os.Stat(m.SSOTPath()); os.IsNotExist(err) {
		if _, err := m.Init(InitOptions{}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return m.authorInteractive()
}

// Show returns raw or rendered SSOT text.
func (m *AgentsManager) Show(opts ShowOptions) (string, error) {
	var data string
	var err error
	if opts.RenderedTool != "" {
		data, err = m.Render(opts.RenderedTool)
	} else {
		b, readErr := os.ReadFile(m.SSOTPath())
		err = readErr
		data = string(b)
	}
	if err != nil {
		return "", err
	}
	if opts.WithLineNumbers {
		return numberLines(data), nil
	}
	return data, nil
}

// Diff returns a unified text diff between rendered desired content and the
// current live target.
func (m *AgentsManager) Diff(toolID string) (string, error) {
	rendered, err := m.Render(toolID)
	if err != nil {
		return "", err
	}
	target, err := m.TargetPath(toolID)
	if err != nil {
		return "", err
	}
	live, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return unifiedTextDiff("target/"+toolID, "rendered/"+toolID, string(live), rendered), nil
}

func (m *AgentsManager) authorSection(section, value string) (*AuthorResult, error) {
	path := m.SSOTPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if _, err := m.Init(InitOptions{}); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	next := setMarkdownSection(string(data), section, value)
	changed := normalizedHash(data) != normalizedHash([]byte(next))
	if changed {
		if err := m.runner().WriteFile(path, []byte(next), 0o644); err != nil {
			return nil, err
		}
	}
	return &AuthorResult{Path: path, Changed: changed, Sections: []string{section}}, nil
}

func (m *AgentsManager) authorInteractive() (*AuthorResult, error) {
	path := m.SSOTPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc := string(data)
	reader := bufio.NewReader(os.Stdin)
	var changed []string
	for _, section := range defaultAgentSections {
		current := strings.TrimSpace(markdownSection(doc, section))
		fmt.Printf("\n## %s\n", section)
		if current == "" {
			fmt.Println("(empty)")
		} else {
			fmt.Println(current)
		}
		fmt.Print("[k]eep, [r]eplace line, [e]dit in $EDITOR, [d]elete: ")
		answer, _ := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "k", "keep":
			continue
		case "r", "replace":
			fmt.Print("New value: ")
			value, _ := reader.ReadString('\n')
			doc = setMarkdownSection(doc, section, strings.TrimRight(value, "\r\n"))
			changed = append(changed, section)
		case "e", "edit":
			value, err := editTextInEditor(current)
			if err != nil {
				return nil, err
			}
			doc = setMarkdownSection(doc, section, value)
			changed = append(changed, section)
		case "d", "delete":
			doc = setMarkdownSection(doc, section, "")
			changed = append(changed, section)
		default:
			fmt.Println("unrecognized action; keeping section")
		}
	}
	didChange := normalizedHash(data) != normalizedHash([]byte(doc))
	if didChange {
		if err := m.runner().WriteFile(path, []byte(doc), 0o644); err != nil {
			return nil, err
		}
	}
	return &AuthorResult{Path: path, Changed: didChange, Sections: changed}, nil
}

func (m *AgentsManager) renderTool(tool AgentTool, ssotBytes []byte) (string, bool, error) {
	out := string(stripManagedHeader(ssotBytes))
	overlayPath := filepath.Join(m.ssotDir(), "overlays", tool.OverlayFile)
	overlay, err := os.ReadFile(overlayPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, false, nil
		}
		return "", false, fmt.Errorf("read overlay %s: %w", overlayPath, err)
	}
	out = strings.TrimRight(out, "\n") + "\n\n" + fmt.Sprintf(agentsOverlayPattern, tool.ID) + "\n" + string(overlay)
	return out, true, nil
}

func (m *AgentsManager) resolveToolIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		for _, tool := range m.registry() {
			ids = append(ids, tool.ID)
		}
		return ids, nil
	}
	seen := make(map[string]bool, len(ids))
	var out []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		if _, ok := m.Tool(id); !ok {
			return nil, fmt.Errorf("unknown agents tool %q", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func (m *AgentsManager) readState() (*agentsState, error) {
	st := &agentsState{LastApplied: map[string]string{}}
	data, err := os.ReadFile(m.StatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	if st.LastApplied == nil {
		st.LastApplied = map[string]string{}
	}
	return st, nil
}

func (m *AgentsManager) writeState(st *agentsState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := m.runner().MkdirAll(m.ssotDir(), 0o755); err != nil {
		return err
	}
	return m.runner().WriteFile(m.StatePath(), data, 0o644)
}

func (m *AgentsManager) backupTarget(toolID, path string) (string, error) {
	dst := m.backupTargetPath(toolID)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	eng := &Engine{Runner: m.runner(), HomeDir: m.homeDir()}
	_, _, err = eng.copyTree(path, dst, info, filepath.Join("agents", toolID))
	if err != nil {
		return "", err
	}
	return dst, nil
}

func (m *AgentsManager) backupTargetPath(toolID string) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	return filepath.Join(m.homeDir(), ".local", "share", "dotfiles", "backup", "agents", ts, toolID)
}

func (m *AgentsManager) backupSSOT() (string, error) {
	path := m.SSOTPath()
	ts := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(m.homeDir(), ".local", "share", "dotfiles", "backup", "agents-ssot", ts, AgentsSSOTName)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	eng := &Engine{Runner: m.runner(), HomeDir: m.homeDir()}
	_, _, err = eng.copyTree(path, dst, info, filepath.Join("agents-ssot", AgentsSSOTName))
	if err != nil {
		return "", err
	}
	return dst, nil
}

func (m *AgentsManager) registry() []AgentTool {
	if len(m.Tools) > 0 {
		return m.Tools
	}
	return RegisteredAgentTools()
}

func (m *AgentsManager) runner() *dotexec.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return dotexec.NewRunner(false, slog.Default())
}

func (m *AgentsManager) homeDir() string {
	if m.HomeDir != "" {
		return m.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (m *AgentsManager) ssotDir() string {
	if m.SSOTDir != "" {
		return m.SSOTDir
	}
	return filepath.Join(m.homeDir(), AgentsSSOTRelPath)
}

func (m *AgentsManager) expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(m.homeDir(), path[2:])
	}
	if path == "~" {
		return m.homeDir()
	}
	return path
}

func normalizedHash(data []byte) string {
	sum := sha256.Sum256(stripManagedHeader(data))
	return hex.EncodeToString(sum[:])
}

func stripManagedHeader(data []byte) []byte {
	s := string(data)
	if strings.HasPrefix(s, agentsManagedHeader) {
		s = strings.TrimPrefix(s, agentsManagedHeader)
		s = strings.TrimLeft(s, "\r\n")
	}
	return []byte(s)
}

func setMarkdownSection(doc, section, value string) string {
	section = strings.TrimSpace(section)
	value = strings.TrimRight(value, "\r\n")
	lines := splitMarkdownLines(strings.TrimRight(doc, "\r\n"))
	start, end := findMarkdownSection(lines, section)
	replacement := []string{"## " + section}
	if value != "" {
		replacement = append(replacement, strings.Split(value, "\n")...)
	}
	if start >= 0 {
		next := append([]string{}, lines[:start]...)
		next = append(next, replacement...)
		next = append(next, lines[end:]...)
		return strings.TrimRight(strings.Join(next, "\n"), "\n") + "\n"
	}
	if len(lines) == 1 && lines[0] == "" {
		return strings.Join(replacement, "\n") + "\n"
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n\n" + strings.Join(replacement, "\n") + "\n"
}

func markdownSection(doc, section string) string {
	lines := splitMarkdownLines(strings.TrimRight(doc, "\r\n"))
	start, end := findMarkdownSection(lines, section)
	if start < 0 || start+1 >= end {
		return ""
	}
	return strings.Join(lines[start+1:end], "\n")
}

func findMarkdownSection(lines []string, section string) (int, int) {
	needle := strings.ToLower(strings.TrimSpace(section))
	start := -1
	for i, line := range lines {
		heading, ok := h2HeadingName(line)
		if !ok {
			continue
		}
		if strings.ToLower(heading) == needle {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if _, ok := h2HeadingName(lines[i]); ok {
			end = i
			break
		}
	}
	return start, end
}

func h2HeadingName(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), true
}

func splitMarkdownLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func numberLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	return b.String()
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func editTextInEditor(initial string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", fmt.Errorf("$EDITOR is not set")
	}
	tmp, err := os.CreateTemp("", "dotfiles-agents-section-*.md")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(initial); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cmd := osexec.CommandContext(context.Background(), editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func unifiedTextDiff(from, to, a, b string) string {
	if a == b {
		return ""
	}
	aLines := diffSplitLines(a)
	bLines := diffSplitLines(b)
	ops := diffOps(aLines, bLines)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@\n", from, to)
	for _, op := range ops {
		switch op.kind {
		case ' ':
			fmt.Fprintf(&out, " %s\n", op.text)
		case '-':
			fmt.Fprintf(&out, "-%s\n", op.text)
		case '+':
			fmt.Fprintf(&out, "+%s\n", op.text)
		}
	}
	return out.String()
}

type diffOp struct {
	kind byte
	text string
}

func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{kind: '-', text: a[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: '-', text: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: '+', text: b[j]})
	}
	return ops
}

func diffSplitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
