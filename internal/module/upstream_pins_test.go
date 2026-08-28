package module

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	internalexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestStageGitComponent_RejectsRevisionMismatch(t *testing.T) {
	original := runPinnedGit
	t.Cleanup(func() { runPinnedGit = original })
	var calls [][]string
	runPinnedGit = func(_ context.Context, _ *internalexec.Runner, args ...string) (*internalexec.Result, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) >= 2 && args[len(args)-2] == "rev-parse" {
			return &internalexec.Result{Stdout: "different-sha\n"}, nil
		}
		return &internalexec.Result{}, nil
	}
	rc := &RunContext{Runner: internalexec.NewProbeRunner()}
	pin := gitComponentPin{Name: "fixture", Repository: "https://example.invalid/repo.git", Commit: "146461f7c6d95f4ba1220559d66eb113418b40a8"}
	err := stageGitComponent(context.Background(), rc, pin, t.TempDir()+"/stage")
	if err == nil || !strings.Contains(err.Error(), "expected commit") {
		t.Fatalf("stageGitComponent error = %v, want exact-revision refusal", err)
	}
	if len(calls) != 4 || !reflect.DeepEqual(calls[0][:4], []string{"clone", "--no-checkout", "--depth", "1"}) {
		t.Fatalf("git command sequence = %#v", calls)
	}
}

func TestComponentPinLifecycle(t *testing.T) {
	marker := componentPinMarker{
		Schema:    componentPinMarkerSchema,
		Component: "oh-my-zsh-base",
		Source:    "https://github.com/ohmyzsh/ohmyzsh.git",
		Commit:    "146461f7c6d95f4ba1220559d66eb113418b40a8",
		Owned:     ohMyZshOwnedEntries(),
		Files:     map[string]string{"oh-my-zsh.sh": "digest"},
	}
	if err := marker.validate(); err != nil {
		t.Fatalf("valid marker rejected: %v", err)
	}
	if !reflect.DeepEqual(marker.Owned, expectedOhMyZshOwnedEntries) {
		t.Fatalf("owned entries = %#v, want %#v", marker.Owned, expectedOhMyZshOwnedEntries)
	}
}

func TestHashManagedFiles_RecordsSafeSymlinkIdentity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"target-a", "target-b"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("same bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "managed-link")
	if err := os.Symlink("target-a", link); err != nil {
		t.Fatal(err)
	}

	before, err := hashManagedFiles(root, []string{"managed-link", "target-a", "target-b"})
	if err != nil {
		t.Fatalf("hashManagedFiles with safe symlink: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target-b", link); err != nil {
		t.Fatal(err)
	}
	after, err := hashManagedFiles(root, []string{"managed-link", "target-a", "target-b"})
	if err != nil {
		t.Fatalf("hashManagedFiles after symlink retarget: %v", err)
	}
	if before["managed-link"] == after["managed-link"] {
		t.Fatal("symlink retarget did not change managed-file identity")
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if _, err := hashManagedFiles(root, []string{"managed-link"}); err == nil {
		t.Fatal("absolute managed symlink accepted")
	}
}
