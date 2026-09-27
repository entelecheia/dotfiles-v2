package aihandoff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func fixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "note.md")
	testGit(t, root, "commit", "-qm", "initial")
	return home, root
}
func options(home, root string) Options {
	return Options{Home: home, Project: root, Agent: "codex", Kind: "validation", Summary: "검증 결과, 다음 작업\n", Artifacts: []string{"note.md"}, Result: "passed", SelectedAgents: []string{"codex"}}
}
func TestReadOnlyEmptyAndDryRun(t *testing.T) {
	home, root := fixture(t)
	report, err := Show(context.Background(), home, root)
	if err != nil || len(report.Entries) != 0 {
		t.Fatalf("%v %#v", err, report)
	}
	o := options(home, root)
	o.DryRun = true
	if _, err := RecordNote(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("readonly/dryrun wrote storage")
	}
}
func TestSharedWorktreeHistoryAndStaleness(t *testing.T) {
	home, root := fixture(t)
	ctx := context.Background()
	if _, err := RecordNote(ctx, options(home, root)); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	testGit(t, root, "worktree", "add", "-q", "--detach", worktree)
	report, err := Show(ctx, home, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.Entries[0].State != "current" || report.Entries[0].Record.ProducerResult != "passed" {
		t.Fatalf("%#v", report)
	}
	if !strings.Contains(report.Verification, "claims") {
		t.Fatal("producer pass treated as validated")
	}
	if err := os.WriteFile(filepath.Join(worktree, "note.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err = Show(ctx, home, worktree)
	if err != nil || report.Entries[0].State != "needs-revalidation" {
		t.Fatalf("%v %#v", err, report)
	}
	testGit(t, worktree, "add", "note.md")
	testGit(t, worktree, "commit", "-qm", "next")
	report, err = Show(ctx, home, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(report.Entries[0].Reasons, ","), "HEAD changed") {
		t.Fatal("head drift missed")
	}
}
func TestConcurrentWriters(t *testing.T) {
	home, root := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := RecordNote(context.Background(), options(home, root)); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := Show(context.Background(), home, root)
	if err != nil || len(report.Entries) != 2 {
		t.Fatalf("%v %#v", err, report)
	}
}
func TestArtifactBoundsAndSelectedAgent(t *testing.T) {
	home, root := fixture(t)
	external := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(external, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{"../outside", external, "escape", ".git/config", "target/output", "node_modules/x", ".env", "secrets.json", "auth.json"} {
		o := options(home, root)
		o.Artifacts = []string{artifact}
		if _, err := RecordNote(context.Background(), o); err == nil {
			t.Fatalf("accepted %q", artifact)
		}
	}
	o := options(home, root)
	o.Agent = "claude"
	if _, err := RecordNote(context.Background(), o); err == nil {
		t.Fatal("unselected producer accepted")
	}
	o = options(home, root)
	o.Summary = strings.Repeat("x", SummaryLimit+1)
	if _, err := RecordNote(context.Background(), o); err == nil {
		t.Fatal("oversize summary accepted")
	}
}

func TestAggregateArtifactBudgetAndMissingEvidence(t *testing.T) {
	home, root := fixture(t)
	ctx := context.Background()
	o := options(home, root)
	if _, err := RecordNote(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "note.md")); err != nil {
		t.Fatal(err)
	}
	report, err := Show(ctx, home, root)
	if err != nil || report.Entries[0].State != "needs-revalidation" {
		t.Fatalf("missing evidence: %v %#v", err, report)
	}
	o.Artifacts = nil
	for _, name := range []string{"a", "b", "c", "d"} {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(9 << 20); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		o.Artifacts = append(o.Artifacts, name)
	}
	if _, err := RecordNote(ctx, o); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("aggregate cap not enforced: %v", err)
	}
}

func TestSummaryFIFORejectedWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSummaryFile(context.Background(), path); err == nil {
		t.Fatal("FIFO summary accepted")
	}
}
func TestCanceledAppendDoesNotCreateStorage(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := appendLocked(ctx, home, filepath.Join(home, ".local", "share", "dotfiles", "ai", "handoffs", "test.jsonl"), []byte("{}\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("canceled append created storage")
	}
}
func TestStorageParentSymlinksRefused(t *testing.T) {
	home, root := fixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".local")); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordNote(context.Background(), options(home, root)); err == nil {
		t.Fatal("symlink store written")
	}
	if _, err := Show(context.Background(), home, root); err == nil {
		t.Fatal("symlink store read")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside store changed: %v %v", entries, err)
	}
}
func TestFingerprintHonorsRemainingBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note"), []byte("123456"), 0600); err != nil {
		t.Fatal(err)
	}
	budget := int64(5)
	if _, err := fingerprint(root, "note", &budget); err == nil {
		t.Fatal("fingerprint exceeded remaining budget")
	}
}
