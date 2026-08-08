package aisettings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeMemBuildTranscriptConfigUsesSessionWorkspaces(t *testing.T) {
	home := t.TempDir()
	kimiWorkspace := filepath.Join(home, "work", "kimi-project")
	kiroWorkspace := filepath.Join(home, "work", "kiro-project")
	mustMkdirAll(t, kimiWorkspace)
	mustMkdirAll(t, kiroWorkspace)

	kimiSession := filepath.Join(home, ".kimi-code", "sessions", "wd_test", "session_11111111-1111-1111-1111-111111111111")
	mustWriteJSON(t, filepath.Join(kimiSession, "state.json"), map[string]any{"workDir": kimiWorkspace})
	mustWriteFile(t, filepath.Join(kimiSession, "agents", "main", "wire.jsonl"), "{}\n")

	kiroSession := filepath.Join(home, ".kiro", "sessions", "workspace", "sess_22222222-2222-2222-2222-222222222222")
	mustWriteJSON(t, filepath.Join(kiroSession, "session.json"), map[string]any{"workspacePaths": []string{kiroWorkspace}})
	mustWriteFile(t, filepath.Join(kiroSession, "messages.jsonl"), "{}\n")

	mgr := NewClaudeMemManager(home, filepath.Join(home, "bin", "dot"), filepath.Join(home, "bin", "node"))
	config, err := mgr.BuildTranscriptConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Watches) != 2 {
		t.Fatalf("watches = %d, want 2: %+v", len(config.Watches), config.Watches)
	}
	counts := countWatches(config.Watches)
	if counts["kimi"] != 1 || counts["kiro"] != 1 {
		t.Fatalf("watch counts = %#v", counts)
	}
	byName := map[string]transcriptWatch{}
	for _, watch := range config.Watches {
		byName[watch.Name] = watch
		if watch.StartAtEnd {
			t.Fatalf("%s watch must replay a newly discovered session from offset zero", watch.Name)
		}
	}
	if byName["kimi"].Workspace != kimiWorkspace || byName["kiro"].Workspace != kiroWorkspace {
		t.Fatalf("workspace mapping wrong: %#v", byName)
	}

	raw, err := json.Marshal(config.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"turn.prompt", "event.toolCallId", "payload.toolCallId", "turn_end"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("schemas missing %q: %s", want, raw)
		}
	}
}

func TestClaudeMemMCPMergePreservesOtherServers(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".kimi-code", "mcp.json")
	mustWriteJSON(t, path, map[string]any{
		"mcpServers": map[string]any{
			"obsidian": map[string]any{"command": "mcpvault", "args": []string{"vault"}},
		},
		"custom": true,
	})
	dotPath := filepath.Join(home, ".local", "bin", "dot")
	changed, err := ensureMCPEntry(path, dotPath, mcpVariantStandard)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first merge reported no change")
	}
	var got struct {
		Custom     bool `json:"custom"`
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if !readJSONFile(path, &got) {
		t.Fatal("merged MCP config did not parse")
	}
	if !got.Custom || got.MCPServers["obsidian"].Command != "mcpvault" {
		t.Fatalf("unrelated config was not preserved: %#v", got)
	}
	entry := got.MCPServers["claude-mem"]
	if entry.Command != dotPath || strings.Join(entry.Args, " ") != "ai memory mcp-server" {
		t.Fatalf("claude-mem entry = %#v", entry)
	}
	changed, err = ensureMCPEntry(path, dotPath, mcpVariantStandard)
	if err != nil || changed {
		t.Fatalf("second merge changed=%v err=%v, want idempotent", changed, err)
	}
}

func TestClaudeMemInstructionsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	mustWriteFile(t, path, "# AI Agents\n\n## Existing\n\n- keep\n")
	changed, err := EnsureMemoryInstructions(path)
	if err != nil || !changed {
		t.Fatalf("first ensure changed=%v err=%v", changed, err)
	}
	changed, err = EnsureMemoryInstructions(path)
	if err != nil || changed {
		t.Fatalf("second ensure changed=%v err=%v", changed, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), memoryBlockStart) != 1 || !strings.Contains(string(raw), "## Existing") {
		t.Fatalf("managed block or existing content wrong:\n%s", raw)
	}
}

func TestClaudeMemLocatePluginPrefersMarketplaceCheckout(t *testing.T) {
	home := t.TempDir()
	plugin := filepath.Join(home, ".claude", "plugins", "marketplaces", "thedotmack", "plugin")
	for _, name := range []string{"mcp-server.cjs", "transcript-watcher.cjs", "bun-runner.js"} {
		mustWriteFile(t, filepath.Join(plugin, "scripts", name), "")
	}
	mgr := NewClaudeMemManager(home, "/bin/dot", "/bin/node")
	got, err := mgr.LocatePlugin()
	if err != nil {
		t.Fatal(err)
	}
	if got != plugin {
		t.Fatalf("plugin = %q, want %q", got, plugin)
	}
}

func TestCodexClaudeMemEnabledAcceptsAnyMarketplaceAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	mustWriteFile(t, path, `[plugins."claude-mem@thedotmack"]
enabled = false

[plugins."claude-mem@local"]
enabled = true

[plugins."other@local"]
enabled = false
`)
	if !codexClaudeMemEnabled(path) {
		t.Fatal("enabled local claude-mem plugin was not detected")
	}
	mustWriteFile(t, path, `[plugins."claude-mem@local"]
enabled = false
`)
	if codexClaudeMemEnabled(path) {
		t.Fatal("disabled claude-mem plugin reported enabled")
	}
}

func TestCopilotMCPEntryHasTypeAndTools(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".copilot", "mcp-config.json")
	mustWriteJSON(t, path, map[string]any{
		"mcpServers": map[string]any{
			"other": map[string]any{"command": "other-cmd", "args": []string{"arg1"}},
		},
	})
	dotPath := filepath.Join(home, ".local", "bin", "dot")
	changed, err := ensureMCPEntry(path, dotPath, mcpVariantCopilot)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first merge reported no change")
	}
	var got struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Type    string   `json:"type"`
			Tools   []string `json:"tools"`
		} `json:"mcpServers"`
	}
	if !readJSONFile(path, &got) {
		t.Fatal("merged Copilot MCP config did not parse")
	}
	entry := got.MCPServers["claude-mem"]
	if entry.Command != dotPath {
		t.Fatalf("command = %q, want %q", entry.Command, dotPath)
	}
	if strings.Join(entry.Args, " ") != "ai memory mcp-server" {
		t.Fatalf("args = %v, want [ai memory mcp-server]", entry.Args)
	}
	if entry.Type != "local" {
		t.Fatalf("type = %q, want \"local\"", entry.Type)
	}
	if len(entry.Tools) != 1 || entry.Tools[0] != "*" {
		t.Fatalf("tools = %v, want [\"*\"]", entry.Tools)
	}
	// other entries must be preserved
	if got.MCPServers["other"].Command != "other-cmd" {
		t.Fatalf("unrelated entry was not preserved: %#v", got.MCPServers)
	}
	// idempotency
	changed, err = ensureMCPEntry(path, dotPath, mcpVariantCopilot)
	if err != nil || changed {
		t.Fatalf("second merge changed=%v err=%v, want idempotent", changed, err)
	}
}

func TestCopilotWatchesDiscoversSessionWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "projects", "myrepo")
	mustMkdirAll(t, workspace)

	sessionDir := filepath.Join(home, ".copilot", "session-state", "sess_abc123")
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	startEvent := `{"type":"session.start","data":{"context":{"cwd":"` + workspace + `","gitRoot":"` + workspace + `"}}}`
	mustWriteFile(t, eventsPath, startEvent+"\n")

	mgr := NewClaudeMemManager(home, filepath.Join(home, "bin", "dot"), filepath.Join(home, "bin", "node"))
	config, err := mgr.BuildTranscriptConfig()
	if err != nil {
		t.Fatal(err)
	}
	counts := countWatches(config.Watches)
	if counts["copilot"] != 1 {
		t.Fatalf("copilot watch count = %d, want 1: %+v", counts["copilot"], config.Watches)
	}
	var found transcriptWatch
	for _, w := range config.Watches {
		if w.Name == "copilot" {
			found = w
		}
	}
	if found.Workspace != workspace {
		t.Fatalf("copilot watch workspace = %q, want %q", found.Workspace, workspace)
	}
	if found.Path != eventsPath {
		t.Fatalf("copilot watch path = %q, want %q", found.Path, eventsPath)
	}
	if found.StartAtEnd {
		t.Fatal("copilot watch must replay a newly discovered session from offset zero")
	}
}

func TestCopilotWatchesSkipsEventsWithoutSessionStart(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "projects", "myrepo")
	mustMkdirAll(t, workspace)

	// events.jsonl whose first line is NOT a session.start event
	sessionDir := filepath.Join(home, ".copilot", "session-state", "sess_nostart")
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	mustWriteFile(t, eventsPath, `{"type":"user","data":{"message":"hello"}}`+"\n")

	mgr := NewClaudeMemManager(home, filepath.Join(home, "bin", "dot"), filepath.Join(home, "bin", "node"))
	config, err := mgr.BuildTranscriptConfig()
	if err != nil {
		t.Fatal(err)
	}
	counts := countWatches(config.Watches)
	if counts["copilot"] != 0 {
		t.Fatalf("copilot watch count = %d, want 0 for missing session.start", counts["copilot"])
	}
}

func TestCopilotTranscriptSchemaFields(t *testing.T) {
	schema := copilotTranscriptSchema()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session.end", "tool_call", "tool_result"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("copilot schema missing %q: %s", want, raw)
		}
	}
	if schema.Name != "copilot" {
		t.Fatalf("schema.Name = %q, want \"copilot\"", schema.Name)
	}
	// user-message should map to session_init (matching kimi/kiro pattern)
	found := false
	for _, ev := range schema.Events {
		if ev.Name == "user-message" && ev.Action == "session_init" {
			found = true
		}
	}
	if !found {
		t.Fatal("copilot schema: user-message event must use session_init action")
	}
}

func TestBuildTranscriptConfigIncludesCopilotInCounts(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "projects", "repo")
	mustMkdirAll(t, workspace)

	sessionDir := filepath.Join(home, ".copilot", "session-state", "sess_xyz")
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	startEvent := `{"type":"session.start","data":{"context":{"cwd":"` + workspace + `"}}}`
	mustWriteFile(t, eventsPath, startEvent+"\n")

	mgr := NewClaudeMemManager(home, filepath.Join(home, "bin", "dot"), filepath.Join(home, "bin", "node"))
	cfg, err := mgr.BuildTranscriptConfig()
	if err != nil {
		t.Fatal(err)
	}
	counts := countWatches(cfg.Watches)
	if _, ok := counts["copilot"]; !ok {
		t.Fatal("countWatches result is missing the \"copilot\" key")
	}
	if counts["copilot"] != 1 {
		t.Fatalf("copilot count = %d, want 1", counts["copilot"])
	}
	// Schemas map must contain "copilot"
	if _, ok := cfg.Schemas["copilot"]; !ok {
		t.Fatal("BuildTranscriptConfig schemas missing \"copilot\"")
	}
}

func mustWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, string(raw))
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestTranscriptConfigWatchesIsArrayWhenEmpty pins the JSON shape, not the Go
// value: a nil slice marshals to `null`, and the plugin's watcher rejects that
// as an invalid config. A machine with no Kimi/Kiro sessions is exactly the
// fresh-install case, so this failed only where it mattered most.
func TestTranscriptConfigWatchesIsArrayWhenEmpty(t *testing.T) {
	m := &ClaudeMemManager{HomeDir: t.TempDir()} // no kimi/kiro sessions
	cfg, err := m.BuildTranscriptConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Watches) != 0 {
		t.Fatalf("expected no watches, got %d", len(cfg.Watches))
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"watches":null`)) {
		t.Errorf("watches serialized as null; the plugin rejects that config:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte(`"watches":[]`)) {
		t.Errorf("watches is not an empty array:\n%s", raw)
	}
}
