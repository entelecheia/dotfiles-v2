package cli

import (
	"strings"
	"testing"
)

// Fixtures mirror the real v2 installed_plugins.json, including a plugin whose
// version is the literal "unknown" — semver is unusable, sha is the truth.
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

func TestParseMarketplaceLocations(t *testing.T) {
	body := `[
	  {"name":"cloudflare","source":"github","repo":"cloudflare/skills","installLocation":"/home/.claude/plugins/marketplaces/cloudflare"},
	  {"name":"openai-codex","installLocation":"/home/.claude/plugins/marketplaces/openai-codex"},
	  {"name":"broken"}
	]`
	locs, err := parseMarketplaceLocations([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("locations = %v, want 2 entries", locs)
	}
	if locs["cloudflare"] != "/home/.claude/plugins/marketplaces/cloudflare" {
		t.Fatalf("cloudflare location = %q", locs["cloudflare"])
	}
	if _, ok := locs["broken"]; ok {
		t.Fatal("entry without installLocation should be skipped")
	}
	if _, err := parseMarketplaceLocations([]byte(`{}`)); err == nil {
		t.Fatal("object payload should error (list is an array)")
	}
}

func TestMarketplaceOf(t *testing.T) {
	if got := marketplaceOf("codex@openai-codex"); got != "openai-codex" {
		t.Fatalf("marketplaceOf = %q", got)
	}
	if got := marketplaceOf("claude.ai@name@market"); got != "market" {
		t.Fatalf("last @ should win, got %q", got)
	}
	if got := marketplaceOf("bare"); got != "" {
		t.Fatalf("id without marketplace should yield empty, got %q", got)
	}
}

func TestDiffPlugins(t *testing.T) {
	before := []installedPlugin{
		{ID: "a@m", Scope: "user", Version: "1.0.0", SHA: "aaa"},
		{ID: "b@m", Scope: "user", Version: "2.0.0", SHA: "bbb"},
		{ID: "c@m", Scope: "user", Version: "unknown", SHA: "ccc"},
	}
	after := []installedPlugin{
		{ID: "a@m", Scope: "user", Version: "1.1.0", SHA: "aaa2"},
		{ID: "b@m", Scope: "user", Version: "2.0.0", SHA: "bbb"},
		{ID: "c@m", Scope: "user", Version: "unknown", SHA: "ccc9"},
	}
	changed := diffPlugins(before, after)
	if strings.Join(changed, ",") != "a@m,c@m" {
		t.Fatalf("changed = %v, want [a@m c@m]", changed)
	}
	if got := diffPlugins(before, before); len(got) != 0 {
		t.Fatalf("identical reads should report no changes, got %v", got)
	}
}

func TestPluginUpdateChecks(t *testing.T) {
	installed := []installedPlugin{
		{ID: "current@mkt-a", SHA: "1111111111"},
		{ID: "outdated@mkt-b", SHA: "2222222222"},
		{ID: "nosha@mkt-a", SHA: ""},
		{ID: "notgit@mkt-c", SHA: "4444444444"},
	}
	// mkt-c is absent: its marketplace is not a git clone, so nothing to compare.
	marketplaces := map[string]string{
		"mkt-a": "1111111111",
		"mkt-b": "9999999999",
	}
	checks := pluginUpdateChecks(installed, marketplaces)
	states := map[string]string{}
	for _, c := range checks {
		states[c.ID] = c.State
	}
	want := map[string]string{
		"current@mkt-a":  "current",
		"outdated@mkt-b": stepOutdated,
		"nosha@mkt-a":    stepUnknown,
		"notgit@mkt-c":   stepUnknown,
	}
	for id, state := range want {
		if states[id] != state {
			t.Fatalf("%s state = %q, want %q", id, states[id], state)
		}
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
