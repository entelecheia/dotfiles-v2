package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAISkillsListJSONAndValidateStrict(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".codex", "skills", "valid", "SKILL.md"), `---
name: valid-skill
description: Valid
schema_version: v1
---
# Valid
`)
	writeCLITestFile(t, filepath.Join(home, ".codex", "skills", "legacy", "SKILL.md"), `---
name: legacy-skill
description: Missing schema
---
# Legacy
`)

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "list", "--tool", "codex", "--json")
	if err != nil {
		t.Fatalf("skills list: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"status": "valid"`) || !strings.Contains(out, `"status": "legacy"`) {
		t.Fatalf("skills list json missing statuses:\n%s", out)
	}

	_, _, err = runDotForTest("--home", home, "ai", "skills", "validate", "--tool", "codex")
	if err != nil {
		t.Fatalf("non-strict validate should pass legacy skills: %v", err)
	}
	_, _, err = runDotForTest("--home", home, "ai", "skills", "validate", "--tool", "codex", "--strict")
	if err == nil {
		t.Fatal("strict validate should fail legacy skills")
	}
}

func TestAISkillsListGeminiAliasScansAntigravity(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".gemini", "antigravity", "skills", "valid", "SKILL.md"), `---
name: antigravity-skill
description: Valid
schema_version: v1
---
# Valid
`)

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "list", "--tool", "gemini", "--json")
	if err != nil {
		t.Fatalf("skills list: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"tool": "antigravity"`) || !strings.Contains(out, `"name": "antigravity-skill"`) {
		t.Fatalf("gemini alias did not scan antigravity roots:\n%s", out)
	}
}

func TestAISkillsApplyPersistsConfiguredSSOT(t *testing.T) {
	home := t.TempDir()
	ssot := filepath.Join(home, "anchor-skills")
	writeCLITestFile(t, filepath.Join(ssot, "vault-extract", "SKILL.md"), "# Vault Extract\n")

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "apply", "--provider", "anchor", "--ssot", ssot, "--tool", "claude", "--persist", "--json")
	if err != nil {
		t.Fatalf("skills apply: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	target := filepath.Join(home, ".claude", "skills", "vault-extract")
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink %s: %v", target, err)
	}
	if got != filepath.Join(ssot, "vault-extract") {
		t.Fatalf("target link = %s", got)
	}
	stateData, err := os.ReadFile(filepath.Join(home, ".config", "dotfiles", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state := string(stateData)
	for _, want := range []string{"skills:", "enabled: true", "provider: anchor", "ssot_path: " + ssot, "- claude"} {
		if !strings.Contains(state, want) {
			t.Fatalf("persisted state missing %q:\n%s", want, state)
		}
	}

	status, errOut, err := runDotForTest("--home", home, "ai", "skills", "status", "--json")
	if err != nil {
		t.Fatalf("skills status from persisted config: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(status, `"status": "in-sync"`) {
		t.Fatalf("status did not use persisted skills config:\n%s", status)
	}
}

func TestAIAgentsApplyAntigravityAndGeminiAlias(t *testing.T) {
	home := t.TempDir()
	ssot := filepath.Join(home, ".config", "dotfiles", "agents", "AGENTS.md")
	target := filepath.Join(home, ".gemini", "GEMINI.md")
	writeCLITestFile(t, ssot, "shared\n")

	if _, errOut, err := runDotForTest("--home", home, "ai", "agents", "apply", "--tool", "antigravity"); err != nil {
		t.Fatalf("agents apply antigravity: %v\nstderr=%s", err, errOut)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "shared\n") {
		t.Fatalf("antigravity target missing shared instructions: %q", got)
	}

	if _, errOut, err := runDotForTest("--home", home, "ai", "agents", "apply", "--tool", "gemini"); err != nil {
		t.Fatalf("agents apply gemini alias: %v\nstderr=%s", err, errOut)
	}
}

func TestAIAuditTailAndSummary(t *testing.T) {
	home := t.TempDir()
	if _, _, err := runDotForTest("--home", home, "ai", "audit", "summary"); err != nil {
		t.Fatalf("empty audit summary: %v", err)
	}
	if _, _, err := runDotForTest("--home", home, "ai", "audit", "tail", "1"); err != nil {
		t.Fatalf("empty audit tail: %v", err)
	}
}

func TestAIAgentsApplyForceWritesAndAudits(t *testing.T) {
	home := t.TempDir()
	ssot := filepath.Join(home, ".config", "dotfiles", "agents", "AGENTS.md")
	target := filepath.Join(home, ".codex", "AGENTS.md")
	writeCLITestFile(t, ssot, "shared\n")
	writeCLITestFile(t, target, "hand edit\n")

	_, _, err := runDotForTest("--home", home, "ai", "agents", "apply", "--tool", "codex")
	if err == nil {
		t.Fatal("agents apply should fail on protected write conflict")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hand edit\n" {
		t.Fatalf("unforced apply overwrote target: %q", got)
	}

	out, errOut, err := runDotForTest("--home", home, "ai", "agents", "apply", "--tool", "codex", "--force")
	if err != nil {
		t.Fatalf("agents apply --force: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "shared\n") {
		t.Fatalf("forced apply did not render SSOT: %q", got)
	}
	events, _, err := runDotForTest("--home", home, "ai", "audit", "tail", "1")
	if err != nil {
		t.Fatalf("audit tail: %v", err)
	}
	if !strings.Contains(events, `"type":"ai.agents.apply"`) {
		t.Fatalf("audit tail missing agents apply event:\n%s", events)
	}
}

func runDotForTest(args ...string) (string, string, error) {
	root := NewRootCmd("dev", "test")
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func writeCLITestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
