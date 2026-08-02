package syncer

import (
	"slices"
	"strings"
	"testing"
)

func TestSecretsFilterArgs_OrderAndContent(t *testing.T) {
	args := secretsFilterArgs([]string{"/.maru/secrets/app.token"})

	// Allow patterns (with parent-dir includes) must precede every exclude.
	wantOrder := []string{
		"--include=/.maru/",
		"--include=/.maru/secrets/",
		"--include=/.maru/secrets/app.token",
		"--include=.env.example",
		"--exclude=/.secrets",
		"--exclude=.env",
	}
	lastIdx := -1
	for _, w := range wantOrder {
		idx := slices.Index(args, w)
		if idx < 0 {
			t.Fatalf("secretsFilterArgs missing %q\nargs: %v", w, args)
		}
		if idx < lastIdx {
			t.Fatalf("order violation: %q at %d before previous at %d\nargs: %v", w, idx, lastIdx, args)
		}
		lastIdx = idx
	}
	for _, w := range []string{"--exclude=/.maru/secrets/**", "--exclude=.env.*", "--exclude=/_sys/mcp.local.json"} {
		if !slices.Contains(args, w) {
			t.Errorf("secretsFilterArgs missing %q", w)
		}
	}
}

func TestSecretsFilterArgs_CommentsAndBlanksSkipped(t *testing.T) {
	args := secretsFilterArgs([]string{"# comment", "  ", ""})
	for _, a := range args {
		if strings.Contains(a, "comment") {
			t.Errorf("comment leaked into args: %q", a)
		}
	}
}

func TestAllowParentDirs(t *testing.T) {
	got := allowParentDirs("/.maru/secrets/app.token")
	want := []string{"/.maru/", "/.maru/secrets/"}
	if !slices.Equal(got, want) {
		t.Errorf("allowParentDirs = %v, want %v", got, want)
	}
	if got := allowParentDirs(".env"); got != nil {
		t.Errorf("unanchored pattern should yield no parents, got %v", got)
	}
	// Wildcard parents stop the expansion.
	got = allowParentDirs("/a/*/c.txt")
	if !slices.Equal(got, []string{"/a/"}) {
		t.Errorf("wildcard parent expansion = %v, want [/a/]", got)
	}
}

func TestSyncFilter_SecretsDenyByDefaultAndAllowOptIn(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude

	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".env", "app/.env", "app/.env.local", ".secrets/token", ".maru/secrets/k.age", "_sys/mcp.local.json"} {
		if !f.shouldSkip("", rel, false) {
			t.Errorf("secret %q not skipped by default", rel)
		}
	}
	for _, rel := range []string{".env.example", "app/.env.sample"} {
		if f.shouldSkip("", rel, false) {
			t.Errorf("env template %q should sync", rel)
		}
	}

	// Explicit allow re-includes the secret and keeps its parents traversable.
	cfg.AllowPatterns = []string{"/.maru/secrets/app.token"}
	f, err = newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.shouldSkip("", ".maru/secrets/app.token", false) {
		t.Error("allowed secret still skipped")
	}
	if f.shouldSkip("", ".maru/secrets", true) || f.shouldSkip("", ".maru", true) {
		t.Error("allow parent dirs not traversable")
	}
	if !f.shouldSkip("", ".maru/secrets/other.key", false) {
		t.Error("non-allowed sibling secret leaked")
	}
}

func TestSyncFilter_SubmodulesAlwaysSkipped(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeExclude
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	f.submodules = []string{"dev", "sites/a", "vault"}
	for _, rel := range []string{"dev", "dev/maru/main.go", "sites/a/index.html", "vault/notes/x.md"} {
		if !f.shouldSkip("", rel, strings.HasSuffix(rel, "dev")) {
			t.Errorf("submodule path %q not skipped", rel)
		}
	}
	if f.shouldSkip("", "sites-notes.md", false) {
		t.Error("non-submodule sibling wrongly skipped")
	}
}

func TestSyncFilter_IncludeModeTrackedUnion(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.FilterMode = FilterModeInclude
	cfg.IncludePatterns = []string{"*.pdf"}
	f, err := newSyncFilter(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	f.tracked = map[string]bool{"src/main.go": true, "docs/plan.md": true}

	for _, rel := range []string{"src/main.go", "docs/plan.md", "assets/scan.PDF"} {
		if f.shouldSkip("", rel, false) {
			t.Errorf("union member %q skipped", rel)
		}
	}
	if !f.shouldSkip("", "untracked-notes.md", false) {
		t.Error("untracked non-binary file must not sync in include mode")
	}
	if f.shouldSkip("", "anydir", true) {
		t.Error("directories must stay traversable in include mode")
	}
}
