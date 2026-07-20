package aisettings

import (
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
	changed, err := ensureMCPEntry(path, dotPath, false)
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
	changed, err = ensureMCPEntry(path, dotPath, false)
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
