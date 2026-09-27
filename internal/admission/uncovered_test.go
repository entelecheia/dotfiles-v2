package admission

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fixtureRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHeavyweightInventoryClassification(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       bool
	}{{"cargo", "cargo test --lib", true}, {"go", "go test ./...", true}, {"cp", "cp -cR /x/target /y/target", true}, {"node", "node /x/playwright test", true}, {"go", "go version", false}, {"ps", "ps -axo pid", false}, {"git", "git status", false}, {"node", "node server.js", false}} {
		if got := heavyweight(tc.name, tc.args); got != tc.want {
			t.Errorf("%s %s: got %t", tc.name, tc.args, got)
		}
	}
}

func TestRepositoryIdentityCollapsesWorktrees(t *testing.T) {
	repo := fixtureRepository(t)
	other := fixtureRepository(t)
	worktree := t.TempDir()
	metadata := filepath.Join(repo, ".git", "worktrees", "linked")
	if err := os.MkdirAll(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+metadata+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main, err := repositoryIdentity(repo)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := repositoryIdentity(worktree)
	if err != nil || linked != main {
		t.Fatalf("main=%q linked=%q err=%v", main, linked, err)
	}
	if o, _ := repositoryIdentity(other); o == main {
		t.Fatal("different repositories share an identity")
	}
	if _, err := repositoryIdentity(t.TempDir()); err == nil {
		t.Fatal("a non-repository resolved to an identity")
	}
}

func TestUncoveredJobsAreScopedButUnknownBlocks(t *testing.T) {
	for _, tc := range []struct {
		repo string
		want int
	}{{"/a/.git", 1}, {"/b/.git", 0}, {"", 1}} {
		if got := filterUncovered([]HeavyJob{{PID: 12, Repository: tc.repo}}, "/a/.git", false); len(got) != tc.want {
			t.Fatalf("repo=%q blockers=%v", tc.repo, got)
		}
	}
	if got := filterUncovered([]HeavyJob{{Repository: "/b/.git", Maintenance: true}}, "", true); len(got) != 1 {
		t.Fatal("shared maintenance collision missed")
	}
	if got := filterUncovered([]HeavyJob{{Repository: "/b/.git"}}, "", true); len(got) != 0 {
		t.Fatalf("non-maintenance work in another repo blocked the maintenance slot: %v", got)
	}
}

func TestFailedLsofBatchDiscardsPartialOwnership(t *testing.T) {
	dirs := lsofDirectories("p123\nn/other/repository\np456\n", context.DeadlineExceeded)
	if len(dirs) != 0 {
		t.Fatalf("trusted partial failed telemetry: %v", dirs)
	}
	job := HeavyJob{PID: 123, Directory: dirs[123]}
	if got := filterUncovered([]HeavyJob{job}, "/current/.git", false); len(got) != 1 {
		t.Fatal("uncertain ownership failed open")
	}
	if complete := lsofDirectories("p123\nn/other/repository\n", nil); complete[123] != "/other/repository" {
		t.Fatal("complete telemetry rejected")
	}
}

func TestUserStateRootFollowsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := UserStateRoot()
	if err != nil || got != DefaultStateRoot(home) {
		t.Fatalf("UserStateRoot = %q, %v; want %q", got, err, DefaultStateRoot(home))
	}
}

// TestParseProcessTableNamesFromArgv0: tools started by absolute path are
// classified by their real name, not a truncated comm column.
func TestParseProcessTableNamesFromArgv0(t *testing.T) {
	rows, parents, err := parseProcessTable("  101     1   5.0 /opt/homebrew/bin/go test ./...\n  102   101  99.0 /usr/bin/make -j2\n  103     1   0.1 /opt/homebrew/bin/dot ai memory bridge\n")
	if err != nil || len(rows) != 3 || parents[102] != 101 {
		t.Fatalf("rows=%+v parents=%v err=%v", rows, parents, err)
	}
	for i, want := range []bool{true, true, false} {
		if got := heavyweight(rows[i].name, rows[i].args); got != want {
			t.Errorf("row %+v heavyweight = %v, want %v", rows[i], got, want)
		}
	}
}
