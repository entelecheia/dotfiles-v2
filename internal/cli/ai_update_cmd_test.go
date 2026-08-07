package cli

import (
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
	if strings.Join(got, ",") != "claude,codex,copilot,gemini,skills" {
		t.Fatalf("default tools = %v", got)
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
	if strings.Contains(out, stepCurrent) || strings.Contains(out, `"status": "updated"`) {
		t.Fatalf("dry-run plugin step claimed a verified outcome:\n%s", out)
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
