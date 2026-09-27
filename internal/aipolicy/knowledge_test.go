package aipolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeCodexRequiresExistingBindings(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Agent: "codex", Home: home}
	grants, err := KnowledgeApprovals(runtime)
	if err != nil || len(grants) != 0 {
		t.Fatalf("%+v %v", grants, err)
	}
	native := []byte("[mcp_servers.obsidian]\ncommand = \"obsidian-mcp\"\n[plugins.\"claude-mem@claude-mem-local\"]\nenabled = true\n[plugins.\"claude-mem@claude-mem-local\".mcp_servers.mcp-search.tools.search]\napproval_mode = \"ask\"\n")
	if err = os.WriteFile(filepath.Join(home, "config.toml"), native, 0600); err != nil {
		t.Fatal(err)
	}
	grants, err = KnowledgeApprovals(runtime)
	if err != nil || len(grants) != len(vaultKnowledgeTools)+len(memoryKnowledgeTools) {
		t.Fatalf("%+v %v", grants, err)
	}
	args, err := KnowledgeArgs(runtime, grants)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{"write_note", "observation_add", "observation_record_event", "approval_mode=\"approve\""} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	for _, excluded := range []string{"delete_note", "move_file", "move_note", "build_corpus", "prime_corpus", "command=", "url="} {
		if strings.Contains(joined, excluded) {
			t.Fatalf("unexpected %s", excluded)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || string(data) != string(native) {
		t.Fatal("native identity changed")
	}
}
func TestKnowledgeCodexNoPhantomBindings(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{
		"[mcp_servers.obsidian.tools.read_note]\napproval_mode = \"ask\"\n",
		"[mcp_servers.obsidian]\ncommand = \"obsidian-mcp\"\nenabled = false\n",
		"[\"mcp_servers.obsidian\"]\ncommand = \"not-a-server\"\n",
		"[plugins.\"claude-mem@claude-mem-local\"]\nenabled = false\n[plugins.\"claude-mem@claude-mem-local\".mcp_servers.mcp-search.tools.search]\napproval_mode = \"ask\"\n",
	} {
		if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		grants, err := KnowledgeApprovals(Runtime{Agent: "codex", Home: home})
		if err != nil || len(grants) != 0 {
			t.Fatalf("phantom grants %+v %v", grants, err)
		}
	}
}
func TestKnowledgeClaudePreciseGrants(t *testing.T) {
	runtime := Runtime{Agent: "claude"}
	grants, err := KnowledgeApprovals(runtime)
	if err != nil {
		t.Fatal(err)
	}
	args, err := KnowledgeArgs(runtime, grants)
	if err != nil || len(args) != 2 || args[0] != "--allowedTools" {
		t.Fatalf("%+v %v", args, err)
	}
	for _, name := range []string{"mcp__obsidian__write_note", "mcp__obsidian__patch_note", "mcp__plugin_claude-mem_mcp-search__observation_add"} {
		if !strings.Contains(args[1], name) {
			t.Fatal(name)
		}
	}
	for _, tool := range []string{"delete_note", "move_note", "build_corpus", "*"} {
		if _, err := KnowledgeArgs(runtime, []KnowledgeApproval{{Server: "obsidian", Tool: tool, ApprovalMode: "approve"}}); err == nil {
			t.Fatal("unsafe grant accepted")
		}
	}
}
func TestKnowledgeOverlayContainsOnlyPermissionLeaves(t *testing.T) {
	prefs, policy, inventory := preferenceFixture(t)
	policy.Targets[0].Agent = "codex"
	policy.Targets[0].Version = "0.157.1"
	home := filepath.Join(prefs.Home, ".codex")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.obsidian]\ncommand = \"private-executable\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory = []Runtime{{Agent: "codex", Home: home, Version: "0.157.1", Available: true, AutoReview: true, SubscriptionVerified: true}}
	changes, err := prefs.Apply(policy, inventory, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(changes[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"write_note\".approval_mode = \"approve\"") || strings.Contains(string(data), "private-executable") {
		t.Fatalf("%s", data)
	}
}

func TestKnowledgeCanonicalDeclaredHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.obsidian]\ncommand = \"obsidian-mcp\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	grants, err := KnowledgeApprovals(Runtime{Agent: "codex", Home: home})
	if err != nil || len(grants) != len(vaultKnowledgeTools) {
		t.Fatalf("%+v %v", grants, err)
	}
}

func TestKnowledgeEnabledPluginManifestOnly(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[plugins.\"claude-mem@claude-mem-local\"]\nenabled = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Agent: "codex", Home: home}
	if _, err := KnowledgeApprovals(runtime); err == nil {
		t.Fatal("unknown enabled binding silently ignored")
	}
	bin := filepath.Join(home, "fake-codex")
	script := "#!/bin/sh\n[ \"$*\" = \"plugin list --json --marketplace claude-mem-local\" ] || exit 44\nprintf '%s' '{\"installed\":[{\"pluginId\":\"claude-mem@claude-mem-local\",\"version\":\"13.28.0\",\"installed\":true}]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runtime.Executable = bin
	sharedPlugins := t.TempDir()
	if err := os.Symlink(sharedPlugins, filepath.Join(home, "plugins")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(sharedPlugins, "cache", "claude-mem-local", "claude-mem", "13.28.0")
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte("{\"mcpServers\":\"./.mcp.json\"}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{\"mcpServers\":{\"mcp-search\":{\"command\":\"node\",\"args\":[\"private-script\"]}}}"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	grants, err := KnowledgeApprovals(runtime)
	if err != nil || len(grants) != len(memoryKnowledgeTools) {
		t.Fatalf("%+v %v", grants, err)
	}
	args, err := KnowledgeArgs(runtime, grants)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "private-script") {
		t.Fatal("identity copied")
	}
	after, err := os.Stat(filepath.Join(root, ".mcp.json"))
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("shared native metadata modified")
	}
	// A symlink below the resolved read boundary cannot escape to another file.
	outside := filepath.Join(t.TempDir(), "foreign.json")
	if err = os.WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = KnowledgeApprovals(runtime); err == nil {
		t.Fatal("metadata symlink escaped installed plugin root")
	}
}

func TestKnowledgeCodexCLIUsesNativeRawSegments(t *testing.T) {
	runtime := Runtime{Agent: "codex"}
	grants := []KnowledgeApproval{
		{Server: "obsidian", Tool: "write_note", ApprovalMode: "approve"},
		{Plugin: "claude-mem@claude-mem-local", Server: "mcp-search", Tool: "observation_add", ApprovalMode: "approve"},
	}
	args, err := KnowledgeArgs(runtime, grants)
	if err != nil {
		t.Fatal(err)
	}
	// Model Codex's actual override-key parser: split raw key by dots. Quotes
	// are literal key characters here, although native TOML files need them.
	expected := [][]string{
		{"mcp_servers", "obsidian", "tools", "write_note", "approval_mode"},
		{"plugins", "claude-mem@claude-mem-local", "mcp_servers", "mcp-search", "tools", "observation_add", "approval_mode"},
	}
	for i, parts := range expected {
		if args[2*i] != "-c" {
			t.Fatalf("unexpected flag %q", args[2*i])
		}
		key, value, ok := strings.Cut(args[2*i+1], "=")
		if !ok || value != "\"approve\"" {
			t.Fatalf("invalid override %q", args[2*i+1])
		}
		actual := strings.Split(key, ".")
		if len(actual) != len(parts) {
			t.Fatalf("unexpected segments %q", actual)
		}
		for j, part := range parts {
			if actual[j] != part {
				t.Fatalf("phantom native key: got %q want %q", actual[j], part)
			}
		}
	}
	if !strings.Contains(knowledgeKey(grants[1]), "plugins.\"claude-mem@claude-mem-local\"") {
		t.Fatal("TOML file keys lost required quoting")
	}
}
