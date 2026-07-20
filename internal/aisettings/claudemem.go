package aisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	ClaudeMemLaunchdLabel = "com.dotfiles.claude-mem-bridge"
	memoryBlockStart      = "<!-- dotfiles:claude-mem:start -->"
	memoryBlockEnd        = "<!-- dotfiles:claude-mem:end -->"
)

const memoryInstructions = `<!-- dotfiles:claude-mem:start -->
## Persistent Memory

- Before non-trivial work, query the ` + "`claude-mem`" + ` MCP server when prior workspace context may affect the result.
- Treat retrieved memory as a lead, and verify drift-prone facts against the current workspace or live system.
- Codex hooks and the Kimi/Kiro transcript bridge capture session activity automatically. Do not duplicate it into separate memory files unless explicitly requested.
<!-- dotfiles:claude-mem:end -->`

// ClaudeMemManager manages the cross-CLI claude-mem integration. Codex uses
// the plugin's native hooks; Kimi and Kiro use MCP for recall and a transcript
// bridge for capture.
type ClaudeMemManager struct {
	HomeDir    string
	DotPath    string
	NodePath   string
	BunPath    string
	PluginRoot string
}

type ClaudeMemInstallResult struct {
	PluginRoot  string
	ConfigPaths []string
	BridgePath  string
	WatchCount  map[string]int
}

type ClaudeMemStatus struct {
	PluginRoot          string
	PluginVersion       string
	CodexNativeHooks    bool
	KimiMCP             bool
	KiroMCP             bool
	InstructionsEnabled bool
	BridgeInstalled     bool
	BridgeRunning       bool
	WatchCount          map[string]int
}

type transcriptWatchConfig struct {
	Version   int                         `json:"version"`
	Schemas   map[string]transcriptSchema `json:"schemas"`
	Watches   []transcriptWatch           `json:"watches"`
	StateFile string                      `json:"stateFile"`
}

type transcriptSchema struct {
	Name        string            `json:"name"`
	Version     string            `json:"version,omitempty"`
	Description string            `json:"description,omitempty"`
	Events      []transcriptEvent `json:"events"`
}

type transcriptEvent struct {
	Name   string         `json:"name"`
	Match  map[string]any `json:"match,omitempty"`
	Action string         `json:"action"`
	Fields map[string]any `json:"fields,omitempty"`
}

type transcriptWatch struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Schema     string `json:"schema"`
	Workspace  string `json:"workspace"`
	StartAtEnd bool   `json:"startAtEnd"`
}

func NewClaudeMemManager(homeDir, dotPath, nodePath string) *ClaudeMemManager {
	return &ClaudeMemManager{HomeDir: homeDir, DotPath: dotPath, NodePath: nodePath}
}

func (m *ClaudeMemManager) DataDir() string {
	return filepath.Join(m.HomeDir, ".claude-mem")
}

func (m *ClaudeMemManager) TranscriptConfigPath() string {
	return filepath.Join(m.DataDir(), "cross-cli-transcript-watch.json")
}

func (m *ClaudeMemManager) TranscriptStatePath() string {
	return filepath.Join(m.DataDir(), "cross-cli-transcript-watch-state.json")
}

func (m *ClaudeMemManager) LaunchdPlistPath() string {
	return filepath.Join(m.HomeDir, "Library", "LaunchAgents", ClaudeMemLaunchdLabel+".plist")
}

func (m *ClaudeMemManager) BridgeLogPath() string {
	return filepath.Join(m.DataDir(), "logs", "cross-cli-bridge.log")
}

func (m *ClaudeMemManager) KimiMCPPath() string {
	return filepath.Join(m.HomeDir, ".kimi-code", "mcp.json")
}

func (m *ClaudeMemManager) KiroMCPPath() string {
	return filepath.Join(m.HomeDir, ".kiro", "settings", "mcp.json")
}

// LocatePlugin resolves an installed claude-mem plugin without pinning a
// cache version. The Claude marketplace checkout is preferred because Codex,
// Kimi, and Kiro can all share it.
func (m *ClaudeMemManager) LocatePlugin() (string, error) {
	if m.PluginRoot != "" && isClaudeMemPlugin(m.PluginRoot) {
		return m.PluginRoot, nil
	}

	var candidates []string
	for _, root := range []string{os.Getenv("CLAUDE_PLUGIN_ROOT"), os.Getenv("PLUGIN_ROOT")} {
		if root != "" {
			candidates = append(candidates, root)
		}
	}
	candidates = append(candidates,
		filepath.Join(m.HomeDir, ".claude", "plugins", "marketplaces", "thedotmack", "plugin"),
	)
	for _, pattern := range []string{
		filepath.Join(m.HomeDir, ".codex", "plugins", "cache", "claude-mem-local", "claude-mem", "*"),
		filepath.Join(m.HomeDir, ".codex", "plugins", "cache", "thedotmack", "claude-mem", "*"),
		filepath.Join(m.HomeDir, ".claude", "plugins", "cache", "thedotmack", "claude-mem", "*"),
	} {
		matches, _ := filepath.Glob(pattern)
		sort.Slice(matches, func(i, j int) bool {
			left, _ := os.Stat(matches[i])
			right, _ := os.Stat(matches[j])
			if left == nil || right == nil {
				return matches[i] > matches[j]
			}
			return left.ModTime().After(right.ModTime())
		})
		candidates = append(candidates, matches...)
	}

	for _, candidate := range candidates {
		if isClaudeMemPlugin(candidate) {
			return candidate, nil
		}
		if isClaudeMemPlugin(filepath.Join(candidate, "plugin")) {
			return filepath.Join(candidate, "plugin"), nil
		}
	}
	return "", errors.New("claude-mem plugin not found; install it in Claude Code or Codex first")
}

func isClaudeMemPlugin(root string) bool {
	if root == "" {
		return false
	}
	for _, name := range []string{"mcp-server.cjs", "transcript-watcher.cjs", "bun-runner.js"} {
		if info, err := os.Stat(filepath.Join(root, "scripts", name)); err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// EnsureMemoryInstructions adds the idempotent recall/capture policy to the
// shared AGENTS SSOT. Rendering remains owned by AgentsManager.
func EnsureMemoryInstructions(ssotPath string) (bool, error) {
	raw, err := os.ReadFile(ssotPath)
	if err != nil {
		return false, fmt.Errorf("read agents SSOT: %w", err)
	}
	text := string(raw)
	start := strings.Index(text, memoryBlockStart)
	end := strings.Index(text, memoryBlockEnd)
	next := text
	if start >= 0 && end >= start {
		end += len(memoryBlockEnd)
		next = text[:start] + memoryInstructions + text[end:]
	} else {
		next = strings.TrimRight(text, "\n") + "\n\n" + memoryInstructions + "\n"
	}
	if next == text {
		return false, nil
	}
	return true, atomicWriteFile(ssotPath, []byte(next), fileModeOrDefault(ssotPath, 0o644))
}

func HasMemoryInstructions(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && bytes.Contains(raw, []byte(memoryBlockStart)) && bytes.Contains(raw, []byte(memoryBlockEnd))
}

// BuildTranscriptConfig discovers concrete session files so each transcript is
// associated with the workspace recorded in its Kimi/Kiro sidecar metadata.
func (m *ClaudeMemManager) BuildTranscriptConfig() (transcriptWatchConfig, error) {
	watches := append(m.kimiWatches(), m.kiroWatches()...)
	sort.Slice(watches, func(i, j int) bool { return watches[i].Path < watches[j].Path })
	return transcriptWatchConfig{
		Version: 1,
		Schemas: map[string]transcriptSchema{
			"kimi": kimiTranscriptSchema(),
			"kiro": kiroTranscriptSchema(),
		},
		Watches:   watches,
		StateFile: m.TranscriptStatePath(),
	}, nil
}

// BuildTranscriptConfigForDisplay returns discovery counts without exposing
// the bridge's internal schema representation to the CLI package.
func (m *ClaudeMemManager) BuildTranscriptConfigForDisplay() (map[string]int, error) {
	config, err := m.BuildTranscriptConfig()
	if err != nil {
		return nil, err
	}
	return countWatches(config.Watches), nil
}

func (m *ClaudeMemManager) kimiWatches() []transcriptWatch {
	pattern := filepath.Join(m.HomeDir, ".kimi-code", "sessions", "*", "session_*", "state.json")
	stateFiles, _ := filepath.Glob(pattern)
	var watches []transcriptWatch
	for _, statePath := range stateFiles {
		var state struct {
			WorkDir string `json:"workDir"`
		}
		if !readJSONFile(statePath, &state) || !isAbsoluteDirectory(state.WorkDir) {
			continue
		}
		wirePath := filepath.Join(filepath.Dir(statePath), "agents", "main", "wire.jsonl")
		if info, err := os.Stat(wirePath); err != nil || info.IsDir() {
			continue
		}
		watches = append(watches, transcriptWatch{
			Name: "kimi", Path: wirePath, Schema: "kimi", Workspace: state.WorkDir, StartAtEnd: false,
		})
	}
	return watches
}

func (m *ClaudeMemManager) kiroWatches() []transcriptWatch {
	pattern := filepath.Join(m.HomeDir, ".kiro", "sessions", "*", "sess_*", "session.json")
	stateFiles, _ := filepath.Glob(pattern)
	var watches []transcriptWatch
	for _, statePath := range stateFiles {
		var state struct {
			WorkspacePaths []string `json:"workspacePaths"`
		}
		if !readJSONFile(statePath, &state) || len(state.WorkspacePaths) == 0 || !isAbsoluteDirectory(state.WorkspacePaths[0]) {
			continue
		}
		messagesPath := filepath.Join(filepath.Dir(statePath), "messages.jsonl")
		if info, err := os.Stat(messagesPath); err != nil || info.IsDir() {
			continue
		}
		watches = append(watches, transcriptWatch{
			Name: "kiro", Path: messagesPath, Schema: "kiro", Workspace: state.WorkspacePaths[0], StartAtEnd: false,
		})
	}
	return watches
}

func readJSONFile(path string, target any) bool {
	raw, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(raw, target) == nil
}

func isAbsoluteDirectory(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func kimiTranscriptSchema() transcriptSchema {
	return transcriptSchema{
		Name: "kimi", Version: "0.27", Description: "Kimi Code wire.jsonl session events.",
		Events: []transcriptEvent{
			{Name: "user-prompt", Match: equals("type", "turn.prompt"), Action: "session_init", Fields: map[string]any{"prompt": "input[0].text"}},
			{Name: "assistant-text", Match: equals("event.part.type", "text"), Action: "assistant_message", Fields: map[string]any{"message": "event.part.text"}},
			{Name: "tool-call", Match: equals("event.type", "tool.call"), Action: "tool_use", Fields: map[string]any{"toolId": "event.toolCallId", "toolName": "event.name", "toolInput": "event.args"}},
			{Name: "tool-result", Match: equals("event.type", "tool.result"), Action: "tool_result", Fields: map[string]any{"toolId": "event.toolCallId", "toolResponse": map[string]any{"coalesce": []any{"event.result.output", "event.result"}}}},
			{Name: "cancelled-turn", Match: equals("type", "turn.cancel"), Action: "session_end"},
		},
	}
}

func kiroTranscriptSchema() transcriptSchema {
	return transcriptSchema{
		Name: "kiro", Version: "2.13", Description: "Kiro CLI messages.jsonl session events.",
		Events: []transcriptEvent{
			{Name: "user-prompt", Match: equals("payload.type", "user"), Action: "session_init", Fields: map[string]any{"prompt": "payload.content"}},
			{Name: "assistant-message", Match: equals("payload.type", "assistant"), Action: "assistant_message", Fields: map[string]any{"message": "payload.content"}},
			{Name: "tool-call", Match: equals("payload.type", "tool_call"), Action: "tool_use", Fields: map[string]any{"toolId": "payload.toolCallId", "toolName": "payload.toolName", "toolInput": "payload.args"}},
			{Name: "tool-result", Match: equals("payload.type", "tool_result"), Action: "tool_result", Fields: map[string]any{"toolId": "payload.toolCallId", "toolResponse": "payload.content"}},
			{Name: "turn-end", Match: equals("payload.type", "turn_end"), Action: "session_end"},
		},
	}
}

func equals(path string, value any) map[string]any {
	return map[string]any{"path": path, "equals": value}
}

func (m *ClaudeMemManager) writeTranscriptConfig(config transcriptWatchConfig) (bool, error) {
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append(raw, '\n')
	return writeIfChanged(m.TranscriptConfigPath(), raw, 0o600)
}

func (m *ClaudeMemManager) seedTranscriptState(config transcriptWatchConfig) error {
	if _, err := os.Stat(m.TranscriptStatePath()); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	offsets := make(map[string]int64, len(config.Watches))
	for _, watch := range config.Watches {
		if info, err := os.Stat(watch.Path); err == nil {
			offsets[watch.Path] = info.Size()
		}
	}
	raw, err := json.MarshalIndent(map[string]any{"offsets": offsets}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(m.TranscriptStatePath(), append(raw, '\n'), 0o600)
}

// Install wires recall into Kimi/Kiro, prepares transcript capture, and loads
// the macOS user LaunchAgent that keeps new sessions discovered.
func (m *ClaudeMemManager) Install(ctx context.Context) (ClaudeMemInstallResult, error) {
	pluginRoot, err := m.LocatePlugin()
	if err != nil {
		return ClaudeMemInstallResult{}, err
	}
	if !codexClaudeMemEnabled(filepath.Join(m.HomeDir, ".codex", "config.toml")) {
		return ClaudeMemInstallResult{}, errors.New("Codex claude-mem plugin is not enabled; run `codex plugin add claude-mem@claude-mem-local` first")
	}
	if m.DotPath == "" || !filepath.IsAbs(m.DotPath) {
		return ClaudeMemInstallResult{}, errors.New("dot executable path must be absolute")
	}
	if m.NodePath == "" || !filepath.IsAbs(m.NodePath) {
		return ClaudeMemInstallResult{}, errors.New("node executable path must be absolute")
	}
	if m.BunPath == "" || !filepath.IsAbs(m.BunPath) {
		return ClaudeMemInstallResult{}, errors.New("bun executable path must be absolute")
	}

	var changedPaths []string
	for _, target := range []struct {
		path string
		kiro bool
	}{{m.KimiMCPPath(), false}, {m.KiroMCPPath(), true}} {
		changed, err := ensureMCPEntry(target.path, m.DotPath, target.kiro)
		if err != nil {
			return ClaudeMemInstallResult{}, err
		}
		if changed {
			changedPaths = append(changedPaths, target.path)
		}
	}

	config, err := m.BuildTranscriptConfig()
	if err != nil {
		return ClaudeMemInstallResult{}, err
	}
	if _, err := m.writeTranscriptConfig(config); err != nil {
		return ClaudeMemInstallResult{}, err
	}
	if err := m.seedTranscriptState(config); err != nil {
		return ClaudeMemInstallResult{}, fmt.Errorf("seed transcript state: %w", err)
	}
	if err := m.installLaunchAgent(ctx); err != nil {
		return ClaudeMemInstallResult{}, err
	}

	return ClaudeMemInstallResult{
		PluginRoot: pluginRoot, ConfigPaths: changedPaths, BridgePath: m.LaunchdPlistPath(), WatchCount: countWatches(config.Watches),
	}, nil
}

func ensureMCPEntry(path, dotPath string, kiro bool) (bool, error) {
	doc := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return false, fmt.Errorf("parse MCP config %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	servers := map[string]json.RawMessage{}
	if raw := doc["mcpServers"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return false, fmt.Errorf("parse mcpServers in %s: %w", path, err)
		}
	}
	entry := map[string]any{"command": dotPath, "args": []string{"ai", "memory", "mcp-server"}}
	if kiro {
		entry["disabled"] = false
	}
	entryRaw, _ := json.Marshal(entry)
	var currentEntry map[string]any
	if json.Unmarshal(servers["claude-mem"], &currentEntry) == nil {
		currentRaw, _ := json.Marshal(currentEntry)
		if bytes.Equal(currentRaw, entryRaw) {
			return false, nil
		}
	}
	servers["claude-mem"] = entryRaw
	serversRaw, _ := json.Marshal(servers)
	doc["mcpServers"] = serversRaw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	return true, atomicWriteFile(path, append(out, '\n'), fileModeOrDefault(path, 0o600))
}

func (m *ClaudeMemManager) installLaunchAgent(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return errors.New("claude-mem bridge service installation currently requires macOS launchd")
	}
	for _, dir := range []string{filepath.Dir(m.LaunchdPlistPath()), filepath.Dir(m.BridgeLogPath())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>ai</string>
    <string>memory</string>
    <string>bridge</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>%s</string>
    <key>PATH</key>
    <string>%s:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>5</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, ClaudeMemLaunchdLabel, xmlEscape(m.DotPath), xmlEscape(m.HomeDir), xmlEscape(filepath.Dir(m.NodePath)), xmlEscape(m.BridgeLogPath()), xmlEscape(m.BridgeLogPath()))
	if err := atomicWriteFile(m.LaunchdPlistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write launch agent: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	target := domain + "/" + ClaudeMemLaunchdLabel
	_ = exec.CommandContext(ctx, "launchctl", "bootout", target).Run()
	var bootstrapErr error
	var bootstrapOut []byte
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
		}
		bootstrapOut, bootstrapErr = exec.CommandContext(ctx, "launchctl", "bootstrap", domain, m.LaunchdPlistPath()).CombinedOutput()
		if bootstrapErr == nil || exec.CommandContext(ctx, "launchctl", "print", target).Run() == nil {
			bootstrapErr = nil
			break
		}
	}
	if bootstrapErr != nil {
		return fmt.Errorf("bootstrap claude-mem bridge: %w: %s", bootstrapErr, strings.TrimSpace(string(bootstrapOut)))
	}
	if out, err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", target).CombinedOutput(); err != nil {
		return fmt.Errorf("start claude-mem bridge: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xmlEscape(value string) string {
	return html.EscapeString(value)
}

// RunMCPServer attaches the current terminal to claude-mem's stdio MCP server.
func (m *ClaudeMemManager) RunMCPServer(ctx context.Context) error {
	if m.NodePath == "" || !filepath.IsAbs(m.NodePath) {
		return errors.New("node executable path must be absolute")
	}
	pluginRoot, err := m.LocatePlugin()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, m.NodePath, filepath.Join(pluginRoot, "scripts", "mcp-server.cjs"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// RunBridge supervises the claude-mem transcript watcher and reloads it when a
// newly created Kimi or Kiro session adds a concrete workspace mapping.
func (m *ClaudeMemManager) RunBridge(ctx context.Context) error {
	if m.BunPath == "" || !filepath.IsAbs(m.BunPath) {
		return errors.New("bun executable path must be absolute")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var child *exec.Cmd
	var done chan error
	var signature string
	start := func() error {
		pluginRoot, err := m.LocatePlugin()
		if err != nil {
			return err
		}
		config, err := m.BuildTranscriptConfig()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(config.Watches)
		nextSignature := pluginRoot + "\n" + string(raw)
		if child != nil && nextSignature == signature {
			return nil
		}
		if child != nil {
			stopChild(child, done)
			child, done = nil, nil
		}
		if _, err := m.writeTranscriptConfig(config); err != nil {
			return err
		}
		child = exec.CommandContext(ctx, m.BunPath,
			filepath.Join(pluginRoot, "scripts", "transcript-watcher.cjs"),
			"watch", "--config", m.TranscriptConfigPath(),
		)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			child = nil
			return err
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd, ch chan<- error) { ch <- cmd.Wait() }(child, done)
		signature = nextSignature
		return nil
	}

	if err := start(); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if child != nil {
				stopChild(child, done)
			}
			return nil
		case err := <-done:
			child, done, signature = nil, nil, ""
			if ctx.Err() == nil && err != nil {
				fmt.Fprintf(os.Stderr, "claude-mem transcript watcher exited: %v\n", err)
			}
		case <-ticker.C:
			if err := start(); err != nil {
				fmt.Fprintf(os.Stderr, "claude-mem bridge rescan failed: %v\n", err)
			}
		}
	}
}

func stopChild(child *exec.Cmd, done <-chan error) {
	if child == nil || child.Process == nil {
		return
	}
	_ = child.Process.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = child.Process.Kill()
		<-done
	}
}

func (m *ClaudeMemManager) Status(ctx context.Context, ssotPath string) ClaudeMemStatus {
	status := ClaudeMemStatus{WatchCount: map[string]int{}}
	if pluginRoot, err := m.LocatePlugin(); err == nil {
		status.PluginRoot = pluginRoot
		status.CodexNativeHooks = fileExists(filepath.Join(pluginRoot, "hooks", "codex-hooks.json")) &&
			codexClaudeMemEnabled(filepath.Join(m.HomeDir, ".codex", "config.toml"))
		var manifest struct {
			Version string `json:"version"`
		}
		if readJSONFile(filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), &manifest) || readJSONFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), &manifest) {
			status.PluginVersion = manifest.Version
		}
	}
	status.KimiMCP = hasManagedMCPEntry(m.KimiMCPPath(), m.DotPath)
	status.KiroMCP = hasManagedMCPEntry(m.KiroMCPPath(), m.DotPath)
	status.InstructionsEnabled = HasMemoryInstructions(ssotPath)
	status.BridgeInstalled = fileExists(m.LaunchdPlistPath())
	if config, err := m.BuildTranscriptConfig(); err == nil {
		status.WatchCount = countWatches(config.Watches)
	}
	if runtime.GOOS == "darwin" {
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), ClaudeMemLaunchdLabel)
		status.BridgeRunning = exec.CommandContext(ctx, "launchctl", "print", target).Run() == nil
	}
	return status
}

func codexClaudeMemEnabled(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inClaudeMemPlugin := false
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "[") {
			inClaudeMemPlugin = strings.HasPrefix(line, `[plugins."claude-mem@`) && strings.HasSuffix(line, `"]`)
			continue
		}
		if inClaudeMemPlugin && line == "enabled = true" {
			return true
		}
	}
	return false
}

func hasManagedMCPEntry(path, dotPath string) bool {
	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if !readJSONFile(path, &doc) {
		return false
	}
	entry, ok := doc.MCPServers["claude-mem"]
	return ok && entry.Command == dotPath && len(entry.Args) == 3 && entry.Args[0] == "ai" && entry.Args[1] == "memory" && entry.Args[2] == "mcp-server"
}

func countWatches(watches []transcriptWatch) map[string]int {
	counts := map[string]int{"kimi": 0, "kiro": 0}
	for _, watch := range watches {
		counts[watch.Name]++
	}
	return counts
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileModeOrDefault(path string, fallback fs.FileMode) fs.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}

func writeIfChanged(path string, content []byte, mode fs.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) {
		return false, nil
	}
	return true, atomicWriteFile(path, content, mode)
}

func atomicWriteFile(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
