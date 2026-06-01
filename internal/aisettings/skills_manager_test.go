package aisettings

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestSkillsManagerApplyAnchorToExplicitTools(t *testing.T) {
	home := t.TempDir()
	writeSkillTestFile(t, filepath.Join(home, ".anchor", "skills", "vault-extract", "SKILL.md"), "# Vault Extract\n")
	mgr := NewSkillsManager(dotexec.NewRunner(false, slog.Default()), home)

	result, err := mgr.Apply(SkillsOptions{Provider: SkillsProviderAnchor, Tools: []string{"claude", "codex"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	for _, target := range []string{
		filepath.Join(home, ".claude", "skills", "vault-extract"),
		filepath.Join(home, ".codex", "skills", "vault-extract"),
	} {
		got, err := os.Readlink(target)
		if err != nil {
			t.Fatalf("readlink %s: %v", target, err)
		}
		want := filepath.Join(home, ".anchor", "skills", "vault-extract")
		if got != want {
			t.Fatalf("link %s = %s, want %s", target, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "vault-extract")); !os.IsNotExist(err) {
		t.Fatalf("explicit tools should not touch agents target, stat err=%v", err)
	}
}

func TestSkillsManagerConflictWarnSkipAndForceBackup(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "ssot", "skill-one")
	writeSkillTestFile(t, filepath.Join(source, "SKILL.md"), "# One\n")
	target := filepath.Join(home, ".claude", "skills", "skill-one")
	writeSkillTestFile(t, filepath.Join(target, "SKILL.md"), "# Hand Edit\n")
	mgr := NewSkillsManager(dotexec.NewRunner(false, slog.Default()), home)

	result, err := mgr.Apply(SkillsOptions{Provider: SkillsProviderPath, SSOTPath: filepath.Join(home, "ssot"), Tools: []string{"claude"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Items) != 1 || !result.Items[0].Conflict || result.Items[0].Changed {
		t.Fatalf("unforced conflict item = %#v", result.Items)
	}
	if _, err := os.Readlink(target); err == nil {
		t.Fatal("unforced conflict replaced target symlink")
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Hand Edit\n" {
		t.Fatalf("unforced conflict modified target: %q", data)
	}

	forced, err := mgr.Apply(SkillsOptions{Provider: SkillsProviderPath, SSOTPath: filepath.Join(home, "ssot"), Tools: []string{"claude"}, Force: true})
	if err != nil {
		t.Fatalf("Apply force: %v", err)
	}
	if len(forced.Items) != 1 || !forced.Items[0].Changed || !forced.Items[0].BackedUp {
		t.Fatalf("forced item = %#v", forced.Items)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("forced target readlink: %v", err)
	}
	if got != source {
		t.Fatalf("forced target = %s, want %s", got, source)
	}
	backup, err := os.ReadFile(filepath.Join(forced.Items[0].BackupPath, "SKILL.md"))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != "# Hand Edit\n" {
		t.Fatalf("backup = %q", backup)
	}
}

func TestSkillsManagerGeminiAndAntigravityRootsAreSeparate(t *testing.T) {
	home := t.TempDir()
	writeSkillTestFile(t, filepath.Join(home, ".anchor", "skills", "shared-skill", "SKILL.md"), "# Shared\n")
	mgr := NewSkillsManager(dotexec.NewRunner(false, slog.Default()), home)

	if _, err := mgr.Apply(SkillsOptions{Provider: SkillsProviderAnchor, Tools: []string{"gemini", "antigravity"}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, target := range []string{
		filepath.Join(home, ".gemini", "skills", "shared-skill"),
		filepath.Join(home, ".gemini", "antigravity", "skills", "shared-skill"),
	} {
		if got, err := os.Readlink(target); err != nil || !strings.HasSuffix(got, filepath.Join(".anchor", "skills", "shared-skill")) {
			t.Fatalf("target %s readlink=%q err=%v", target, got, err)
		}
	}
}

func TestSkillsManagerDefaultToolsDetectsPresentToolHomes(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewSkillsManager(dotexec.NewRunner(false, slog.Default()), home)

	got := mgr.DefaultTools()
	if !defaultToolsHas(got, "claude") || !defaultToolsHas(got, "antigravity") {
		t.Fatalf("DefaultTools = %v, want claude + antigravity", got)
	}
	if defaultToolsHas(got, "codex") {
		t.Fatalf("DefaultTools detected codex without ~/.codex: %v", got)
	}
}

func TestSkillsManagerDefaultToolsFallsBackToAll(t *testing.T) {
	home := t.TempDir() // no tool homes present
	mgr := NewSkillsManager(dotexec.NewRunner(false, slog.Default()), home)

	got := mgr.DefaultTools()
	if len(got) != len(RegisteredSkillTools()) {
		t.Fatalf("fallback DefaultTools = %v, want all %d registered tools", got, len(RegisteredSkillTools()))
	}
}

func defaultToolsHas(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func writeSkillTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
