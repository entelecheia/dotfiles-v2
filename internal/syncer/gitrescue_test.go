package syncer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rescueFixture is a bare origin, a writer clone that publishes commits, and
// the workspace clone under test. The writer's commits reach the workspace's
// remote-tracking refs through a fetch, never its HEAD.
type rescueFixture struct {
	origin, writer, ws string
	base               string
}

func newRescueFixture(t *testing.T) *rescueFixture {
	t.Helper()
	tmp := t.TempDir()
	f := &rescueFixture{origin: filepath.Join(tmp, "origin.git"), writer: filepath.Join(tmp, "writer"), ws: filepath.Join(tmp, "ws")}
	gitStateRun_(t, tmp, "init", "-q", "--bare", "-b", "main", f.origin)
	gitStateRun_(t, tmp, "clone", "-q", f.origin, f.writer)
	// A clone of an empty repo takes the host's init.defaultBranch.
	gitStateRun_(t, f.writer, "symbolic-ref", "HEAD", "refs/heads/main")
	f.base = gitStateCommitFile(t, f.writer, "a.txt", "a1\n", "base")
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, tmp, "clone", "-q", f.origin, f.ws)
	return f
}

// publish commits content on the writer's main, pushes it and fetches it into
// the workspace, returning the new tip.
func (f *rescueFixture) publish(t *testing.T, name, content string) string {
	t.Helper()
	sha := gitStateCommitFile(t, f.writer, name, content, "writer "+name)
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	return sha
}

// deliver makes the workspace files equal to commit sha's tree, the way peer
// sync delivers the other Mac's files, without touching HEAD or the index.
func (f *rescueFixture) deliver(t *testing.T, sha string) {
	t.Helper()
	for _, name := range strings.Fields(gitStateRun_(t, f.ws, "ls-tree", "--name-only", sha)) {
		gitStateRewriteTracked(t, filepath.Join(f.ws, name), gitStateRun_(t, f.ws, "show", sha+":"+name)+"\n")
	}
	for _, name := range strings.Fields(gitStateRun_(t, f.ws, "ls-tree", "--name-only", "HEAD")) {
		if out := gitStateRun_(t, f.ws, "ls-tree", "--name-only", sha, name); out == "" {
			_ = os.Remove(filepath.Join(f.ws, name))
		}
	}
}

func rescueRealign(t *testing.T, ws string, opts RealignOptions) *GitRepoReport {
	t.Helper()
	opts.Now = func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
	res, err := PeerGitRealign(context.Background(), ws, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	return gitStateReport(t, res, ".")
}

func requireClass(t *testing.T, rep *GitRepoReport, class, suggestion string) {
	t.Helper()
	if rep.Status != GitRepoNoMatch && rep.Status != GitRepoSkipped {
		t.Fatalf("status = %q (%s), want no-match or skipped", rep.Status, rep.Reason)
	}
	if rep.Class != class || !strings.Contains(rep.Suggestion, suggestion) {
		t.Fatalf("class %q suggestion %q, want %q containing %q", rep.Class, rep.Suggestion, class, suggestion)
	}
	doc, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"class":"`+class+`"`) || !strings.Contains(string(doc), `"suggestion":`) {
		t.Fatalf("--json document lacks class/suggestion: %s", doc)
	}
}

func TestPeerGitClass_AtTip(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRewriteTracked(t, filepath.Join(f.ws, "a.txt"), "shared wip\n")
	requireClass(t, rescueRealign(t, f.ws, RealignOptions{}), GitClassAtTip, "only uncommitted changes differ")
}

func TestPeerGitClass_AheadUnpushed(t *testing.T) {
	f := newRescueFixture(t)
	gitStateCommitFile(t, f.ws, "local.txt", "mine\n", "local only")
	gitStateRewriteTracked(t, filepath.Join(f.ws, "a.txt"), "wip\n")
	requireClass(t, rescueRealign(t, f.ws, RealignOptions{}), GitClassAheadUnpushed, "1 local-only commit(s) on main; push them")
}

// The seven-repo case: one unpushed local commit on main, while the other
// Mac published two commits whose files peer sync delivered.
func divergedFixture(t *testing.T) (*rescueFixture, string, string) {
	f := newRescueFixture(t)
	local := gitStateCommitFile(t, f.ws, "docs.md", "unpushed docs\n", "local docs")
	f.publish(t, "b.txt", "b1\n")
	tip := f.publish(t, "b.txt", "b2\n")
	f.deliver(t, tip)
	return f, local, tip
}

func TestPeerGitClass_DivergedAndRescue(t *testing.T) {
	f, local, tip := divergedFixture(t)
	rep := rescueRealign(t, f.ws, RealignOptions{})
	requireClass(t, rep, GitClassDiverged, "1 local-only commit(s) vs 2 on origin/main")
	if rep.RescueTarget != tip || !strings.Contains(rep.Suggestion, "--rescue --apply") {
		t.Fatalf("rescue target %q suggestion %q, want %q", rep.RescueTarget, rep.Suggestion, tip)
	}

	preview := rescueRealign(t, f.ws, RealignOptions{Rescue: true})
	if preview.Status != GitRepoRealignable || preview.Target != tip || preview.Rescue != "rescue/260928-main" {
		t.Fatalf("rescue preview = %+v", preview)
	}
	if got := gitStateHead(t, f.ws); got != local {
		t.Fatalf("preview moved HEAD to %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.ws, ".git", "refs", "heads", "rescue")); err == nil {
		t.Fatal("preview created a rescue branch")
	}

	before := map[string][]byte{}
	for _, name := range []string{"a.txt", "b.txt"} {
		before[name] = gitStateFileBytes(t, filepath.Join(f.ws, name))
	}
	applied := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
	if applied.Status != GitRepoRealigned || !applied.RescuePushed {
		t.Fatalf("rescue apply = %+v", applied)
	}
	if got := gitStateHead(t, f.ws); got != tip {
		t.Fatalf("HEAD = %s, want %s", got, tip)
	}
	if got := gitStateRun_(t, f.ws, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("HEAD left main: %s", got)
	}
	if got := gitStateRun_(t, f.ws, "rev-parse", "refs/heads/rescue/260928-main"); got != local {
		t.Fatalf("local rescue branch = %s, want the local commit %s", got, local)
	}
	if got := gitStateRun_(t, f.origin, "rev-parse", "refs/heads/rescue/260928-main"); got != local {
		t.Fatalf("pushed rescue branch = %s, want %s", got, local)
	}
	for name, want := range before {
		if got := gitStateFileBytes(t, filepath.Join(f.ws, name)); string(got) != string(want) {
			t.Fatalf("worktree %s changed: %q -> %q", name, want, got)
		}
	}
	if out := gitStateRun_(t, f.ws, "status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Fatalf("index not aligned with the files:\n%s", out)
	}
}

func TestPeerGitRescue_NoPushKeepsBranchLocal(t *testing.T) {
	f, local, tip := divergedFixture(t)
	rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
	if rep.Status != GitRepoRealigned || rep.RescuePushed || gitStateHead(t, f.ws) != tip {
		t.Fatalf("rescue --no-push = %+v", rep)
	}
	if got := gitStateRun_(t, f.ws, "rev-parse", "refs/heads/rescue/260928-main"); got != local {
		t.Fatalf("rescue branch = %s, want %s", got, local)
	}
	if out := gitStateRun_(t, f.origin, "branch", "--list", "rescue/*"); out != "" {
		t.Fatalf("--no-push pushed: %s", out)
	}
}

// The dev/maru case: a feature branch whose work was squash-merged upstream,
// with the files matching main.
func TestPeerGitClass_BranchMismatchAndRescue(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
	gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
	featTip := gitStateCommitFile(t, f.ws, "feat.txt", "f2\n", "f2")
	f.publish(t, "feat.txt", "f2\n") // the squash merge
	mainTip := f.publish(t, "a.txt", "a2\n")
	f.deliver(t, mainTip)

	rep := rescueRealign(t, f.ws, RealignOptions{})
	requireClass(t, rep, GitClassBranchMismatch, "on feature, but the files match main")
	if rep.RescueTarget != mainTip {
		t.Fatalf("rescue target = %q, want main's tip %q", rep.RescueTarget, mainTip)
	}

	applied := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
	if applied.Status != GitRepoRealigned || !strings.Contains(applied.Undo, "symbolic-ref HEAD refs/heads/feature") {
		t.Fatalf("rescue apply = %+v", applied)
	}
	if got := gitStateRun_(t, f.ws, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("HEAD on %s, want main", got)
	}
	if got := gitStateHead(t, f.ws); got != mainTip {
		t.Fatalf("HEAD = %s, want %s", got, mainTip)
	}
	if got := gitStateRun_(t, f.ws, "rev-parse", "refs/heads/rescue/260928-feature"); got != featTip {
		t.Fatalf("rescue branch = %s, want %s", got, featTip)
	}
	if out := gitStateRun_(t, f.ws, "status", "--porcelain", "--untracked-files=no"); out != "" {
		t.Fatalf("index not aligned with the files:\n%s", out)
	}
}

func TestPeerGitClass_StaleRebaseHead(t *testing.T) {
	f := newRescueFixture(t)
	if err := os.WriteFile(filepath.Join(f.ws, ".git", "REBASE_HEAD"), []byte(f.base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireClass(t, rescueRealign(t, f.ws, RealignOptions{}), GitClassStaleRebaseHead, "update-ref -d REBASE_HEAD")

	// With a rebase directory it is a real operation in progress.
	if err := os.MkdirAll(filepath.Join(f.ws, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rep := rescueRealign(t, f.ws, RealignOptions{}); rep.Class != "" || !strings.Contains(rep.Reason, "operation in progress") {
		t.Fatalf("real rebase reported as %q (%s)", rep.Class, rep.Reason)
	}
}

// With the index lock held, a HEAD that moved since the plan (a commit made
// during the rescue push) stops the switch and leaves the repo untouched.
func TestPeerGitRescue_SwitchRefusesAMovedHead(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
	planned := gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
	mainTip := f.publish(t, "a.txt", "a2\n")
	moved := gitStateCommitFile(t, f.ws, "feat.txt", "f2\n", "made during the push")

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	run := &gitStateRun{git: gitPath}
	gitdir := gitStateRun_(t, f.ws, "rev-parse", "--absolute-git-dir")
	rep := &GitRepoReport{Path: ".", Head: planned, Target: mainTip, Rescue: "rescue/x", branch: "feature", rescueBranch: "main"}
	run.switchBranch(context.Background(), f.ws, gitdir, rep)
	if rep.Status != GitRepoSkipped || !strings.Contains(rep.Reason, "HEAD moved") {
		t.Fatalf("rep = %+v", rep)
	}
	if got := gitStateRun_(t, f.ws, "symbolic-ref", "--short", "HEAD"); got != "feature" || gitStateHead(t, f.ws) != moved {
		t.Fatalf("repo changed: HEAD on %s at %s", got, gitStateHead(t, f.ws))
	}
	if _, err := os.Stat(filepath.Join(gitdir, "index.lock")); !os.IsNotExist(err) {
		t.Fatal("index.lock left behind")
	}
}

// A default branch the rescue creates tracks origin's, so later realigns
// and pulls have an upstream.
func TestPeerGitRescue_CreatedDefaultBranchTracksOrigin(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
	gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
	gitStateRun_(t, f.ws, "branch", "-q", "-D", "main")
	mainTip := f.publish(t, "a.txt", "a2\n")
	f.deliver(t, mainTip)

	preview := rescueRealign(t, f.ws, RealignOptions{Rescue: true, NoPush: true})
	if preview.RescueRemote != "" {
		t.Fatalf("--no-push preview plans a push to %q", preview.RescueRemote)
	}
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned {
		t.Fatalf("rescue = %+v", rep)
	}
	if got := gitStateRun_(t, f.ws, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Fatalf("main upstream = %q", got)
	}
}

// A default branch checked out in a linked worktree is not moved under it.
func TestPeerGitRescue_SkipsDefaultBranchCheckedOutElsewhere(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
	gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
	mainTip := f.publish(t, "a.txt", "a2\n")
	f.deliver(t, mainTip)
	wt := filepath.Join(t.TempDir(), "wt-main")
	gitStateRun_(t, f.ws, "worktree", "add", "-q", wt, "main")
	before := gitStateRun_(t, f.ws, "rev-parse", "main")

	// Refused at plan time: no rescue branch is created for a move that
	// cannot happen.
	rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
	if rep.Status != GitRepoNoMatch || !strings.Contains(rep.Suggestion, "linked worktree") {
		t.Fatalf("rescue = %+v", rep)
	}
	if out := gitStateRun_(t, f.ws, "branch", "--list", "rescue/*"); out != "" {
		t.Fatalf("a rescue branch was created: %s", out)
	}
	if got := gitStateRun_(t, f.ws, "rev-parse", "main"); got != before {
		t.Fatalf("main moved to %s under its worktree", got)
	}
	if out := gitStateRun_(t, wt, "status", "--porcelain"); out != "" {
		t.Fatalf("the linked worktree shows changes:\n%s", out)
	}
}

// A feature branch rebased and force-pushed on the other Mac is diverged
// from its own upstream, whose tip the files match; it is not a branch
// whose work landed on main.
func TestPeerGitClass_ForcePushedFeatureIsDivergedFromItsUpstream(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.writer, "checkout", "-q", "-b", "feature")
	gitStateCommitFile(t, f.writer, "feat.txt", "f1\n", "f1")
	gitStateRun_(t, f.writer, "push", "-q", "-u", "origin", "feature")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature", "--track", "origin/feature")
	gitStateRun_(t, f.writer, "commit", "-q", "--amend", "-m", "f1 rebased")
	gitStateCommitFile(t, f.writer, "feat.txt", "f2\n", "f2")
	gitStateRun_(t, f.writer, "push", "-q", "-f", "origin", "feature")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	tip := gitStateRun_(t, f.ws, "rev-parse", "origin/feature")
	f.deliver(t, tip)

	rep := rescueRealign(t, f.ws, RealignOptions{})
	if rep.Class != GitClassDiverged || rep.RescueTarget != tip {
		t.Fatalf("class %q target %q, want diverged -> origin/feature %s", rep.Class, rep.RescueTarget, tip)
	}
}
