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

func TestAISkillsPathDefaultsWithNoFlags(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	writeCLITestFile(t, filepath.Join(home, ".anchor", "skills", "vault-extract", "SKILL.md"), "# Vault Extract\n")

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "path")
	if err != nil {
		t.Fatalf("skills path with no flags should succeed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "anchor") {
		t.Fatalf("path output missing provider anchor:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(home, ".anchor", "skills")) {
		t.Fatalf("path output missing anchor SSOT root:\n%s", out)
	}
	if !strings.Contains(out, "Target Roots") || !strings.Contains(out, filepath.Join(home, ".claude", "skills")) {
		t.Fatalf("path output missing detected target root:\n%s", out)
	}
}

func TestAISkillsStatusDefaultsWithNoFlags(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	writeCLITestFile(t, filepath.Join(home, ".anchor", "skills", "vault-extract", "SKILL.md"), "# Vault Extract\n")

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "status", "--json")
	if err != nil {
		t.Fatalf("skills status with no flags should succeed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"provider": "anchor"`) {
		t.Fatalf("status json missing provider anchor:\n%s", out)
	}
	if !strings.Contains(out, `"claude"`) {
		t.Fatalf("status json missing detected tool:\n%s", out)
	}
}

func TestAISkillsPathFallsBackToAllToolsWhenNoneDetected(t *testing.T) {
	home := t.TempDir() // no ~/.claude, ~/.codex, ~/.gemini etc.
	writeCLITestFile(t, filepath.Join(home, ".anchor", "skills", "vault-extract", "SKILL.md"), "# Vault Extract\n")

	out, errOut, err := runDotForTest("--home", home, "ai", "skills", "path")
	if err != nil {
		t.Fatalf("skills path fallback should succeed: %v\nstderr=%s", err, errOut)
	}
	for _, id := range []string{"claude", "codex", "agents", "gemini", "antigravity"} {
		if !strings.Contains(out, id) {
			t.Fatalf("fallback path output missing tool %q:\n%s", id, out)
		}
	}
}

func TestAISkillsApplyNoToolsReturnsActionableError(t *testing.T) {
	home := t.TempDir()
	ssot := filepath.Join(home, "anchor-skills")
	writeCLITestFile(t, filepath.Join(ssot, "vault-extract", "SKILL.md"), "# Vault Extract\n")

	_, errOut, err := runDotForTest("--home", home, "ai", "skills", "apply", "--provider", "anchor", "--ssot", ssot)
	if err == nil {
		t.Fatal("apply with no tools and no config should error")
	}
	msg := err.Error() + errOut
	for _, want := range []string{"--tool", "claude", "modules.ai.skills.tools"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("apply error not actionable, missing %q: %v\nstderr=%s", want, err, errOut)
		}
	}
}

func TestAISkillsApplyProviderPathRequiresSSOT(t *testing.T) {
	home := t.TempDir()

	_, errOut, err := runDotForTest("--home", home, "ai", "skills", "apply", "--provider", "path", "--tool", "claude")
	if err == nil {
		t.Fatal("apply with provider=path and no --ssot should error")
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, "--ssot") || !strings.Contains(msg, "anchor") {
		t.Fatalf("provider=path error not actionable: %v\nstderr=%s", err, errOut)
	}
}

func TestAISkillsApplyUnknownToolListsValid(t *testing.T) {
	home := t.TempDir()
	ssot := filepath.Join(home, "anchor-skills")
	writeCLITestFile(t, filepath.Join(ssot, "demo", "SKILL.md"), "# Demo\n")

	_, errOut, err := runDotForTest("--home", home, "ai", "skills", "apply", "--provider", "anchor", "--ssot", ssot, "--tool", "bogus")
	if err == nil {
		t.Fatal("apply with an unknown tool should error")
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, "bogus") || !strings.Contains(msg, "valid:") {
		t.Fatalf("unknown-tool error missing valid list: %v\nstderr=%s", err, errOut)
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
