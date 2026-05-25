package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDoesNotWarnForUnmanagedClaudeSkills(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(root, "legacy-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy-skill", "SKILL.md"), []byte("# skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runDotForTest("--home", home, "apply", "--yes", "--dry-run", "--profile", "minimal")
	if err != nil {
		t.Fatalf("apply: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if strings.Contains(errOut, "~/.claude/skills") || strings.Contains(out, "~/.claude/skills") {
		t.Fatalf("apply warned about unmanaged Claude skills\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
}
