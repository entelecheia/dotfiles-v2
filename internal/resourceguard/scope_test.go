package resourceguard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fixtureRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestScopeCollapsesGitWorktreesAndAllowsDifferentRepositories(t *testing.T) {
	repo := fixtureRepository(t)
	other := fixtureRepository(t)
	worktree := t.TempDir()
	metadata := filepath.Join(repo, ".git", "worktrees", "linked")
	if err := os.MkdirAll(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "commondir"), []byte("../..\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+metadata+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	one, err := ResolveScope(Options{ProjectDir: repo})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := ResolveScope(Options{ProjectDir: worktree})
	if err != nil || one.Key != linked.Key {
		t.Fatalf("main=%+v linked=%+v err=%v", one, linked, err)
	}
	g, now, _ := testGuard(t)
	seedHealthy(t, g, now)
	release, err := g.acquire(context.Background(), Options{ProjectDir: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if r, err := g.acquire(context.Background(), Options{ProjectDir: worktree, HomeDir: "/different-profile"}); err == nil {
		r()
		t.Fatal("same repo worktree bypassed slot")
	}
	r, err := g.acquire(context.Background(), Options{ProjectDir: other})
	if err != nil {
		t.Fatalf("different repo blocked: %v", err)
	}
	r()
}
func TestSharedMaintenanceScopeIndependentOfCallingRepository(t *testing.T) {
	g, now, _ := testGuard(t)
	seedHealthy(t, g, now)
	one := Options{ScopeKey: "tooling", ProjectDir: fixtureRepository(t)}
	other := Options{ScopeKey: "tooling", ProjectDir: fixtureRepository(t)}
	release, err := g.acquire(context.Background(), one)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if r, err := g.acquire(context.Background(), other); err == nil {
		r()
		t.Fatal("calling repo bypassed shared install slot")
	}
}
func TestUncoveredJobsAreScopedButUnknownBlocks(t *testing.T) {
	scope := Scope{Key: "repo:/a/.git", Project: "/a/.git"}
	for _, tc := range []struct {
		repo string
		want int
	}{{"/a/.git", 1}, {"/b/.git", 0}, {"", 1}} {
		s := filterUncovered(Sample{Jobs: []HeavyJob{{PID: 12, Repository: tc.repo}}}, scope)
		if len(s.Uncovered) != tc.want {
			t.Fatalf("repo=%q blockers=%v", tc.repo, s.Uncovered)
		}
	}
	s := filterUncovered(Sample{Jobs: []HeavyJob{{Repository: "/b/.git", Maintenance: true}}}, Scope{Shared: true})
	if len(s.Uncovered) != 1 {
		t.Fatal("shared maintenance collision missed")
	}
}
func TestOtherRepoWorkDoesNotResetSharedHostRecovery(t *testing.T) {
	g, now, s := testGuard(t)
	s.Uncovered = []string{"cargo in another repo"}
	s.Jobs = []HeavyJob{{Repository: "/other/.git", Name: "cargo"}}
	seedHealthy(t, g, now)
	_, h, err := g.observe(context.Background())
	if err != nil || h.HealthySince.IsZero() {
		t.Fatalf("job reset host health: %+v %v", h, err)
	}
	release, err := g.acquire(context.Background(), Options{ProjectDir: fixtureRepository(t)})
	if err != nil {
		t.Fatal(err)
	}
	release()
	s.MemoryNormal = false
	if r, err := g.acquire(context.Background(), Options{ProjectDir: fixtureRepository(t)}); err == nil {
		r()
		t.Fatal("host pressure did not block other repo")
	}
}
func TestUnknownRepositoryOwnershipDefers(t *testing.T) {
	if _, err := ResolveScope(Options{ProjectDir: t.TempDir()}); err == nil {
		t.Fatal("nonrepository silently admitted")
	}
}

func TestFailedLsofBatchDiscardsPartialOwnership(t *testing.T) {
	partial := "p123\nn/other/repository\np456\n"
	dirs := lsofDirectories(partial, context.DeadlineExceeded)
	if len(dirs) != 0 {
		t.Fatalf("trusted partial failed telemetry: %v", dirs)
	}
	job := HeavyJob{PID: 123, Directory: dirs[123]}
	sample := filterUncovered(Sample{Jobs: []HeavyJob{job}}, Scope{Project: "/current/.git"})
	if len(sample.Uncovered) != 1 {
		t.Fatal("uncertain ownership failed open")
	}
	complete := lsofDirectories("p123\nn/other/repository\n", nil)
	if complete[123] != "/other/repository" {
		t.Fatal("complete telemetry rejected")
	}
}
