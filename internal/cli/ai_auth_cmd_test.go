package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNeedsAuth(t *testing.T) {
	// The live file is `{}` most of the time; the populated value schema is
	// undocumented, so the parser must tolerate any JSON value shape.
	names, err := parseNeedsAuth([]byte(`{"cloudflare-builds":{"at":1},"cloudflare-bindings":"stale"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Join(names, ",") != "cloudflare-bindings,cloudflare-builds" {
		t.Fatalf("names = %v, want sorted cloudflare pair", names)
	}
	if got, err := parseNeedsAuth([]byte(`{}`)); err != nil || got != nil {
		t.Fatalf("empty cache = (%v,%v), want (nil,nil)", got, err)
	}
	if _, err := parseNeedsAuth([]byte(`[]`)); err == nil {
		t.Fatal("non-object cache should error")
	}
}

func TestParseMCPServers(t *testing.T) {
	body := `{
  "someOtherKey": 1,
  "mcpServers": {
    "github": {"command": "gh", "args": ["mcp"]},
    "Neon": {"type": "http", "url": "https://mcp.neon.tech/mcp"}
  }
}`
	servers, err := parseMCPServers([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(servers) != 2 || servers[0].Name != "Neon" {
		t.Fatalf("servers = %+v, want sorted [Neon github]", servers)
	}
	if servers[0].URL != "https://mcp.neon.tech/mcp" || servers[0].Type != "http" {
		t.Fatalf("http server fields lost: %+v", servers[0])
	}
	if servers[1].detail() != "stdio" {
		t.Fatalf("stdio server detail = %q", servers[1].detail())
	}
	if got, err := parseMCPServers([]byte(`{}`)); err != nil || got != nil {
		t.Fatalf("missing mcpServers = (%v,%v), want (nil,nil)", got, err)
	}
}

func TestAIAuthStatusListsPendingServers(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".claude", "mcp-needs-auth-cache.json"),
		`{"cloudflare-bindings":{},"claude.ai Canva":{}}`)
	writeCLITestFile(t, filepath.Join(home, ".claude.json"), `{
  "mcpServers": {
    "cloudflare-bindings": {"type": "http", "url": "https://bindings.mcp.cloudflare.com/mcp"},
    "github": {"command": "gh"}
  }
}`)

	out, errOut, err := runDotForTest("--home", home, "ai", "auth", "status")
	if err != nil {
		t.Fatalf("auth status: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "needs auth") {
		t.Fatalf("pending server not flagged:\n%s", out)
	}
	if !strings.Contains(out, "github") {
		t.Fatalf("configured server missing from status:\n%s", out)
	}
	// A connector that only exists in the pending cache must still be listed.
	if !strings.Contains(out, "claude.ai Canva") {
		t.Fatalf("pending-only connector missing from status:\n%s", out)
	}

	jsonOut, errOut, err := runDotForTest("--home", home, "ai", "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status json: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(jsonOut, `"pending_count": 2`) {
		t.Fatalf("json missing pending count:\n%s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"needs_auth": true`) {
		t.Fatalf("json missing needs_auth flag:\n%s", jsonOut)
	}
}

func TestAIAuthStatusEmptyHome(t *testing.T) {
	out, errOut, err := runDotForTest("--home", t.TempDir(), "ai", "auth", "status")
	if err != nil {
		t.Fatalf("auth status on empty home should succeed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "no MCP servers configured") {
		t.Fatalf("unexpected empty-home output:\n%s", out)
	}
}

func TestAIAuthLoginRequiresServerSelection(t *testing.T) {
	_, _, err := runDotForTest("--home", t.TempDir(), "ai", "auth", "login")
	if err == nil {
		t.Fatal("login without servers or --all-needed should error")
	}
}

func TestAIAuthRejectsUnknownTool(t *testing.T) {
	_, _, err := runDotForTest("--home", t.TempDir(), "ai", "auth", "relogin", "srv", "--tool", "gemini", "--yes")
	if err == nil {
		t.Fatal("unknown --tool should error")
	}
}

func TestAIAuthAllNeededIsClaudeOnly(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".claude", "mcp-needs-auth-cache.json"), `{"srv":{}}`)
	// Claude's pending cache says nothing about codex servers.
	_, _, err := runDotForTest("--home", home, "ai", "auth", "login", "--all-needed", "--tool", "codex")
	if err == nil {
		t.Fatal("--all-needed with --tool codex should error")
	}
}

func TestAIAuthAllNeededRejectsExplicitServers(t *testing.T) {
	_, _, err := runDotForTest("--home", t.TempDir(), "ai", "auth", "login", "--all-needed", "srv")
	if err == nil {
		t.Fatal("--all-needed combined with server args should error")
	}
}
