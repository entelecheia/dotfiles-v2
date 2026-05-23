package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnNonSymlinkClaudeSkills(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(root, "regular-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular-dir", "SKILL.md"), []byte("# skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "loose.md"), []byte("# loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".anchor", "skills", "linked")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	warnNonSymlinkClaudeSkills(&Printer{Out: &out, Err: &errb}, home)
	got := errb.String()

	if !strings.Contains(got, "regular-dir/") || !strings.Contains(got, "loose.md") {
		t.Fatalf("warning missing unmanaged entries: %q", got)
	}
	if strings.Contains(got, "empty-dir") || strings.Contains(got, "linked") {
		t.Fatalf("warning included allowed entries: %q", got)
	}
}
