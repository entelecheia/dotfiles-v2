package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
	for _, tc := range []struct{ rescue, fetch bool }{{true, false}, {true, true}, {false, false}} {
		rescue, fetch := tc.rescue, tc.fetch
		t.Run(fmt.Sprintf("rescue=%v fetch=%v", rescue, fetch), func(t *testing.T) {
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

			opts := RealignOptions{Apply: true, Rescue: rescue, NoPush: true, Fetch: fetch, Now: func() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) }}
			local := gitStateHead(t, sub)
			if _, err := PeerGitRealign(context.Background(), ws, nil, opts); err != nil {
				t.Fatal(err)
			}
			// Without --rescue the child stays diverged; the parent stays
			// either way (#201).
			want := s1
			if !rescue {
				want = local
			}
			if gitStateHead(t, ws) != p0 || gitStateHead(t, sub) != want {
				t.Fatalf("parent %s child %s, want p0 kept and the child at %s", shortRev(gitStateHead(t, ws)), shortRev(gitStateHead(t, sub)), shortRev(want))
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
	// Without --rescue nothing moves, and no tie line describes the rescue
	// (round 21).
	if rep := rescueRealign(t, ws, RealignOptions{}); rep.Status != GitRepoNoMatch || rep.TieBreak != "" {
		t.Fatalf("no rescue = %+v, want no-match with no tie line", rep)
	}
}

// A rescue that follows a no-match where HEAD won its own tie shows the
// rescue's tie line, with the children it passes, not "HEAD stays"
// (#189 round 21).
func TestPeerGitRescue_RescueAfterAHeadTieShowsItsOwnTieLine(t *testing.T) {
	tmp := t.TempDir()
	subSrc := filepath.Join(tmp, "sub-src")
	gitStateInitRepo(t, subSrc)
	s0 := gitStateCommitFile(t, subSrc, "f.txt", "0\n", "s0")
	s1 := gitStateCommitFile(t, subSrc, "f.txt", "1\n", "s1")

	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	gitStateCommitFile(t, origin, "a.txt", "0\n", "a")
	gitStateCommitFile(t, origin, "c.txt", "F\n", "c")
	gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
	gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s0)
	gitStateRun_(t, origin, "add", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "b0")
	gitStateRun_(t, origin, "checkout", "-q", "-b", "feature")
	f1 := gitStateCommitFile(t, origin, "a.txt", "1\n", "f1 feature work")
	gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s1)
	gitStateRun_(t, origin, "add", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "f2 bump sub only")
	gitStateRun_(t, origin, "checkout", "-q", "main")
	gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "update", "-q")
	gitStateRewriteTracked(t, filepath.Join(origin, "a.txt"), "1\n")
	gitStateRewriteTracked(t, filepath.Join(origin, "c.txt"), "M\n")
	gitStateRun_(t, origin, "-C", "sub", "checkout", "-q", s1)
	gitStateRun_(t, origin, "add", "a.txt", "c.txt", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "m1 squash of the feature, sub@s1, c.txt")

	ws := filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)
	gitStateRun_(t, ws, "checkout", "-q", "-b", "feature", "--track", "origin/feature")
	gitStateRun_(t, ws, "reset", "-q", "--hard", f1)
	gitStateRun_(t, ws, "-c", "protocol.file.allow=always", "submodule", "update", "-q")
	gitStateRewriteTracked(t, filepath.Join(ws, "c.txt"), "M\n")

	rep := rescueRealign(t, ws, RealignOptions{Rescue: true, NoPush: true})
	if rep.Status != GitRepoRealignable || rep.Class != GitClassBranchMismatch ||
		strings.Contains(rep.TieBreak, "HEAD stays") || !strings.Contains(rep.TieBreak, "is past what sub holds") {
		t.Fatalf("rescue = %+v, want a branch-mismatch move whose tie line names sub", rep)
	}
}

// The rescue push is a backup ref, not a publish: a pre-push hook (a lint,
// a test run, one that writes files) neither runs nor blocks it (#189
// round 22).
func TestPeerGitRescue_PushRunsNoHook(t *testing.T) {
	f, local, tip := divergedFixture(t)
	hook := filepath.Join(gitStateRun_(t, f.ws, "rev-parse", "--absolute-git-dir"), "hooks", "pre-push")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/hook-ran\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
	if rep.Status != GitRepoRealigned || !rep.RescuePushed || gitStateHead(t, f.ws) != tip {
		t.Fatalf("rescue = %+v, want pushed and realigned", rep)
	}
	if got := gitStateRun_(t, f.origin, "rev-parse", "refs/heads/rescue/260928-main"); got != local {
		t.Fatalf("pushed rescue branch = %s, want %s", got, local)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "hook-ran")); err == nil {
		t.Fatal("the pre-push hook ran")
	}
}

// No git command a peer git run starts runs a repo hook, in a preview or
// an apply: post-checkout (the .gitmodules restore), post-index-change
// (the temp-index refresh), reference-transaction (ref moves) and pre-push
// (the rescue push) all stay silent (#204).
func TestPeerGit_RunsNoRepoHooks(t *testing.T) {
	marks := t.TempDir()
	install := func(t *testing.T, repo string) {
		t.Helper()
		dir := filepath.Join(gitStateRun_(t, repo, "rev-parse", "--absolute-git-dir"), "hooks")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, hook := range []string{"post-checkout", "post-index-change", "reference-transaction", "pre-push"} {
			script := "#!/bin/sh\ntouch " + shellQuote(filepath.Join(marks, hook)) + "\n"
			if err := os.WriteFile(filepath.Join(dir, hook), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			// The same hook defined in config (git 2.54+), which the hooks
			// directory setting does not control.
			gitStateRun_(t, repo, "config", "--add", "hook.mark it=now.event", hook)
		}
		gitStateRun_(t, repo, "config", "hook.mark it=now.command", "touch "+shellQuote(filepath.Join(marks, "config-hook")))
		// And the fsmonitor hook.
		monitor := filepath.Join(dir, "fsmonitor")
		if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch "+shellQuote(filepath.Join(marks, "fsmonitor"))+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitStateRun_(t, repo, "config", "core.fsmonitor", monitor)
	}
	ran := func(t *testing.T) {
		t.Helper()
		if entries, _ := os.ReadDir(marks); len(entries) > 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("repo hooks ran: %v", names)
		}
	}

	// A stale .gitmodules restored and a realign (URL move fixture).
	ws, _, _, _, _, _ := urlMoveFixture(t)
	install(t, ws)
	if _, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{}); err != nil {
		t.Fatal(err)
	}
	ran(t)
	if _, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true}); err != nil {
		t.Fatal(err)
	}
	ran(t)

	// A pushed rescue.
	f, _, tip := divergedFixture(t)
	install(t, f.ws)
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoRealigned || !rep.RescuePushed || gitStateHead(t, f.ws) != tip {
		t.Fatalf("rescue = %+v", rep)
	}
	ran(t)
}

// git 2.54 reads hook.<event>.enabled only as a hook named after the
// event, so each configured hook is also turned off by its own name, a name
// with dots, spaces or "=" included (#204). Checked on the flags: git 2.55
// stops the hook by event either way.
func TestNoHooksNamesEachConfigHook(t *testing.T) {
	repo := t.TempDir()
	gitStateInitRepo(t, repo)
	for _, name := range []string{"mark", "team.Guard", "lint staged", "a=b"} {
		gitStateRun_(t, repo, "config", "hook."+name+".event", "post-checkout")
	}
	gitStateRun_(t, repo, "config", "hook.idle.command", "true") // no event: never runs
	r := &gitStateRun{git: "git"}
	flags := r.noHooks(context.Background(), repo)
	for _, name := range []string{"mark", "team.Guard", "lint staged", "a=b"} {
		if want := "--config-env=hook." + name + ".enabled=" + hookOffEnv; !slices.Contains(flags, want) {
			t.Errorf("noHooks = %q, missing %q", flags, want)
		}
	}
	if !slices.Contains(flags, "core.hooksPath=/dev/null") {
		t.Errorf("noHooks = %q, missing the hooks directory setting", flags)
	}
	if strings.Contains(strings.Join(flags, " "), "hook.idle.") {
		t.Errorf("noHooks = %q, names a hook with no event", flags)
	}
}

// A no-match suggestion names only a command the same run accepts (#209),
// in a preview, a --rescue preview and a refused --rescue --apply alike: a
// Git LFS repo's rescue is kept local, and a rescue the switch refuses
// names no command, only what to do first.
func TestPeerGitRescue_SuggestionNamesACommandTheRunAccepts(t *testing.T) {
	suggestions := func(t *testing.T, ws string) []string {
		t.Helper()
		var out []string
		for _, opts := range []RealignOptions{{}, {Rescue: true}, {Apply: true, Rescue: true}} {
			rep := rescueRealign(t, ws, opts)
			if rep.Status != GitRepoNoMatch {
				t.Fatalf("%+v: status %q, want no-match (%s)", opts, rep.Status, rep.Suggestion)
			}
			out = append(out, rep.Suggestion)
		}
		return out
	}
	lfsFixture := func(t *testing.T) *rescueFixture {
		f := newRescueFixture(t)
		gitStateCommitFile(t, f.writer, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n", "lfs")
		gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
		gitStateRun_(t, f.ws, "pull", "-q", "--ff-only")
		return f
	}
	keptLocal := func(t *testing.T, ws, tip string) {
		t.Helper()
		for _, s := range suggestions(t, ws) {
			if !strings.HasSuffix(s, "keeping it local: dot peer git realign --rescue --no-push --apply .") || !strings.Contains(s, "Git LFS") {
				t.Fatalf("suggestion %q, want the LFS reason and the --no-push rescue", s)
			}
		}
		if rep := rescueRealign(t, ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, ws) != tip {
			t.Fatalf("the suggested command did not realign: %+v", rep)
		}
	}
	t.Run("lfs diverged", func(t *testing.T) {
		f := lfsFixture(t)
		gitStateCommitFile(t, f.ws, "docs.md", "unpushed docs\n", "local docs")
		tip := f.publish(t, "b.txt", "b1\n")
		f.deliver(t, tip)
		keptLocal(t, f.ws, tip)
	})
	t.Run("lfs branch mismatch", func(t *testing.T) {
		f := lfsFixture(t)
		gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
		gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
		tip := f.publish(t, "a.txt", "a2\n")
		f.deliver(t, tip)
		keptLocal(t, f.ws, tip)
	})
	refused := func(t *testing.T, ws, why string) {
		t.Helper()
		for _, s := range suggestions(t, ws) {
			if strings.Contains(s, "dot peer git realign") || !strings.Contains(s, "not rescued: "+why) {
				t.Fatalf("suggestion %q, want %q and no command", s, why)
			}
		}
	}
	t.Run("default branch in linked worktrees", func(t *testing.T) {
		f := newRescueFixture(t)
		gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
		gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
		f.deliver(t, f.publish(t, "a.txt", "a2\n"))
		tmp, err := filepath.EvalSymlinks(t.TempDir()) // the paths git lists
		if err != nil {
			t.Fatal(err)
		}
		wt1, wt2, other := filepath.Join(tmp, "wt-main"), filepath.Join(tmp, "wt-main-2"), filepath.Join(tmp, "detached")
		gitStateRun_(t, f.ws, "worktree", "add", "-q", wt1, "main")
		gitStateRun_(t, f.ws, "worktree", "add", "-q", "-f", wt2, "main")
		// Another worktree whose directory is missing: the steps must not
		// drop it (git worktree prune would).
		gitStateRun_(t, f.ws, "worktree", "add", "-q", "--detach", other)
		if err := os.RemoveAll(other); err != nil {
			t.Fatal(err)
		}
		on := func(wt string) string { return "main is checked out in the linked worktree " + wt }
		refused(t, f.ws, "(1) "+on(wt1)+"; switch that worktree to another branch; (2) "+on(wt2)+"; switch that worktree to another branch")

		gitStateRun_(t, f.ws, "worktree", "lock", wt1)
		for _, wt := range []string{wt1, wt2} {
			if err := os.RemoveAll(wt); err != nil {
				t.Fatal(err)
			}
		}
		git := "git -C " + shellWord(f.ws)
		gone := ", which is missing; if it is gone for good, not just unmounted: "
		refused(t, f.ws, "(1) "+on(wt1)+gone+git+" worktree unlock "+shellWord(wt1)+"; "+git+" worktree remove "+shellWord(wt1)+"; (2) "+on(wt2)+gone+git+" worktree remove "+shellWord(wt2))

		gitStateRun_(t, f.ws, "worktree", "unlock", wt1)
		gitStateRun_(t, f.ws, "worktree", "remove", wt1)
		gitStateRun_(t, f.ws, "worktree", "remove", wt2)
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "dot peer git realign --rescue --apply .") {
			t.Fatalf("after the steps: suggestion %q, want the rescue", s)
		}
		if list := gitStateRun_(t, f.ws, "worktree", "list", "--porcelain"); !strings.Contains(list, "worktree "+other+"\n") {
			t.Fatalf("the steps dropped another missing worktree:\n%s", list)
		}
	})
	t.Run("every refusal at once", func(t *testing.T) {
		f := lfsFixture(t)
		gitStateCommitFile(t, f.ws, "mine.txt", "m\n", "unpushed on main")
		gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
		gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
		tip := f.publish(t, "a.txt", "a2\n")
		f.deliver(t, tip)
		wt := filepath.Join(t.TempDir(), "wt-main")
		gitStateRun_(t, f.ws, "worktree", "add", "-q", wt, "main")
		refused(t, f.ws, "(1) main is checked out in the linked worktree")
		for _, s := range suggestions(t, f.ws) {
			if !strings.Contains(s, "(2) local main has commits") || !strings.HasSuffix(s, "(3) the repo uses Git LFS, and a pushed rescue branch would lack its LFS objects; rescue with --no-push after that") {
				t.Fatalf("suggestion %q, want all three steps", s)
			}
		}
		// Following the steps in order lifts all three.
		gitStateRun_(t, f.ws, "worktree", "remove", wt)
		gitStateRun_(t, f.ws, "branch", "-m", "main", "main-kept")
		keptLocal(t, f.ws, tip)
	})
	// A worktree rebasing or bisecting the default branch holds it, as git
	// counts it, though the porcelain lists that worktree as detached
	// (#211). Ending it leaves the branch checked out there, so the step
	// also switches the worktree.
	stopRebase := func(t *testing.T, wt, run string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt, "rebase", "--exec", run}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=dot-test", "GIT_AUTHOR_EMAIL=dot-test@example.invalid",
			"GIT_COMMITTER_NAME=dot-test", "GIT_COMMITTER_EMAIL=dot-test@example.invalid")
		out, _ := cmd.CombinedOutput()
		if _, err := os.Stat(gitStateRun_(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge")); err != nil {
			t.Fatalf("the rebase did not stop in a rebase:\n%s", out)
		}
	}
	for _, tc := range []struct {
		name, op string
		add      []string // worktree add flags
		start    func(t *testing.T, wt string)
		end      []string
		repo     string // the repo's directory name, when not ws
	}{
		{"rebase", "rebase", []string{"main"}, func(t *testing.T, wt string) { stopRebase(t, wt, "false", "--root") }, []string{"rebase", "--abort"}, ""},
		{"rebase (--relative-paths)", "rebase", []string{"--relative-paths", "main"}, func(t *testing.T, wt string) { stopRebase(t, wt, "false", "--root") }, []string{"rebase", "--abort"}, ""},
		// A repo path that would be a glob pattern.
		{"rebase (repo path w[s]*)", "rebase", []string{"main"}, func(t *testing.T, wt string) { stopRebase(t, wt, "false", "--root") }, []string{"rebase", "--abort"}, "w[s]*"},
		// Another branch rebased with --update-refs over main's commit.
		{"rebase --update-refs", "rebase", []string{"-b", "topic", "main"}, func(t *testing.T, wt string) {
			gitStateRun_(t, wt, "commit", "-q", "--allow-empty", "-m", "t1")
			stopRebase(t, wt, "false", "--update-refs", "--root")
		}, []string{"rebase", "--abort"}, ""},
		{"bisect", "bisect", []string{"main"}, func(t *testing.T, wt string) {
			gitStateRun_(t, wt, "bisect", "start")
			gitStateRun_(t, wt, "checkout", "-q", "--detach")
		}, []string{"bisect", "reset"}, ""},
		// A bisect started on main still holds it from another branch.
		{"bisect left on another branch", "bisect", []string{"main"}, func(t *testing.T, wt string) {
			gitStateRun_(t, wt, "bisect", "start")
			gitStateRun_(t, wt, "switch", "-q", "-c", "other")
		}, []string{"bisect", "reset"}, ""},
	} {
		t.Run("default branch held by a "+tc.name+" in a linked worktree", func(t *testing.T) {
			f := newRescueFixture(t)
			if tc.repo != "" {
				f.ws = filepath.Join(t.TempDir(), tc.repo)
				gitStateRun_(t, filepath.Dir(f.ws), "clone", "-q", f.origin, f.ws)
			}
			gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
			gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
			tip := f.publish(t, "a.txt", "a2\n")
			f.deliver(t, tip)
			tmp, err := filepath.EvalSymlinks(t.TempDir()) // the path git lists
			if err != nil {
				t.Fatal(err)
			}
			wt := filepath.Join(tmp, "wt-main")
			gitStateRun_(t, f.ws, append([]string{"worktree", "add", "-q", wt}, tc.add...)...)
			before := gitStateRun_(t, f.ws, "rev-parse", "main")
			tc.start(t, wt)
			refused(t, f.ws, "main is held by a "+tc.op+" in the linked worktree "+wt+"; finish or abort it there, and if that leaves main checked out, switch that worktree to another branch")
			if got := gitStateRun_(t, f.ws, "rev-parse", "main"); got != before {
				t.Fatalf("main moved to %s under the %s", got, tc.name)
			}
			gitStateRun_(t, wt, tc.end...)
			gitStateRun_(t, wt, "checkout", "-q", "--detach")
			if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
				t.Fatalf("after the step: %+v, want realigned to %s", rep, tip)
			}
		})
	}
	// A gone worktree's rebase keeps its state (the commits so far on a
	// detached HEAD, an autostash) only in the admin dir a remove deletes,
	// so the step names that dir and what is there, and no command that
	// drops it; recovering from it first loses nothing.
	t.Run("a gone worktree mid-rebase", func(t *testing.T) {
		f := newRescueFixture(t)
		gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
		gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
		tip := f.publish(t, "a.txt", "a2\n")
		f.deliver(t, tip)
		tmp, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(tmp, "wt-main")
		gitStateRun_(t, f.ws, "worktree", "add", "-q", wt, "main")
		gitStateRewriteTracked(t, filepath.Join(wt, "a.txt"), "uncommitted\n")
		stopRebase(t, wt, "echo resolved > work.txt && git add work.txt && git commit -q -m 'conflict work' && false", "--autostash", "--root")
		wip := gitStateRun_(t, wt, "rev-parse", "HEAD")
		admin := gitStateRun_(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
		autostash, err := os.ReadFile(filepath.Join(admin, "rebase-merge", "autostash"))
		if err != nil {
			t.Fatalf("no autostash: %v", err)
		}
		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}
		refused(t, f.ws, "main is held by a rebase in the linked worktree "+wt+", which is missing; its admin dir "+admin+" holds that rebase's state (HEAD "+wip+", any autostash, rewritten refs and the reflog); if the worktree is gone for good, recover what you need from there first, then remove it with git worktree remove")
		for _, s := range suggestions(t, f.ws) {
			if strings.Contains(s, "git -C") {
				t.Fatalf("suggestion %q names a command", s)
			}
		}
		// Recovering first, as the step says, keeps both.
		gitStateRun_(t, f.ws, "branch", "main-wip", wip)
		gitStateRun_(t, f.ws, "stash", "store", "-m", "autostash", strings.TrimSpace(string(autostash)))
		gitStateRun_(t, f.ws, "worktree", "remove", wt)
		if got := gitStateRun_(t, f.ws, "log", "-1", "--format=%s", "main-wip"); got != "conflict work" {
			t.Fatalf("main-wip holds %q, want the rebase's commit", got)
		}
		if got := gitStateRun_(t, f.ws, "show", "stash@{0}:a.txt"); got != "uncommitted" {
			t.Fatalf("the stored autostash holds %q", got)
		}
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
			t.Fatalf("after the steps: %+v, want realigned to %s", rep, tip)
		}
	})
	// A branch named rescue blocks every rescue/<date>-<branch> name, so
	// no suffix helps; the step renames it (#212).
	t.Run("a branch named rescue", func(t *testing.T) {
		f, local, tip := divergedFixture(t)
		gitStateRun_(t, f.ws, "branch", "rescue", f.base)
		refused(t, f.ws, "a branch named rescue keeps rescue/260928-main from being created; rename it first: git -C "+shellWord(f.ws)+" branch -m rescue rescue-kept")
		if out := gitStateRun_(t, f.ws, "for-each-ref", "refs/heads/rescue/"); out != "" {
			t.Fatalf("a rescue branch was created: %s", out)
		}
		gitStateRun_(t, f.ws, "branch", "-m", "rescue", "rescue-kept")
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "dot peer git realign --rescue --apply .") {
			t.Fatalf("after the rename: suggestion %q, want the rescue", s)
		}
		// A rescue name already taken still gets a suffix.
		gitStateRun_(t, f.ws, "branch", "rescue/260928-main", f.base)
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || rep.Rescue != "rescue/260928-main-2" || gitStateHead(t, f.ws) != tip {
			t.Fatalf("rescue = %+v, want rescue/260928-main-2 and HEAD at %s", rep, tip)
		}
		if got := gitStateRun_(t, f.ws, "rev-parse", "rescue/260928-main-2"); got != local {
			t.Fatalf("rescue/260928-main-2 = %s, want %s", got, local)
		}
	})
	// git refuses to rename a branch a rebase holds, so the step for a
	// blocking rescue branch ends that first.
	t.Run("a branch named rescue held by a rebase", func(t *testing.T) {
		f, _, tip := divergedFixture(t)
		tmp, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(tmp, "wt-rescue")
		gitStateRun_(t, f.ws, "worktree", "add", "-q", "-b", "rescue", wt, f.base)
		stopRebase(t, wt, "false", "--root")
		refused(t, f.ws, "a branch named rescue keeps rescue/260928-main from being created; finish or abort the rebase holding it in the linked worktree "+wt+", then rename it: git -C "+shellWord(f.ws)+" branch -m rescue rescue-kept")
		gitStateRun_(t, wt, "rebase", "--abort")
		gitStateRun_(t, f.ws, "branch", "-m", "rescue", "rescue-kept")
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
			t.Fatalf("after the steps: %+v, want realigned to %s", rep, tip)
		}
	})
	// A branch named rescue on the push remote blocks every pushed rescue
	// name there; --no-push lifts it (#214).
	t.Run("a branch named rescue on the remote", func(t *testing.T) {
		f, local, tip := divergedFixture(t)
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue")
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		for _, s := range suggestions(t, f.ws) {
			if !strings.Contains(s, "the remote origin has a branch named rescue (as last fetched; if it was deleted there: git -C "+shellWord(f.ws)+" branch -d -r origin/rescue), which keeps rescue/260928-main from being pushed") || !strings.HasSuffix(s, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
				t.Fatalf("suggestion %q, want the remote's rescue branch and the --no-push rescue", s)
			}
		}
		rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
		if rep.Status != GitRepoRealigned || rep.RescuePushed || gitStateHead(t, f.ws) != tip || gitStateRun_(t, f.ws, "rev-parse", "rescue/260928-main") != local {
			t.Fatalf("the suggested command: %+v, want a local rescue and HEAD at %s", rep, tip)
		}
	})
	// Deleted on the remote but still in the tracking refs: the prune the
	// step names lifts it.
	t.Run("a stale remote rescue branch", func(t *testing.T) {
		f, _, _ := divergedFixture(t)
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue")
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		gitStateRun_(t, f.writer, "push", "-q", "origin", ":refs/heads/rescue")
		// Another stale tracking ref the step must not touch.
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/gone")
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		gitStateRun_(t, f.writer, "push", "-q", "origin", ":refs/heads/gone")
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.Contains(s, "if it was deleted there: git -C "+shellWord(f.ws)+" branch -d -r origin/rescue") {
			t.Fatalf("suggestion %q, want that one tracking ref removed", s)
		}
		gitStateRun_(t, f.ws, "branch", "-d", "-r", "origin/rescue")
		gitStateRun_(t, f.ws, "rev-parse", "--verify", "-q", "refs/remotes/origin/gone") // left alone
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "dot peer git realign --rescue --apply .") {
			t.Fatalf("after the prune: suggestion %q, want the pushed rescue", s)
		}
	})
	t.Run("local default branch the target lacks", func(t *testing.T) {
		f := newRescueFixture(t)
		gitStateCommitFile(t, f.ws, "mine.txt", "m\n", "unpushed on main")
		gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feature")
		gitStateCommitFile(t, f.ws, "feat.txt", "f1\n", "f1")
		f.deliver(t, f.publish(t, "a.txt", "a2\n"))
		refused(t, f.ws, "local main has commits")
		// Names an earlier move took, or a branch under them holds, are
		// skipped.
		gitStateRun_(t, f.ws, "branch", "main-kept/x", f.base)
		gitStateRun_(t, f.ws, "branch", "main-kept-2", f.base)
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "git -C "+shellWord(f.ws)+" branch -m main main-kept-3") {
			t.Fatalf("suggestion %q, want the branch move to a free name", s)
		}
		// Following the advice lifts the refusal, and the kept branch
		// holds the commits.
		mine := gitStateRun_(t, f.ws, "rev-parse", "main")
		gitStateRun_(t, f.ws, "branch", "-m", "main", "main-kept-3")
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "dot peer git realign --rescue --apply .") {
			t.Fatalf("after the advice: suggestion %q, want the rescue", s)
		}
		if got := gitStateRun_(t, f.ws, "rev-parse", "main-kept-3"); got != mine {
			t.Fatalf("main-kept-3 = %s, want %s", got, mine)
		}
	})
}

// A pushed rescue skips a name the push remote holds, as its
// remote-tracking refs show; when the remote holds it unseen, the failed
// push says to fetch and run again (#214).
func TestPeerGitRescue_AvoidsNamesTheRemoteHolds(t *testing.T) {
	t.Run("fetched", func(t *testing.T) {
		f, local, tip := divergedFixture(t)
		// The other Mac rescued the same branch today.
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue/260928-main")
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		if got := rescueRealign(t, f.ws, RealignOptions{Rescue: true}).Rescue; got != "rescue/260928-main-2" {
			t.Fatalf("pushed rescue name %q, want rescue/260928-main-2", got)
		}
		if got := rescueRealign(t, f.ws, RealignOptions{Rescue: true, NoPush: true}).Rescue; got != "rescue/260928-main" {
			t.Fatalf("local-only rescue name %q, want rescue/260928-main", got)
		}
		rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
		if rep.Status != GitRepoRealigned || !rep.RescuePushed || gitStateHead(t, f.ws) != tip {
			t.Fatalf("rescue = %+v, want pushed and realigned", rep)
		}
		if got := gitStateRun_(t, f.origin, "rev-parse", "refs/heads/rescue/260928-main-2"); got != local {
			t.Fatalf("origin rescue/260928-main-2 = %s, want %s", got, local)
		}
		if got := gitStateRun_(t, f.origin, "rev-parse", "refs/heads/rescue/260928-main"); got == local {
			t.Fatal("the other Mac's rescue branch was overwritten")
		}
	})
	t.Run("not fetched", func(t *testing.T) {
		f, local, tip := divergedFixture(t)
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue/260928-main")
		rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
		if rep.Status != GitRepoUnresolvable || !strings.Contains(rep.Reason, "if origin holds this name or a rescue branch: git -C "+shellWord(f.ws)+" fetch origin, then run again, which picks a free name or names --no-push; otherwise fix what the error below says; or use --no-push") {
			t.Fatalf("rescue = %+v, want the fetch-and-run-again hint", rep)
		}
		if gitStateHead(t, f.ws) != local || gitStateRun_(t, f.ws, "rev-parse", "rescue/260928-main") != local {
			t.Fatal("HEAD moved, or the local rescue branch was not kept")
		}
		// Following the hint pushes a free name.
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoRealigned || !rep.RescuePushed || gitStateHead(t, f.ws) != tip {
			t.Fatalf("after the fetch: %+v, want pushed and realigned", rep)
		}
	})
	// A remote rescue branch unseen: the fetch the hint names first turns
	// the next run into the --no-push refusal, not another failed push.
	t.Run("a remote rescue branch, not fetched", func(t *testing.T) {
		f, local, tip := divergedFixture(t)
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue")
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoUnresolvable || !strings.Contains(rep.Reason, "fetch origin, then run again") || gitStateHead(t, f.ws) != local {
			t.Fatalf("rescue = %+v, want the push refused with the fetch first", rep)
		}
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		if s := rescueRealign(t, f.ws, RealignOptions{}).Suggestion; !strings.HasSuffix(s, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
			t.Fatalf("after the fetch: suggestion %q, want the --no-push rescue", s)
		}
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
			t.Fatalf("the --no-push rescue: %+v, want realigned to %s", rep, tip)
		}
	})
	// A remote branch under the name blocks it too (directory/file).
	t.Run("a remote branch under the name", func(t *testing.T) {
		f, local, _ := divergedFixture(t)
		gitStateRun_(t, f.writer, "push", "-q", "origin", "HEAD:refs/heads/rescue/260928-main/x")
		gitStateRun_(t, f.ws, "fetch", "-q", "origin")
		rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
		if rep.Status != GitRepoRealigned || rep.Rescue != "rescue/260928-main-2" || gitStateRun_(t, f.origin, "rev-parse", "refs/heads/rescue/260928-main-2") != local {
			t.Fatalf("rescue = %+v, want rescue/260928-main-2 pushed", rep)
		}
	})
}

// commitAt commits content with a fixed author date, as a history rewrite
// keeps it, and returns the new commit.
func commitAt(t *testing.T, dir, name, content, subject, date string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitStateRun_(t, dir, "add", name)
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", subject)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dot-test", "GIT_AUTHOR_EMAIL=dot-test@example.invalid", "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME=dot-test", "GIT_COMMITTER_EMAIL=dot-test@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	return gitStateRun_(t, dir, "rev-parse", "HEAD")
}

// rewrittenFixture: three commits reach both Macs, then the writer rewrites
// them (one string replaced, authors, dates and subjects kept, as git
// filter-repo does) and force-pushes; the workspace keeps the old history,
// with the rewritten files delivered (#216).
func rewrittenFixture(t *testing.T) (f *rescueFixture, oldTip, newTip string) {
	f = newRescueFixture(t)
	dates := []string{"1700000000 +0900", "1700000100 +0900", "1700000200 +0900"}
	for i, date := range dates {
		commitAt(t, f.writer, "notes.txt", strings.Repeat("secret line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, f.ws, "pull", "-q", "--ff-only")
	oldTip = gitStateHead(t, f.ws)
	gitStateRun_(t, f.writer, "reset", "-q", "--hard", f.base)
	for i, date := range dates {
		newTip = commitAt(t, f.writer, "notes.txt", strings.Repeat("redacted line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "-f", "origin", "main")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	f.deliver(t, newTip)
	return f, oldTip, newTip
}

// A rewritten upstream is not local-only work: its class says so, and a
// rescue of it is never pushed, since that would publish what the rewrite
// took out (#216).
func TestPeerGitRescue_RewrittenUpstreamRescuesOnlyLocally(t *testing.T) {
	f, oldTip, newTip := rewrittenFixture(t)
	for _, opts := range []RealignOptions{{}, {Rescue: true}, {Apply: true, Rescue: true}} {
		rep := rescueRealign(t, f.ws, opts)
		if rep.Status != GitRepoNoMatch || rep.Class != GitClassRewrittenUpstream {
			t.Fatalf("%+v: status %q class %q, want no-match rewritten-upstream (%s)", opts, rep.Status, rep.Class, rep.Suggestion)
		}
		for _, want := range []string{"origin/main was rewritten: the 3 local-only commit(s) are pre-rewrite versions", "fetch --tags --force", "a pushed rescue branch would publish the history the rewrite took out"} {
			if !strings.Contains(rep.Suggestion, want) {
				t.Fatalf("%+v: suggestion %q lacks %q", opts, rep.Suggestion, want)
			}
		}
		if !strings.HasSuffix(rep.Suggestion, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
			t.Fatalf("%+v: suggestion %q, want the --no-push rescue", opts, rep.Suggestion)
		}
	}
	if out := gitStateRun_(t, f.origin, "for-each-ref", "refs/heads/rescue/"); out != "" {
		t.Fatalf("a rescue branch reached the remote: %s", out)
	}
	rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true})
	if rep.Status != GitRepoRealigned || rep.RescuePushed || gitStateHead(t, f.ws) != newTip || gitStateRun_(t, f.ws, "rev-parse", "rescue/260928-main") != oldTip {
		t.Fatalf("--no-push rescue = %+v, want HEAD at %s and the old history kept locally", rep, newTip)
	}
}

// One real local commit on top of the rewritten ones keeps the repo
// diverged (AC3 is about repos with no twins), but its rescue is still
// never pushed: the push would send the pre-rewrite commits (#216).
func TestPeerGitClass_RewriteWithRealLocalWorkRescuesOnlyLocally(t *testing.T) {
	f, oldTip, newTip := rewrittenFixture(t)
	commitAt(t, f.ws, "mine.txt", "mine\n", "my own work", "1700000300 +0900")
	f.deliver(t, newTip) // the other Mac's files, as peer sync leaves them
	rep := rescueRealign(t, f.ws, RealignOptions{})
	if rep.Class != GitClassDiverged || !strings.Contains(rep.Suggestion, "3 of the 4 local-only commit(s) are pre-rewrite versions of commits on the remote") || !strings.HasSuffix(rep.Suggestion, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
		t.Fatalf("class %q suggestion %q, want diverged with the --no-push rescue and why", rep.Class, rep.Suggestion)
	}
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoNoMatch {
		t.Fatalf("pushed rescue: %+v, want refused", rep)
	}
	if out := gitStateRun_(t, f.origin, "for-each-ref", "refs/heads/rescue/"); out != "" {
		t.Fatalf("a rescue branch reached the remote: %s", out)
	}
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateRun_(t, f.ws, "merge-base", "--is-ancestor", oldTip, "rescue/260928-main") != "" {
		t.Fatalf("--no-push rescue: %+v", rep)
	}
}

// A branch cut from the old default branch would push the pre-rewrite
// commits too, so its branch-mismatch rescue stays local (#216).
func TestPeerGitRescue_BranchFromTheOldHistoryRescuesOnlyLocally(t *testing.T) {
	f, _, newTip := rewrittenFixture(t)
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feat")
	commitAt(t, f.ws, "feat.txt", "f\n", "feature work", "1700000400 +0900")
	f.deliver(t, newTip)
	for _, opts := range []RealignOptions{{}, {Rescue: true}, {Apply: true, Rescue: true}} {
		rep := rescueRealign(t, f.ws, opts)
		if rep.Class != GitClassBranchMismatch || !strings.Contains(rep.Suggestion, "pre-rewrite versions of commits on the remote") || strings.Contains(rep.Suggestion, "realign --rescue --apply") {
			t.Fatalf("%+v: class %q suggestion %q, want branch-mismatch refusing the push", opts, rep.Class, rep.Suggestion)
		}
	}
	gitStateRun_(t, f.ws, "branch", "-m", "main", "main-kept")
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != newTip {
		t.Fatalf("--no-push rescue: %+v, want HEAD at %s", rep, newTip)
	}
	if out := gitStateRun_(t, f.origin, "for-each-ref", "refs/heads/rescue/"); out != "" {
		t.Fatalf("a rescue branch reached the remote: %s", out)
	}
}

// Tracking refs can hide what a push sends: a branch the remote deleted
// still at the old tip (fetch without --prune), or a push-only remote with
// none. The push stays refused, by the class and by the branch the rescue
// target is on (#216).
func TestPeerGitRescue_RewriteStaysLocalWhateverTheTrackingRefs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		realWork bool
		setup    func(t *testing.T, f *rescueFixture, oldTip string)
	}{
		{"a stale tracking ref at the old tip", false, func(t *testing.T, f *rescueFixture, oldTip string) {
			gitStateRun_(t, f.ws, "update-ref", "refs/remotes/origin/keep", oldTip)
		}},
		{"a push-only remote", false, func(t *testing.T, f *rescueFixture, oldTip string) {
			public := filepath.Join(filepath.Dir(f.origin), "public.git") // checked below
			gitStateRun_(t, filepath.Dir(public), "init", "-q", "--bare", public)
			gitStateRun_(t, f.ws, "remote", "add", "public", public)
			gitStateRun_(t, f.ws, "config", "remote.pushDefault", "public")
		}},
		{"real work and a stale tracking ref", true, func(t *testing.T, f *rescueFixture, oldTip string) {
			gitStateRun_(t, f.ws, "update-ref", "refs/remotes/origin/keep", oldTip)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, oldTip, newTip := rewrittenFixture(t)
			if tc.realWork {
				commitAt(t, f.ws, "mine.txt", "mine\n", "my own work", "1700000300 +0900")
				f.deliver(t, newTip)
			}
			tc.setup(t, f, oldTip)
			for _, opts := range []RealignOptions{{}, {Rescue: true}, {Apply: true, Rescue: true}} {
				rep := rescueRealign(t, f.ws, opts)
				if rep.Status != GitRepoNoMatch || rep.RescuePushed || !strings.HasSuffix(rep.Suggestion, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
					t.Fatalf("%+v: %+v, want the push refused and the --no-push rescue named", opts, rep)
				}
			}
			for _, remote := range []string{f.origin, filepath.Join(filepath.Dir(f.origin), "public.git")} {
				if _, err := os.Stat(remote); err == nil {
					if out := gitStateRun_(t, remote, "for-each-ref", "refs/heads/rescue/"); out != "" {
						t.Fatalf("a rescue branch reached %s: %s", remote, out)
					}
				}
			}
		})
	}
}

// A feature branch whose own upstream was rewritten, rescued as a branch
// mismatch toward a push-only remote: the twins are on origin/feat, not on
// the default branch or the push remote, and the push is still refused
// (#216).
func TestPeerGitRescue_RewrittenFeatureUpstreamStaysLocal(t *testing.T) {
	f := newRescueFixture(t)
	gitStateRun_(t, f.writer, "checkout", "-q", "-b", "feat")
	dates := []string{"1700000000 +0900", "1700000100 +0900"}
	for i, date := range dates {
		commitAt(t, f.writer, "notes.txt", strings.Repeat("secret line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "-u", "origin", "feat")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	gitStateRun_(t, f.ws, "checkout", "-q", "-b", "feat", "--track", "origin/feat")
	gitStateRun_(t, f.writer, "reset", "-q", "--hard", f.base)
	for i, date := range dates {
		commitAt(t, f.writer, "notes.txt", strings.Repeat("redacted line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "-f", "origin", "feat")
	gitStateRun_(t, f.writer, "checkout", "-q", "main")
	tip := f.publish(t, "a.txt", "a2\n")
	public := filepath.Join(filepath.Dir(f.origin), "public.git")
	gitStateRun_(t, filepath.Dir(public), "init", "-q", "--bare", public)
	gitStateRun_(t, f.ws, "remote", "add", "public", public)
	gitStateRun_(t, f.ws, "config", "remote.pushDefault", "public")
	f.deliver(t, tip)
	for _, opts := range []RealignOptions{{}, {Rescue: true}, {Apply: true, Rescue: true}} {
		rep := rescueRealign(t, f.ws, opts)
		if rep.Class != GitClassBranchMismatch || rep.RescuePushed || !strings.Contains(rep.Suggestion, "pre-rewrite versions of commits on the remote") || !strings.HasSuffix(rep.Suggestion, "keeping it local: dot peer git realign --rescue --no-push --apply .") {
			t.Fatalf("%+v: %+v, want the push refused", opts, rep)
		}
	}
	if out := gitStateRun_(t, public, "for-each-ref", "refs/heads/"); out != "" {
		t.Fatalf("something reached the push-only remote: %s", out)
	}
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
		t.Fatalf("--no-push rescue: %+v, want HEAD at %s", rep, tip)
	}
}

// log.showSignature adds lines to a signed commit's log entry; the twin
// check reads without them, so signed pre-rewrite commits still pair with
// their unsigned rewrites (#216).
func TestPeerGitRescue_RewriteOfSignedCommits(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	key := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	f := newRescueFixture(t)
	gitStateRun_(t, f.writer, "config", "gpg.format", "ssh")
	gitStateRun_(t, f.writer, "config", "user.signingkey", key)
	gitStateRun_(t, f.writer, "config", "commit.gpgsign", "true")
	dates := []string{"1700000000 +0900", "1700000100 +0900"}
	for i, date := range dates {
		commitAt(t, f.writer, "notes.txt", strings.Repeat("secret line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, f.ws, "pull", "-q", "--ff-only")
	gitStateRun_(t, f.writer, "config", "commit.gpgsign", "false")
	gitStateRun_(t, f.writer, "reset", "-q", "--hard", f.base)
	var newTip string
	for i, date := range dates {
		newTip = commitAt(t, f.writer, "notes.txt", strings.Repeat("redacted line\n", i+1), "notes "+strconv.Itoa(i), date)
	}
	gitStateRun_(t, f.writer, "push", "-q", "-f", "origin", "main")
	gitStateRun_(t, f.ws, "fetch", "-q", "origin")
	f.deliver(t, newTip)
	gitStateRun_(t, f.ws, "config", "log.showSignature", "true")
	if rep := rescueRealign(t, f.ws, RealignOptions{}); rep.Class != GitClassRewrittenUpstream {
		t.Fatalf("class %q suggestion %q, want rewritten-upstream", rep.Class, rep.Suggestion)
	}
}

// --candidate-refs widens the candidates to refs such as the unpushed
// branches of a Mac that stopped; a target taken from one names it, and a
// commit that is not a strict descendant of HEAD is never taken (#217).
func TestPeerGitRealign_CandidateRefs(t *testing.T) {
	stopped := func(t *testing.T, f *rescueFixture) string {
		t.Helper()
		other := filepath.Join(t.TempDir(), "m3")
		gitStateRun_(t, filepath.Dir(other), "clone", "-q", f.origin, other)
		sha := gitStateCommitFile(t, other, "a.txt", "never pushed\n", "unpushed on m3")
		gitStateRun_(t, f.ws, "fetch", "-q", other, "+refs/heads/*:refs/peer/m3/heads/*")
		return sha
	}
	t.Run("taken only with the flag", func(t *testing.T) {
		f := newRescueFixture(t)
		sha := stopped(t, f)
		f.deliver(t, sha)
		if rep := rescueRealign(t, f.ws, RealignOptions{}); rep.Status != GitRepoNoMatch {
			t.Fatalf("without the flag: %+v, want no-match", rep)
		}
		rep := rescueRealign(t, f.ws, RealignOptions{CandidateRefs: []string{"refs/peer/m3/"}})
		if rep.Status != GitRepoRealignable || rep.Target != sha || rep.TargetRef != "refs/peer/m3/heads/main" {
			t.Fatalf("with the flag: %+v, want realignable onto %s from refs/peer/m3/heads/main", rep, sha)
		}
		if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, CandidateRefs: []string{"refs/peer/m3/"}}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != sha {
			t.Fatalf("apply: %+v", rep)
		}
	})
	t.Run("a non-descendant is never taken", func(t *testing.T) {
		f := newRescueFixture(t)
		gitStateCommitFile(t, f.ws, "local.txt", "mine\n", "local only") // HEAD moves past the fork point
		sha := stopped(t, f)
		f.deliver(t, sha)
		if rep := rescueRealign(t, f.ws, RealignOptions{CandidateRefs: []string{"refs/peer/m3/"}}); rep.Status == GitRepoRealignable || rep.Target == sha {
			t.Fatalf("%+v: took a commit that is not a descendant of HEAD", rep)
		}
	})
	t.Run("a commit the upstream offers too is not labeled", func(t *testing.T) {
		f := newRescueFixture(t)
		tip := f.publish(t, "a.txt", "a2\n")
		gitStateRun_(t, f.ws, "fetch", "-q", f.origin, "+refs/heads/*:refs/peer/m3/heads/*")
		f.deliver(t, tip)
		if rep := rescueRealign(t, f.ws, RealignOptions{CandidateRefs: []string{"refs/peer/m3/"}}); rep.Status != GitRepoRealignable || rep.Target != tip || rep.TargetRef != "" {
			t.Fatalf("%+v, want the upstream candidate, unlabeled", rep)
		}
	})
}

// A pushed rescue branch in a Git LFS repo would point at objects the
// remote lacks, so the rescue is refused unless it stays local (#204).
func TestPeerGitRescue_LFSRepoRescuesOnlyLocally(t *testing.T) {
	f := newRescueFixture(t)
	gitStateCommitFile(t, f.writer, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n", "lfs")
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, f.ws, "pull", "-q", "--ff-only")
	local := gitStateCommitFile(t, f.ws, "docs.md", "unpushed docs\n", "local docs")
	f.publish(t, "b.txt", "b1\n")
	tip := f.publish(t, "b.txt", "b2\n")
	f.deliver(t, tip)

	rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true})
	if rep.Status != GitRepoNoMatch || !strings.Contains(rep.Suggestion, "Git LFS") || gitStateHead(t, f.ws) != local {
		t.Fatalf("pushed rescue in an LFS repo = %+v, want refused with HEAD kept", rep)
	}
	if out := gitStateRun_(t, f.ws, "branch", "--list", "rescue/*"); out != "" {
		t.Fatalf("a rescue branch was created: %s", out)
	}
	// The check keeps its glob pathspec whatever the caller's environment
	// says, and a check that fails refuses too.
	t.Setenv("GIT_LITERAL_PATHSPECS", "1")
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); !strings.Contains(rep.Suggestion, "Git LFS") {
		t.Fatalf("with literal pathspecs = %+v, want refused", rep)
	}
	gitStateRun_(t, f.ws, "config", "grep.threads", "not-a-number")
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); !strings.Contains(rep.Suggestion, "cannot tell whether the repo uses Git LFS") {
		t.Fatalf("with a failing check = %+v, want refused", rep)
	}
	gitStateRun_(t, f.ws, "config", "--unset", "grep.threads")
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true, NoPush: true}); rep.Status != GitRepoRealigned || gitStateHead(t, f.ws) != tip {
		t.Fatalf("local rescue in an LFS repo = %+v, want realigned", rep)
	}
}

// "#" starts a comment only as a line's first non-blank character, so a
// pattern holding "#" still sets the attribute (#204).
func TestLFSRefusalReadsCommentsAsGitDoes(t *testing.T) {
	for attrs, lfs := range map[string]bool{
		"assets/#raw/*.psd filter=lfs\n": true,
		"  # *.bin filter=lfs\n":         false,
	} {
		repo := t.TempDir()
		gitStateInitRepo(t, repo)
		gitStateCommitFile(t, repo, ".gitattributes", attrs, "attributes")
		r := &gitStateRun{git: "git"}
		if got := r.lfsRefusal(context.Background(), repo) != ""; got != lfs {
			t.Errorf("%q: refused = %v, want %v", attrs, got, lfs)
		}
	}
}

// A .gitattributes that only mentions filter=lfs in a comment, or sets
// another filter, is not an LFS repo: its rescue pushes (#204).
func TestPeerGitRescue_LFSCheckReadsAttributeLines(t *testing.T) {
	f := newRescueFixture(t)
	gitStateCommitFile(t, f.writer, ".gitattributes", "# was: *.bin filter=lfs\n*.dat filter=lfs2\n", "attributes")
	gitStateRun_(t, f.writer, "push", "-q", "origin", "main")
	gitStateRun_(t, f.ws, "pull", "-q", "--ff-only")
	gitStateCommitFile(t, f.ws, "docs.md", "unpushed docs\n", "local docs")
	f.publish(t, "b.txt", "b1\n")
	tip := f.publish(t, "b.txt", "b2\n")
	f.deliver(t, tip)
	if rep := rescueRealign(t, f.ws, RealignOptions{Apply: true, Rescue: true}); rep.Status != GitRepoRealigned || !rep.RescuePushed || gitStateHead(t, f.ws) != tip {
		t.Fatalf("rescue = %+v, want pushed and realigned", rep)
	}
}
