package aisettings

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestPatchAgentsCoauthorInstruction(t *testing.T) {
	doc := "# AI Agents\n\n## Tool-Specific Notes\n\nKeep me.\n"
	got := patchAgentsCoauthorInstruction(doc)
	if !strings.Contains(got, coauthorGuardStart) || !strings.Contains(got, "Co-authored-by") {
		t.Fatalf("guard block missing:\n%s", got)
	}
	if !strings.Contains(got, "commit messages in English") {
		t.Fatalf("English commit policy missing:\n%s", got)
	}
	if !strings.Contains(got, "Keep me.") {
		t.Fatalf("existing section content removed:\n%s", got)
	}
	again := patchAgentsCoauthorInstruction(got)
	if strings.Count(again, coauthorGuardStart) != 1 {
		t.Fatalf("guard block duplicated:\n%s", again)
	}
}

func TestPatchGitHooksPath(t *testing.T) {
	got := patchGitHooksPath("[user]\n    name = Test\n\n[core]\n    pager = less\n")
	if !strings.Contains(got, "hooksPath = ~/.config/git/hooks") {
		t.Fatalf("hooksPath missing:\n%s", got)
	}
	if !strings.Contains(got, "pager = less") {
		t.Fatalf("core key removed:\n%s", got)
	}
}

func TestCoauthorGuardHookWarnAndBlock(t *testing.T) {
	dir := t.TempDir()
	msg := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msg, []byte("feat: test\n\nCo-authored-by: Bot <bot@example.com>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode string
		want int
	}{
		{CoauthorGuardWarn, 0},
		{CoauthorGuardBlock, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			hook := filepath.Join(dir, "hook-"+tc.mode)
			if err := os.WriteFile(hook, []byte(coauthorGuardHookScript(tc.mode)), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", hook, msg)
			err := cmd.Run()
			got := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					got = exit.ExitCode()
				} else {
					t.Fatalf("run hook: %v", err)
				}
			}
			if got != tc.want {
				t.Fatalf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCoauthorGuardApplyDetectsHooksPathConflict(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".config", "git", "config"), []byte("[core]\n    hooksPath = ~/.other-hooks\n"))
	mgr := NewCoauthorGuardManager(dotexec.NewRunner(false, slog.Default()), home)
	_, err := mgr.Apply(CoauthorGuardOptions{Mode: CoauthorGuardWarn})
	if err == nil || !strings.Contains(err.Error(), "not dot-managed") {
		t.Fatalf("expected hooksPath conflict, got %v", err)
	}
}
