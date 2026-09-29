package syncer

import (
	"context"
	"encoding/json"
	"fmt"
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

// A pushed feature branch at its upstream tip, with WIP in files the feature
// added, is at-tip: main does not track those files, so it must not look
// closer to the worktree than HEAD (they would turn untracked on a switch).
func TestPeerGitClass_FeatureWIPIsNotABranchMismatch(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feat")
	gitStateCommitFile(t, f.ws, "n1.txt", "n1\n", "n1")
	gitStateCommitFile(t, f.ws, "n2.txt", "n2\n", "n2")
	gitStateRun_(t, f.ws, "push", "-q", "-u", "origin", "feat")
	f.publish(t, "a.txt", "a2\n")
	gitStateRewriteTracked(t, filepath.Join(f.ws, "n1.txt"), "wip1\n")
	gitStateRewriteTracked(t, filepath.Join(f.ws, "n2.txt"), "wip2\n")
	requireClass(t, rescueRealign(t, f.ws, RealignOptions{}), GitClassAtTip, "only uncommitted changes differ")
}

// Local commits ahead of a feature upstream whose files the worktree holds
// (what peer sync delivers from the Mac that pushed it) are ahead-unpushed,
// even when a main commit is closer than HEAD: the upstream itself matches.
func TestPeerGitClass_AheadOfMatchingUpstreamIsNotABranchMismatch(t *testing.T) {
	f := newRescueFixture(t)
	f.publish(t, "b.txt", "b1\n")
	gitStateRun_(t, f.ws, "merge", "-q", "--ff-only", "origin/main")
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feat")
	gitStateCommitFile(t, f.ws, "a.txt", "f1\n", "a f1")
	pushed := gitStateCommitFile(t, f.ws, "b.txt", "f1\n", "b f1")
	gitStateRun_(t, f.ws, "push", "-q", "-u", "origin", "feat")
	for _, v := range []string{"f2", "f3", "f4"} {
		gitStateCommitFile(t, f.ws, "a.txt", v+"\n", "a "+v)
		gitStateCommitFile(t, f.ws, "b.txt", v+"\n", "b "+v)
	}
	f.publish(t, "a.txt", "f1\n") // main: a.txt matches, b.txt does not
	f.deliver(t, pushed)
	requireClass(t, rescueRealign(t, f.ws, RealignOptions{}), GitClassAheadUnpushed, "local-only commit")
}

// A rescue branch goes where git would push the branch: a fork setup's
// pushRemote, not the fetch upstream.
func TestRescuePushRemoteFollowsGitsPushOrder(t *testing.T) {
	repo := t.TempDir()
	gitStateInitRepo(t, repo)
	r := &gitStateRun{git: "git"}
	ctx := context.Background()
	for _, tc := range []struct{ key, value, want string }{
		{"", "", "origin"},
		{"branch.feat.remote", "upstream", "upstream"},
		{"remote.pushDefault", "mine", "mine"},
		{"branch.feat.pushRemote", "fork", "fork"},
	} {
		if tc.key != "" {
			gitStateRun_(t, repo, "config", tc.key, tc.value)
		}
		if got := r.pushRemote(ctx, repo, "feat"); got != tc.want {
			t.Fatalf("after %s=%s: push remote %q, want %q", tc.key, tc.value, got, tc.want)
		}
	}
}

// A branch tracking a local branch pushes into this repo ("."), so its
// rescue branch stays local instead of landing on origin.
func TestPeerGitRescue_LocalUpstreamKeepsTheRescueLocal(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feat", "--track", "main")
	gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
	tip := f.publish(t, "a.txt", "a2\n")
	gitStateRun_(t, f.ws, "update-ref", "refs/heads/main", tip)
	f.deliver(t, tip)

	r := &gitStateRun{git: "git"}
	if got := r.pushRemote(context.Background(), f.ws, "feat"); got != "." {
		t.Fatalf("push remote %q, want .", got)
	}
	rep := rescueRealign(t, f.ws, RealignOptions{Rescue: true})
	if rep.Class != GitClassDiverged || rep.RescueRemote != "" {
		t.Fatalf("class %q rescue remote %q, want diverged kept local", rep.Class, rep.RescueRemote)
	}
	if !strings.Contains(rep.Suggestion, "on main") || strings.Contains(rep.Suggestion, "refs/heads/") {
		t.Fatalf("suggestion names the upstream as %q", rep.Suggestion)
	}
}

// A rescue target is chosen like a realign's: of the upstream commits that
// tie on the files, one recording a gitlink the child has not reached loses,
// so a diverged parent does not move past an undelivered bump and record a
// rewind (#189 round 15).
func TestPeerGitRescue_TieFollowsTheChildren(t *testing.T) {
	for _, fetched := range []bool{true, false} {
		t.Run(fmt.Sprintf("fetched=%v", fetched), func(t *testing.T) {
			tmp := t.TempDir()
			subSrc := filepath.Join(tmp, "sub-src")
			gitStateInitRepo(t, subSrc)
			gitStateCommitFile(t, subSrc, "file.txt", "v1\n", "s1")

			origin := filepath.Join(tmp, "origin")
			gitStateInitRepo(t, origin)
			gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
			gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
			gitStateRun_(t, origin, "commit", "-q", "-m", "p0")
			ws := filepath.Join(tmp, "ws")
			gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)

			p1 := gitStateCommitFile(t, origin, "readme.md", "parent v2\n", "p1")
			s2 := gitStateCommitFile(t, subSrc, "file.txt", "v2\n", "s2")
			gitStateRun_(t, origin, "-C", "sub", "fetch", "-q")
			gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s2)
			gitStateRun_(t, origin, "add", "sub")
			gitStateRun_(t, origin, "commit", "-q", "-m", "p2 bump sub only")

			// Diverged: one local commit whose file peer sync removed, and
			// p1's readme delivered; the child keeps s1's files.
			gitStateCommitFile(t, ws, "docs.md", "unpushed\n", "local docs")
			if err := os.Remove(filepath.Join(ws, "docs.md")); err != nil {
				t.Fatal(err)
			}
			gitStateRewriteTracked(t, filepath.Join(ws, "readme.md"), "parent v2\n")
			gitStateRun_(t, ws, "fetch", "-q", "--no-recurse-submodules")
			if fetched {
				gitStateRun_(t, ws, "-C", "sub", "fetch", "-q")
			}

			rep := rescueRealign(t, ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
			if rep.Status != GitRepoRealigned || gitStateHead(t, ws) != p1 {
				t.Fatalf("rescue = %+v, HEAD %s, want realigned to %s", rep, gitStateHead(t, ws), p1)
			}
			if log := gitStateRun_(t, ws, "diff", "--submodule=log"); strings.Contains(log, "rewind") {
				t.Fatalf("the parent records a rewind:\n%s", log)
			}
		})
	}
}

// A child's rescue has two endings, its target or HEAD when the rescue
// fails (a push, a ref it cannot create), and the parent has moved by then,
// so it must hold for both: it never records a commit the child lacks
// (#189 rounds 18 and 19). A parent whose child is rescued follows it on
// the next run.
func TestPeerGitRescue_ParentHoldsForEitherRescueEnding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mismatch bool // a branch-mismatch child, else a diverged one
		push     bool
		fail     bool
	}{
		{"diverged, local rescue", false, false, false},
		{"diverged, failed push", false, true, true},
		{"branch mismatch, pushed", true, true, false},
		{"branch mismatch, failed push", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			subSrc := filepath.Join(tmp, "sub-src")
			gitStateInitRepo(t, subSrc)
			s0 := gitStateCommitFile(t, subSrc, "a.txt", "0\n", "s0")
			var mid, tip string // the parent's middle and newest records
			if tc.mismatch {
				gitStateRun_(t, subSrc, "checkout", "-q", "-b", "feat")
				mid = gitStateCommitFile(t, subSrc, "feat.txt", "1\n", "f1")
				gitStateRun_(t, subSrc, "checkout", "-q", "main")
				gitStateCommitFile(t, subSrc, "feat.txt", "1\n", "S1 squash of f1")
				tip = gitStateCommitFile(t, subSrc, "a.txt", "2\n", "S2")
			} else {
				tip = gitStateCommitFile(t, subSrc, "a.txt", "u\n", "su")
			}
			origin := filepath.Join(tmp, "origin")
			gitStateInitRepo(t, origin)
			gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
			gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
			var records []string
			for _, sha := range []string{s0, mid, tip} {
				if sha == "" {
					continue
				}
				gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", sha)
				gitStateRun_(t, origin, "add", "sub")
				gitStateRun_(t, origin, "commit", "-q", "-m", "p@"+shortRev(sha))
				records = append(records, gitStateHead(t, origin))
			}
			p0, pTip := records[0], records[len(records)-1]

			ws := filepath.Join(tmp, "ws")
			gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)
			gitStateRun_(t, ws, "reset", "-q", "--hard", p0)
			gitStateRun_(t, ws, "-c", "protocol.file.allow=always", "submodule", "update", "-q")
			sub := filepath.Join(ws, "sub")
			var held string // the child's HEAD when its rescue fails
			if tc.mismatch {
				// On a feature branch whose work landed on main, main's tip
				// delivered.
				gitStateRun_(t, sub, "checkout", "-q", "-B", "feat", "origin/feat")
				gitStateRewriteTracked(t, filepath.Join(sub, "a.txt"), "2\n")
				held = mid
			} else {
				// Diverged: one local commit on main, su's files delivered.
				gitStateRun_(t, sub, "checkout", "-q", "-B", "main", s0)
				gitStateRun_(t, sub, "branch", "-q", "--set-upstream-to=origin/main")
				held = gitStateCommitFile(t, sub, "docs.md", "mine\n", "local docs")
				if err := os.Remove(filepath.Join(sub, "docs.md")); err != nil {
					t.Fatal(err)
				}
				gitStateRewriteTracked(t, filepath.Join(sub, "a.txt"), "u\n")
			}
			if tc.fail {
				gitStateRun_(t, sub, "remote", "set-url", "--push", "origin", filepath.Join(tmp, "nowhere"))
			}

			opts := RealignOptions{Apply: true, Rescue: true, NoPush: !tc.push, Now: func() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) }}
			if _, err := PeerGitRealign(context.Background(), ws, nil, opts); err != nil {
				t.Fatal(err)
			}
			want := tip
			if tc.fail {
				want = held
			}
			if gitStateHead(t, ws) != p0 || gitStateHead(t, sub) != want {
				t.Fatalf("parent %s child %s, want the parent kept at p0 and the child at %s", shortRev(gitStateHead(t, ws)), shortRev(gitStateHead(t, sub)), shortRev(want))
			}
			// "<" lists a commit the parent records that the child lacks.
			if log := gitStateRun_(t, ws, "diff", "--submodule=log"); strings.Contains(log, "  <") {
				t.Fatalf("the parent records a commit the child did not take:\n%s", log)
			}
			if tc.fail {
				return
			}
			// The next run follows the rescued child.
			if _, err := PeerGitRealign(context.Background(), ws, nil, opts); err != nil {
				t.Fatal(err)
			}
			if got := gitStateHead(t, ws); got != pTip {
				t.Fatalf("second run: parent %s, want %s", shortRev(got), shortRev(pTip))
			}
		})
	}
}

// A child whose rescue is planned and whose files match its rescue target
// exactly cannot be where a candidate's missing commit is, so that
// candidate drops out, with --fetch too (#189 round 20).
func TestPeerGitRescue_ChildAtItsRescueTargetRulesOutAMissingCommit(t *testing.T) {
	for _, fetch := range []bool{false, true} {
		t.Run(fmt.Sprintf("fetch=%v", fetch), func(t *testing.T) {
			tmp := t.TempDir()
			subSrc := filepath.Join(tmp, "sub-src")
			gitStateInitRepo(t, subSrc)
			s0 := gitStateCommitFile(t, subSrc, "a.txt", "0\n", "s0")
			s1 := gitStateCommitFile(t, subSrc, "a.txt", "1\n", "s1")
			origin := filepath.Join(tmp, "origin")
			gitStateInitRepo(t, origin)
			gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
			gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
			gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s0)
			gitStateRun_(t, origin, "add", "sub")
			gitStateRun_(t, origin, "commit", "-q", "-m", "p0")
			p0 := gitStateHead(t, origin)
			ws := filepath.Join(tmp, "ws")
			gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)
			// s2 exists only upstream: the child never fetched it.
			s2 := gitStateCommitFile(t, subSrc, "a.txt", "2\n", "s2")
			gitStateRun_(t, origin, "-C", "sub", "fetch", "-q")
			gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s2)
			gitStateRun_(t, origin, "add", "sub")
			gitStateRun_(t, origin, "commit", "-q", "-m", "p1 bump sub to s2")
			gitStateRun_(t, ws, "fetch", "-q", "--no-recurse-submodules")

			sub := filepath.Join(ws, "sub")
			gitStateRun_(t, sub, "checkout", "-q", "-B", "main", s0)
			gitStateRun_(t, sub, "update-ref", "refs/remotes/origin/main", s1)
			gitStateRun_(t, sub, "branch", "-q", "--set-upstream-to=origin/main")
			gitStateCommitFile(t, sub, "docs.md", "mine\n", "local docs")
			if err := os.Remove(filepath.Join(sub, "docs.md")); err != nil {
				t.Fatal(err)
			}
			gitStateRewriteTracked(t, filepath.Join(sub, "a.txt"), "1\n") // s1's files exactly

			opts := RealignOptions{Apply: true, Rescue: true, NoPush: true, Fetch: fetch, Now: func() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) }}
			if _, err := PeerGitRealign(context.Background(), ws, nil, opts); err != nil {
				t.Fatal(err)
			}
			if gitStateHead(t, ws) != p0 || gitStateHead(t, sub) != s1 {
				t.Fatalf("parent %s child %s, want p0 kept and the child rescued to s1", shortRev(gitStateHead(t, ws)), shortRev(gitStateHead(t, sub)))
			}
			if log := gitStateRun_(t, ws, "diff", "--submodule=log"); strings.Contains(log, "  <") || strings.Contains(log, "not present") {
				t.Fatalf("the parent records a commit the child lacks:\n%s", log)
			}
		})
	}
}

// A diverged parent's rescue past a child it leaves behind says so in the
// tie line (#189 round 20).
func TestPeerGitRescue_ParentRescuePastAChildSaysSo(t *testing.T) {
	tmp := t.TempDir()
	subSrc := filepath.Join(tmp, "sub-src")
	gitStateInitRepo(t, subSrc)
	gitStateCommitFile(t, subSrc, "f.txt", "0\n", "s0")
	s1 := gitStateCommitFile(t, subSrc, "f.txt", "1\n", "s1")
	s2 := gitStateCommitFile(t, subSrc, "f.txt", "2\n", "s2")
	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
	gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
	gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s1)
	gitStateRun_(t, origin, "add", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "p0")
	ws := filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)
	gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s2)
	gitStateRun_(t, origin, "add", "sub")
	gitStateCommitFile(t, origin, "readme.md", "parent v2\n", "p1 readme and bump s2")
	gitStateRun_(t, ws, "fetch", "-q", "--no-recurse-submodules")

	// Diverged root: a local commit whose file peer sync removed, p1's readme
	// delivered; the child keeps s1's files.
	gitStateCommitFile(t, ws, "docs.md", "mine\n", "local docs")
	if err := os.Remove(filepath.Join(ws, "docs.md")); err != nil {
		t.Fatal(err)
	}
	gitStateRewriteTracked(t, filepath.Join(ws, "readme.md"), "parent v2\n")

	rep := rescueRealign(t, ws, RealignOptions{Rescue: true, NoPush: true})
	if rep.Status != GitRepoRealignable || !strings.Contains(rep.TieBreak, "is past what sub holds") {
		t.Fatalf("rescue = %+v, want a move whose tie line names sub", rep)
	}
}
