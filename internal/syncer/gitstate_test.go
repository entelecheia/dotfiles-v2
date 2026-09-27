package syncer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitStateFixtures are real-git sandboxes under t.TempDir(). They never touch
// the user's actual repositories.
func gitStateRun_(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dot-test", "GIT_AUTHOR_EMAIL=dot-test@example.invalid",
		"GIT_COMMITTER_NAME=dot-test", "GIT_COMMITTER_EMAIL=dot-test@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitStateInitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitStateRun_(t, dir, "init", "-q", "-b", "main")
}

func gitStateCommitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitStateRun_(t, dir, "add", name)
	gitStateRun_(t, dir, "commit", "-q", "-m", message)
	return gitStateRun_(t, dir, "rev-parse", "HEAD")
}

func gitStateHead(t *testing.T, dir string) string {
	t.Helper()
	return gitStateRun_(t, dir, "rev-parse", "HEAD")
}

// gitStateObjectCount counts files under .git/objects (loose and packed), the
// observable for "classification writes no objects".
func gitStateObjectCount(t *testing.T, repo string) int {
	t.Helper()
	gitdir := gitStateRun_(t, repo, "rev-parse", "--absolute-git-dir")
	count := 0
	err := filepath.Walk(filepath.Join(gitdir, "objects"), func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func gitStateFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// gitStateRewriteTracked overwrites a tracked file the way peer sync delivers
// it and backdates the mtime, so git's stat comparison can never mistake the
// rewrite for the indexed version no matter how fast the test runs.
func gitStateRewriteTracked(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func gitStateIndexBytes(t *testing.T, repo string) []byte {
	t.Helper()
	gitdir := gitStateRun_(t, repo, "rev-parse", "--absolute-git-dir")
	return gitStateFileBytes(t, filepath.Join(gitdir, "index"))
}

func gitStateReport(t *testing.T, res *GitStateResult, path string) *GitRepoReport {
	t.Helper()
	for _, rep := range res.Repos {
		if rep.Path == path {
			return rep
		}
	}
	t.Fatalf("no report for %q in %+v", path, res.Repos)
	return nil
}

// gitStateBehindFixture builds the AC1 scenario: a workspace whose submodule
// HEAD sits one commit behind the parent gitlink while the submodule worktree
// already matches the gitlink's tree. It returns the workspace root, the
// submodule path, the old HEAD (C1) and the gitlink target (C2).
func gitStateBehindFixture(t *testing.T) (ws, sub, c1, c2 string) {
	t.Helper()
	tmp := t.TempDir()

	subSrc := filepath.Join(tmp, "sub-src")
	gitStateInitRepo(t, subSrc)
	c1 = gitStateCommitFile(t, subSrc, "file.txt", "v1\n", "c1")
	c2 = gitStateCommitFile(t, subSrc, "file.txt", "v2\n", "c2")

	ws = filepath.Join(tmp, "ws")
	gitStateInitRepo(t, ws)
	gitStateRun_(t, ws, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
	gitStateRun_(t, ws, "commit", "-q", "-m", "add sub")

	sub = filepath.Join(ws, "sub")
	// HEAD moves back to C1; the file keeps the C2 content, the way peer sync
	// delivers it: files travel, HEAD stays behind.
	gitStateRun_(t, sub, "checkout", "-q", c1)
	gitStateRewriteTracked(t, filepath.Join(sub, "file.txt"), "v2\n")
	return ws, sub, c1, c2
}

// AC1: HEAD behind the gitlink, worktree identical to the gitlink tree.
// Realign moves HEAD to the descendant commit; the worktree stays
// byte-identical and no object is written. The undo record restores HEAD.
func TestPeerGitRealign_DescendantMatch(t *testing.T) {
	ws, sub, c1, c2 := gitStateBehindFixture(t)
	ctx := context.Background()

	res, err := PeerGitStatus(ctx, ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, "sub")
	if rep.Status != GitRepoRealignable {
		t.Fatalf("status = %q (%s), want realignable", rep.Status, rep.Reason)
	}
	if rep.Target != c2 {
		t.Fatalf("target = %q, want gitlink %q", rep.Target, c2)
	}
	if rep.TargetDiffs != 0 {
		t.Fatalf("target diffs = %d, want 0", rep.TargetDiffs)
	}

	objectsBefore := gitStateObjectCount(t, sub)
	worktreeBefore := gitStateFileBytes(t, filepath.Join(sub, "file.txt"))

	res, err = PeerGitRealign(ctx, ws, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rep = gitStateReport(t, res, "sub")
	if rep.Status != GitRepoRealigned {
		t.Fatalf("status = %q (%s), want realigned", rep.Status, rep.Reason)
	}
	if rep.PreviousHead != c1 {
		t.Fatalf("undo record = %q, want old HEAD %q", rep.PreviousHead, c1)
	}
	if got := gitStateHead(t, sub); got != c2 {
		t.Fatalf("HEAD = %q, want %q", got, c2)
	}
	if got := gitStateFileBytes(t, filepath.Join(sub, "file.txt")); string(got) != string(worktreeBefore) {
		t.Fatalf("worktree changed: got %q, want %q", got, worktreeBefore)
	}
	if got := gitStateObjectCount(t, sub); got != objectsBefore {
		t.Fatalf("object count changed: %d -> %d", objectsBefore, got)
	}
	if reflog := gitStateRun_(t, sub, "log", "-g", "-1", "--format=%gs"); !strings.Contains(reflog, "dot peer realign") {
		t.Fatalf("reflog subject %q does not record the realign", reflog)
	}

	// The undo record restores HEAD and index exactly.
	gitStateRun_(t, sub, "reset", "--mixed", "-q", rep.PreviousHead)
	if got := gitStateHead(t, sub); got != c1 {
		t.Fatalf("after undo HEAD = %q, want %q", got, c1)
	}
	if got := gitStateFileBytes(t, filepath.Join(sub, "file.txt")); string(got) != string(worktreeBefore) {
		t.Fatalf("undo changed the worktree: got %q, want %q", got, worktreeBefore)
	}
}

// The first-parent chain from HEAD to the branch upstream supplies the
// candidate when there is no parent gitlink. The command never fetches.
func TestPeerGitRealign_UpstreamChainCandidate(t *testing.T) {
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	c1 := gitStateCommitFile(t, origin, "file.txt", "v1\n", "c1")
	c2 := gitStateCommitFile(t, origin, "file.txt", "v2\n", "c2")

	ws := filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "clone", "-q", origin, ws)
	gitStateRun_(t, ws, "reset", "--hard", "-q", c1)
	gitStateRewriteTracked(t, filepath.Join(ws, "file.txt"), "v2\n")

	res, err := PeerGitRealign(context.Background(), ws, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, ".")
	if rep.Status != GitRepoRealigned || rep.Target != c2 {
		t.Fatalf("status = %q target = %q, want realigned -> %q", rep.Status, rep.Target, c2)
	}
}

// AC2: classification writes no objects and no worktree files. A modified
// tracked file and an untracked file are present so the content test actually
// runs; untracked files never block.
func TestPeerGitStatus_WritesNothing(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	gitStateInitRepo(t, repo)
	gitStateCommitFile(t, repo, "tracked.txt", "base\n", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	objectsBefore := gitStateObjectCount(t, repo)
	indexBefore := gitStateIndexBytes(t, repo)
	worktreeBefore := gitStateFileBytes(t, filepath.Join(repo, "tracked.txt"))

	res, err := PeerGitStatus(context.Background(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, ".")
	if rep.Status != GitRepoNoMatch {
		t.Fatalf("status = %q (%s), want no-match (no descendant candidate)", rep.Status, rep.Reason)
	}
	if rep.HeadDiffs != 1 {
		t.Fatalf("head diffs = %d, want 1 (untracked files never count)", rep.HeadDiffs)
	}

	if got := gitStateObjectCount(t, repo); got != objectsBefore {
		t.Fatalf("object count changed: %d -> %d", objectsBefore, got)
	}
	if got := gitStateIndexBytes(t, repo); string(got) != string(indexBefore) {
		t.Fatal("index changed during classification")
	}
	if got := gitStateFileBytes(t, filepath.Join(repo, "tracked.txt")); string(got) != string(worktreeBefore) {
		t.Fatal("worktree changed during classification")
	}
	if _, err := os.Stat(filepath.Join(repo, "untracked.txt")); err != nil {
		t.Fatal("untracked file removed during classification")
	}
}

// The default run is a preview: nothing under .git changes.
func TestPeerGitRealign_PreviewChangesNothing(t *testing.T) {
	ws, sub, c1, _ := gitStateBehindFixture(t)

	headBefore := gitStateHead(t, sub)
	indexBefore := gitStateIndexBytes(t, sub)
	objectsBefore := gitStateObjectCount(t, sub)

	res, err := PeerGitRealign(context.Background(), ws, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep := gitStateReport(t, res, "sub"); rep.Status != GitRepoRealignable {
		t.Fatalf("preview status = %q, want realignable", rep.Status)
	}
	if got := gitStateHead(t, sub); got != headBefore || got != c1 {
		t.Fatalf("HEAD moved in a preview: %q", got)
	}
	if got := gitStateIndexBytes(t, sub); string(got) != string(indexBefore) {
		t.Fatal("index changed in a preview")
	}
	if got := gitStateObjectCount(t, sub); got != objectsBefore {
		t.Fatalf("object count changed in a preview: %d -> %d", objectsBefore, got)
	}
}

// AC7: locked, in-progress, unmerged and staged repos are skipped and
// reported with a reason; an applied realign leaves them untouched.
func TestPeerGit_SkipConditions(t *testing.T) {
	newRepo := func(t *testing.T) string {
		repo := filepath.Join(t.TempDir(), "repo")
		gitStateInitRepo(t, repo)
		gitStateCommitFile(t, repo, "file.txt", "v1\n", "c1")
		return repo
	}
	gitdir := func(t *testing.T, repo string) string {
		return gitStateRun_(t, repo, "rev-parse", "--absolute-git-dir")
	}

	cases := []struct {
		name       string
		prepare    func(t *testing.T, repo string)
		wantReason string
	}{
		{
			name: "index lock",
			prepare: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(gitdir(t, repo), "index.lock"), []byte{}, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "index.lock present",
		},
		{
			name: "operation in progress",
			prepare: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(gitdir(t, repo), "MERGE_HEAD"), []byte("deadbeef\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantReason: "operation in progress (MERGE_HEAD)",
		},
		{
			name: "unmerged entries",
			prepare: func(t *testing.T, repo string) {
				gitStateRun_(t, repo, "checkout", "-q", "-b", "side")
				gitStateCommitFile(t, repo, "file.txt", "side\n", "side")
				gitStateRun_(t, repo, "checkout", "-q", "main")
				gitStateCommitFile(t, repo, "file.txt", "main\n", "main")
				cmd := exec.Command("git", "merge", "--no-commit", "side")
				cmd.Dir = repo
				// Identity, like gitStateRun_: hosts without a global git
				// identity (CI) refuse even a --no-commit merge, which would
				// silently leave a clean repo instead of the conflict fixture.
				cmd.Env = append(os.Environ(),
					"GIT_AUTHOR_NAME=dot-test", "GIT_AUTHOR_EMAIL=dot-test@example.invalid",
					"GIT_COMMITTER_NAME=dot-test", "GIT_COMMITTER_EMAIL=dot-test@example.invalid",
				)
				_ = cmd.Run() // the conflict is the fixture
				if out := gitStateRun_(t, repo, "ls-files", "-u"); strings.TrimSpace(out) == "" {
					t.Fatal("merge did not produce the conflict fixture")
				}
			},
			wantReason: "unmerged index entries",
		},
		{
			name: "staged changes",
			prepare: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitStateRun_(t, repo, "add", "file.txt")
			},
			wantReason: "staged changes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			tc.prepare(t, repo)
			headBefore := gitStateHead(t, repo)

			res, err := PeerGitStatus(context.Background(), repo, nil)
			if err != nil {
				t.Fatal(err)
			}
			rep := gitStateReport(t, res, ".")
			if rep.Status != GitRepoSkipped {
				t.Fatalf("status = %q, want skipped", rep.Status)
			}
			if rep.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", rep.Reason, tc.wantReason)
			}

			res, err = PeerGitRealign(context.Background(), repo, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if rep := gitStateReport(t, res, "."); rep.Status != GitRepoSkipped {
				t.Fatalf("realign status = %q, want skipped", rep.Status)
			}
			if got := gitStateHead(t, repo); got != headBefore {
				t.Fatalf("HEAD moved on a skipped repo: %q -> %q", headBefore, got)
			}
		})
	}
}

// AC7: a parent gitlink naming a commit the repo does not have marks that
// repo unresolvable, and the run continues with the other repos.
func TestPeerGit_MissingGitlinkObject(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "ws")
	gitStateInitRepo(t, ws)
	gitStateCommitFile(t, ws, "root.txt", "root\n", "root")

	sub := filepath.Join(ws, "sub")
	gitStateInitRepo(t, sub)
	gitStateCommitFile(t, sub, "file.txt", "v1\n", "c1")
	// The repo must need a realign for the vanished gitlink to matter; a
	// clean repo never looks at its candidates.
	gitStateRewriteTracked(t, filepath.Join(sub, "file.txt"), "v2\n")

	dead := "0123456789012345678901234567890123456789"
	gitStateRun_(t, ws, "update-index", "--add", "--cacheinfo", "160000,"+dead+",sub")
	gitStateRun_(t, ws, "commit", "-q", "-m", "record dead gitlink")

	res, err := PeerGitStatus(context.Background(), ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, "sub")
	if rep.Status != GitRepoUnresolvable {
		t.Fatalf("status = %q, want unresolvable", rep.Status)
	}
	if !strings.Contains(rep.Reason, "gitlink") {
		t.Fatalf("reason = %q, want a gitlink explanation", rep.Reason)
	}
	// The run continued: the root repo was classified too.
	if root := gitStateReport(t, res, "."); root.Status == "" {
		t.Fatal("root repo was not classified")
	}
}

// A linked worktree at a gitlink path is listed but never realigned.
func TestPeerGit_LinkedWorktreeListedNotRealigned(t *testing.T) {
	tmp := t.TempDir()
	subSrc := filepath.Join(tmp, "sub-src")
	gitStateInitRepo(t, subSrc)
	c1 := gitStateCommitFile(t, subSrc, "file.txt", "v1\n", "c1")

	ws := filepath.Join(tmp, "ws")
	gitStateInitRepo(t, ws)
	gitStateRun_(t, ws, "update-index", "--add", "--cacheinfo", "160000,"+c1+",sub")
	gitStateRun_(t, ws, "commit", "-q", "-m", "record gitlink")

	sub := filepath.Join(ws, "sub")
	gitStateRun_(t, subSrc, "worktree", "add", "--detach", sub, c1)

	res, err := PeerGitRealign(context.Background(), ws, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, "sub")
	if rep.Status != GitRepoLinkedWorktree {
		t.Fatalf("status = %q (%s), want linked-worktree", rep.Status, rep.Reason)
	}
	if got := gitStateHead(t, sub); got != c1 {
		t.Fatalf("linked worktree HEAD moved: %q", got)
	}
}

// Optional repo arguments restrict the run; an unknown path is an error
// before anything mutates.
func TestPeerGit_RepoRestriction(t *testing.T) {
	ws, _, _, _ := gitStateBehindFixture(t)
	ctx := context.Background()

	res, err := PeerGitStatus(ctx, ws, []string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Path != "sub" {
		t.Fatalf("restricted run reported %+v, want only sub", res.Repos)
	}

	if _, err := PeerGitStatus(ctx, ws, []string{"nosuch"}); err == nil {
		t.Fatal("unknown repo argument did not error")
	}
	if _, err := PeerGitRealign(ctx, ws, []string{"nosuch"}, true); err == nil {
		t.Fatal("unknown repo argument did not error before an applied realign")
	}
}

// A strict descendant whose tree equals HEAD's (an empty commit, or a
// sequence whose net tree is unchanged) must still be offered: HEAD
// otherwise stays behind forever even though the files already match.
func TestPeerGitRealign_SameTreeDescendant(t *testing.T) {
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	c1 := gitStateCommitFile(t, origin, "file.txt", "v1\n", "c1")
	gitStateRun_(t, origin, "commit", "-q", "--allow-empty", "-m", "empty")
	c2 := gitStateHead(t, origin)

	ws := filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "clone", "-q", origin, ws)
	gitStateRun_(t, ws, "reset", "--hard", "-q", c1)

	res, err := PeerGitStatus(context.Background(), ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := gitStateReport(t, res, ".")
	if rep.Status != GitRepoRealignable || rep.Target != c2 {
		t.Fatalf("status = %q target = %q, want realignable -> same-tree descendant %q", rep.Status, rep.Target, c2)
	}

	res, err = PeerGitRealign(context.Background(), ws, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	rep = gitStateReport(t, res, ".")
	if rep.Status != GitRepoRealigned {
		t.Fatalf("status = %q (%s), want realigned", rep.Status, rep.Reason)
	}
	if got := gitStateHead(t, ws); got != c2 {
		t.Fatalf("HEAD = %q, want %q", got, c2)
	}
}
