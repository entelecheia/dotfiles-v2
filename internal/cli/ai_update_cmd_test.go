package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures mirror the real v2 installed_plugins.json, including a plugin whose
// version is the literal "unknown" — claude resolves update availability from
// each plugin's own manifest, so dot records rather than interprets these.
const installedPluginsFixture = `{
  "version": 2,
  "plugins": {
    "frontend-design@claude-plugins-official": [
      {
        "scope": "user",
        "version": "unknown",
        "gitCommitSha": "82a73a367be4991ff22e2b43317b3956933c9f9a"
      }
    ],
    "superpowers@claude-plugins-official": [
      {
        "scope": "user",
        "version": "6.2.0",
        "gitCommitSha": "8ea39819eed74fe2a0338e71789f06b30e953041"
      }
    ],
    "local-plugin@local-market": [
      {
        "scope": "project",
        "version": "1.0.0",
        "gitCommitSha": ""
      }
    ]
  }
}`

func TestParseInstalledPlugins(t *testing.T) {
	plugins, err := parseInstalledPlugins([]byte(installedPluginsFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(plugins) != 3 {
		t.Fatalf("got %d plugins, want 3: %+v", len(plugins), plugins)
	}
	// Sorted by id, so frontend-design comes first.
	if plugins[0].ID != "frontend-design@claude-plugins-official" {
		t.Fatalf("plugins not sorted by id: %+v", plugins)
	}
	if plugins[0].Version != "unknown" || plugins[0].SHA != "82a73a367be4991ff22e2b43317b3956933c9f9a" {
		t.Fatalf("unexpected first plugin: %+v", plugins[0])
	}
	if plugins[1].Scope != "project" {
		t.Fatalf("scope not preserved: %+v", plugins[1])
	}
}

func TestParseInstalledPluginsRejectsOtherSchema(t *testing.T) {
	if _, err := parseInstalledPlugins([]byte(`{"version":1,"plugins":{}}`)); err == nil {
		t.Fatal("schema version 1 should be rejected")
	}
	if _, err := parseInstalledPlugins([]byte(`not json`)); err == nil {
		t.Fatal("malformed json should be rejected")
	}
}

// diffPlugins is the ground truth for "what did the update actually change" —
// it compares two reads of installed_plugins.json around the update loop.
func TestDiffPlugins(t *testing.T) {
	before := []installedPlugin{
		{ID: "a@m", Scope: "user", Version: "1.0.0", SHA: "aaa"},
		{ID: "b@m", Scope: "user", Version: "2.0.0", SHA: "bbb"},
		{ID: "c@m", Scope: "user", Version: "unknown", SHA: "ccc"},
	}
	after := []installedPlugin{
		{ID: "a@m", Scope: "user", Version: "1.1.0", SHA: "aaa2"},
		{ID: "b@m", Scope: "user", Version: "2.0.0", SHA: "bbb"},
		// Version stays "unknown"; only the sha moves.
		{ID: "c@m", Scope: "user", Version: "unknown", SHA: "ccc9"},
	}
	changed := diffPlugins(before, after)
	if strings.Join(changed, ",") != "a@m,c@m" {
		t.Fatalf("changed = %v, want [a@m c@m]", changed)
	}
	if got := diffPlugins(before, before); len(got) != 0 {
		t.Fatalf("identical reads should report no changes, got %v", got)
	}
	if got := diffPlugins(nil, after); len(got) != 0 {
		t.Fatalf("plugins absent before the update are not 'changed', got %v", got)
	}
}

func TestParseClaudeLastUpdate(t *testing.T) {
	body := `{"timestamp":"2026-08-07T18:05:30.261Z","path":"native","outcome":"failed","status":"install_failed","version_from":"2.1.224","version_to":null}`
	outcome, status, ok := parseClaudeLastUpdate([]byte(body))
	if !ok || outcome != "failed" || status != "install_failed" {
		t.Fatalf("got (%q,%q,%v)", outcome, status, ok)
	}
	if _, _, ok := parseClaudeLastUpdate([]byte(`{}`)); ok {
		t.Fatal("empty object should not report an outcome")
	}
	if _, _, ok := parseClaudeLastUpdate([]byte(`garbage`)); ok {
		t.Fatal("malformed json should not report an outcome")
	}
}

func TestParseMaruUpdateCheck(t *testing.T) {
	body := `{
  "active": {"displayVersion": "r30871679829"},
  "available": {"displayVersion": "r30683517763"},
  "updateAvailable": false
}`
	available, active, avail, err := parseMaruUpdateCheck([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if available || active != "r30871679829" || avail != "r30683517763" {
		t.Fatalf("got (%v,%q,%q)", available, active, avail)
	}
	if _, _, _, err := parseMaruUpdateCheck([]byte(`nope`)); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestParseMaruSyncPending(t *testing.T) {
	body := `{"applied":false,"desiredSkills":45,"actions":[
	  {"action":"record-install","skillName":"a","target":"codex"},
	  {"action":"record-install","skillName":"b","target":"codex"}
	]}`
	n, err := parseMaruSyncPending([]byte(body))
	if err != nil || n != 2 {
		t.Fatalf("got (%d,%v), want (2,nil)", n, err)
	}
	if n, err := parseMaruSyncPending([]byte(`{"applied":true}`)); err != nil || n != 0 {
		t.Fatalf("no actions key should mean zero pending, got (%d,%v)", n, err)
	}
}

func TestResolveUpdateToolsKeepsPhaseOrder(t *testing.T) {
	cmd := newAIUpdateCmd()
	if err := cmd.Flags().Set("tool", "skills,claude"); err != nil {
		t.Fatal(err)
	}
	got, err := resolveUpdateTools(cmd)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Join(got, ",") != "claude,skills" {
		t.Fatalf("got %v, want [claude skills]", got)
	}

	bad := newAIUpdateCmd()
	if err := bad.Flags().Set("tool", "nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveUpdateTools(bad); err == nil {
		t.Fatal("unknown tool should error")
	}
}

func TestResolveUpdateToolsDefaultsToAll(t *testing.T) {
	got, err := resolveUpdateTools(newAIUpdateCmd())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Join(got, ",") != "claude,codex,copilot,gemini,kimi,kiro,cursor,skills" {
		t.Fatalf("default tools = %v", got)
	}
}

func TestToolBinaryResolvesRenamedCLIs(t *testing.T) {
	// kiro and cursor ship under a different executable name than their phase
	// id. Getting this wrong makes both phases permanently "not in PATH"
	// instead of failing loudly.
	for tool, want := range map[string]string{
		"kiro":   "kiro-cli",
		"cursor": "cursor-agent",
		"kimi":   "kimi",
		"claude": "claude",
	} {
		if got := toolBinary(tool); got != want {
			t.Errorf("toolBinary(%q) = %q, want %q", tool, got, want)
		}
	}
	for tool := range updateToolBinary {
		if !containsString(updateTools, tool) {
			t.Errorf("updateToolBinary has %q, which is not an update phase", tool)
		}
	}
}

func TestResolveUpdateToolsRejectsEmptyFilter(t *testing.T) {
	// A mistyped filter must not silently widen back to every tool.
	cmd := newAIUpdateCmd()
	if err := cmd.Flags().Set("tool", ","); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveUpdateTools(cmd); err == nil {
		t.Fatal("empty --tool filter should error, not run all tools")
	}
}

func TestAIUpdateDryRunReportsSkippedNotUpdated(t *testing.T) {
	// --dry-run executes nothing, so no step may claim it updated anything.
	out, errOut, err := runDotForTest("--home", t.TempDir(), "--dry-run", "ai", "update", "--tool", "gemini", "--json")
	if err != nil {
		t.Fatalf("dry-run update: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, `"status": "updated"`) {
		t.Fatalf("dry-run reported an update that never ran:\n%s", out)
	}
	if !strings.Contains(out, `"status": "skipped"`) {
		t.Fatalf("dry-run should report skipped steps:\n%s", out)
	}
}

func TestAIUpdateDryRunSkipsPluginLoop(t *testing.T) {
	// A populated plugin file must not produce an "up-to-date" plugins step
	// under --dry-run: the update loop never ran, so the before/after diff of
	// installed_plugins.json is trivially empty and means nothing.
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), installedPluginsFixture)

	out, errOut, err := runDotForTest("--home", home, "--dry-run", "ai", "update", "--tool", "claude", "--json")
	if err != nil {
		t.Fatalf("dry-run update: %v\nstderr=%s", err, errOut)
	}
	// Holds whether or not claude is installed: with no claude the whole phase
	// is skipped, and with claude the plugin loop must still not claim an
	// outcome it never verified.
	for _, forbidden := range []string{`"status": "updated"`, `"status": "` + stepCurrent + `"`, `"status": "` + stepRan + `"`} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("dry-run step claimed a verified outcome (%s):\n%s", forbidden, out)
		}
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return // no claude on this machine (CI); the phase-level skip is all we can assert
	}
	if !strings.Contains(out, "would update 3 installed plugin(s)") {
		t.Fatalf("dry-run should describe the plugin work it would do:\n%s", out)
	}
}

func TestAIUpdateAndAuthRegistered(t *testing.T) {
	out, errOut, err := runDotForTest("ai", "--help")
	if err != nil {
		t.Fatalf("ai help: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"update", "auth"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ai help missing %q subcommand:\n%s", want, out)
		}
	}
}

func TestCodexClaudeMemCacheStep(t *testing.T) {
	writeCache := func(t *testing.T, home string, runnable bool) string {
		t.Helper()
		root := filepath.Join(home, ".codex", "plugins", "cache", "claude-mem-local", "claude-mem", "13.14.0")
		for _, name := range []string{"mcp-server.cjs", "transcript-watcher.cjs", "bun-runner.js"} {
			writeCLITestFile(t, filepath.Join(root, "scripts", name), "")
		}
		if runnable {
			writeCLITestFile(t, filepath.Join(root, ".install-version"), `{"version":"13.14.0"}`)
			if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	t.Run("no cache is skipped", func(t *testing.T) {
		step := codexClaudeMemCacheStep(t.TempDir())
		if step.Status != stepSkipped {
			t.Fatalf("status = %q, want %q (%s)", step.Status, stepSkipped, step.Detail)
		}
	})
	t.Run("runnable cache is up-to-date", func(t *testing.T) {
		home := t.TempDir()
		root := writeCache(t, home, true)
		step := codexClaudeMemCacheStep(home)
		if step.Status != stepCurrent || !strings.Contains(step.Detail, root) {
			t.Fatalf("step = %+v, want %q with path %s", step, stepCurrent, root)
		}
	})
	t.Run("broken cache fails with repair commands", func(t *testing.T) {
		home := t.TempDir()
		root := writeCache(t, home, false)
		step := codexClaudeMemCacheStep(home)
		if step.Status != stepFailed {
			t.Fatalf("status = %q, want %q (%s)", step.Status, stepFailed, step.Detail)
		}
		for _, want := range []string{root, "codex plugin remove claude-mem", "codex plugin add claude-mem@claude-mem-local"} {
			if !strings.Contains(step.Detail, want) {
				t.Fatalf("detail %q missing %q", step.Detail, want)
			}
		}
	})
}
