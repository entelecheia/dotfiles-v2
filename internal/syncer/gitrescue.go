package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// rescueChainLimit caps how many commits bestOnChain reads, newest first.
//
// ponytail: fixed cap. A branch whose files match a default-branch commit
// older than the newest 50 stays unclassified; raise it or bisect by
// content if that case appears.
const rescueChainLimit = 50

// classifyNoMatch refines a no-match repo into one of the #178 classes with a
// one-line next step. It only reads: refs, counts and content comparisons.
// Diverged and branch-mismatch repos get a RescueTarget when a commit on the
// upstream or default branch matches the files better than HEAD.
func (r *gitStateRun) classifyNoMatch(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	rep.branch, _ = r.read(ctx, abs, "symbolic-ref", "-q", "--short", "HEAD")
	label := rep.branch
	if label == "" {
		label = "a detached HEAD"
	}

	// A checkout on another branch (the dev/maru case: a squash-merged
	// feature branch) whose files match the default branch.
	if def, ref := r.defaultBranch(ctx, abs); ref != "" && rep.branch != def {
		if cand, diffs, ok := r.bestOnChain(ctx, abs, gitdir, rep.Head, ref, rep.HeadDiffs); ok {
			rep.Class = GitClassBranchMismatch
			rep.RescueTarget, rep.rescueBranch, rep.remote = cand, def, "origin"
			rep.rescueDiffs = diffs
			rep.Suggestion = fmt.Sprintf("on %s, but %s %s at %s; keep HEAD on a rescue branch and switch: %s",
				label, matchWords(diffs), def, shortRev(cand), rescueCommand(rep.Path))
			return
		}
	}

	upstream, err := r.read(ctx, abs, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err != nil || upstream == "" {
		return
	}
	upName := strings.TrimPrefix(upstream, "refs/remotes/")
	counts, err := r.read(ctx, abs, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return
	}
	ahead, errA := strconv.Atoi(fields[0])
	behind, errB := strconv.Atoi(fields[1])
	if errA != nil || errB != nil {
		return
	}
	switch {
	case ahead == 0 && behind == 0:
		rep.Class = GitClassAtTip
		rep.Suggestion = "at " + upName + "; only uncommitted changes differ, nothing to realign"
	case behind == 0:
		rep.Class = GitClassAheadUnpushed
		rep.Suggestion = fmt.Sprintf("%d local-only commit(s) on %s; push them: git -C %s push", ahead, label, shellWord(abs))
	case ahead > 0:
		rep.Class = GitClassDiverged
		rep.remote = "origin"
		if rep.branch != "" {
			if remote, err := r.read(ctx, abs, "config", "branch."+rep.branch+".remote"); err == nil && remote != "" {
				rep.remote = remote
			}
		}
		if cand, diffs, ok := r.bestOnChain(ctx, abs, gitdir, rep.Head, upstream, rep.HeadDiffs); ok {
			rep.RescueTarget = cand
			rep.rescueDiffs = diffs
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) vs %d on %s, and %s %s; keep the local commits on a rescue branch and realign: %s",
				ahead, behind, upName, matchWords(diffs), shortRev(cand), rescueCommand(rep.Path))
		} else {
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) vs %d on %s, and no upstream commit matches the files better than HEAD; rebase or merge by hand",
				ahead, behind, upName)
		}
	}
}

func rescueCommand(path string) string {
	return "dot peer git realign --rescue --apply " + shellWord(path)
}

// shellWord quotes s for a suggested command line only when it needs it.
func shellWord(s string) string {
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@+=,", r)
	}
	if s == "" || strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) >= 0 {
		return shellQuote(s)
	}
	return s
}

// matchWords says how well the rescue target matches the files.
func matchWords(diffs int) string {
	if diffs == 0 {
		return "the files match"
	}
	return fmt.Sprintf("the files are closest to (%d differing)", diffs)
}

func shortRev(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// defaultBranch names origin's default branch and its remote-tracking ref:
// origin/HEAD when set, else main or master when present.
func (r *gitStateRun) defaultBranch(ctx context.Context, abs string) (string, string) {
	if ref, err := r.read(ctx, abs, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(ref, "refs/remotes/origin/") {
		return strings.TrimPrefix(ref, "refs/remotes/origin/"), ref
	}
	for _, name := range []string{"main", "master"} {
		ref := "refs/remotes/origin/" + name
		if _, err := r.read(ctx, abs, "rev-parse", "--verify", "-q", ref); err == nil {
			return name, ref
		}
	}
	return "", ""
}

// bestOnChain finds the commit on tip's first-parent chain, not reachable
// from head, whose content is closest to the worktree, newest first; ties
// keep the newer commit. It succeeds only when that commit beats HEAD.
func (r *gitStateRun) bestOnChain(ctx context.Context, abs, gitdir, head, tip string, headDiffs int) (string, int, bool) {
	out, err := r.read(ctx, abs, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(rescueChainLimit), tip, "--not", head)
	if err != nil || out == "" {
		return "", 0, false
	}
	best, bestDiffs := "", -1
	for _, sha := range strings.Split(out, "\n") {
		diffs, err := r.contentDiffs(ctx, abs, gitdir, strings.TrimSpace(sha))
		if err != nil {
			continue
		}
		if bestDiffs < 0 || diffs < bestDiffs {
			best, bestDiffs = strings.TrimSpace(sha), diffs
		}
		if diffs == 0 {
			break
		}
	}
	if best == "" || bestDiffs >= headDiffs {
		return "", 0, false
	}
	return best, bestDiffs, true
}

// planRescue turns a classified no-match into a realignable move under
// --rescue: the target is the matching commit and HEAD's commits get a
// rescue branch named after today and the branch.
func (r *gitStateRun) planRescue(ctx context.Context, abs string, rep *GitRepoReport) {
	name := rep.branch
	if name == "" {
		name = "detached"
	}
	base := "rescue/" + r.opts.Now().Format("060102") + "-" + strings.ReplaceAll(name, "/", "-")
	rep.Rescue = base
	for i := 2; ; i++ {
		if _, err := r.read(ctx, abs, "rev-parse", "--verify", "-q", "refs/heads/"+rep.Rescue); err != nil {
			break
		}
		rep.Rescue = base + "-" + strconv.Itoa(i)
	}
	rep.Status = GitRepoRealignable
	rep.Reason = ""
	rep.Target = rep.RescueTarget
	rep.TargetDiffs = rep.rescueDiffs
	if !r.opts.NoPush {
		rep.RescueRemote = rep.remote
	}
}

// rescue keeps HEAD on the rescue branch, pushes it unless NoPush, then moves
// HEAD and the index to the target: the same branch for a diverged repo, the
// default branch for a branch mismatch. The worktree is never written, and a
// failed push leaves HEAD where it was.
func (r *gitStateRun) rescue(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	ref := "refs/heads/" + rep.Rescue
	if _, err := r.runOutput(ctx, abs, nil, false, "update-ref", "-m", "dot peer rescue", ref, rep.Head, ""); err != nil {
		rep.Status = GitRepoSkipped
		rep.Reason = "cannot create rescue branch " + rep.Rescue + ": " + shortErr(err)
		return
	}
	if !r.opts.NoPush {
		if _, err := r.runOutput(ctx, abs, nil, false, "push", "-q", rep.RescueRemote, ref+":"+ref); err != nil {
			rep.Status = GitRepoUnresolvable
			rep.Reason = "rescue branch " + rep.Rescue + " kept locally but the push to " + rep.remote + " failed; HEAD not moved (retry, or use --no-push): " + shortErr(err)
			return
		}
		rep.RescuePushed = true
	}
	if rep.rescueBranch == "" {
		r.realign(ctx, abs, gitdir, rep)
		return
	}
	r.switchBranch(ctx, abs, gitdir, rep)
}

// switchBranch points HEAD at the default branch, created at or
// fast-forwarded to the target, with the index built from the target, under
// the same index.lock protocol realign uses. A local default branch with
// commits the target lacks is refused.
func (r *gitStateRun) switchBranch(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	def, target := rep.rescueBranch, rep.Target
	defRef := "refs/heads/" + def
	oldDef, _ := r.read(ctx, abs, "rev-parse", "--verify", "-q", defRef)
	if oldDef != "" && oldDef != target && !r.strictDescendant(ctx, abs, oldDef, target) {
		rep.Status = GitRepoSkipped
		rep.Reason = "local " + def + " has commits " + shortRev(target) + " lacks; HEAD not moved (rescue branch " + rep.Rescue + " kept)"
		return
	}
	tempIndex, cleanup, err := r.tempIndexFor(ctx, abs, gitdir, target)
	if err != nil {
		rep.Status = GitRepoUnresolvable
		rep.Reason = "cannot build target index: " + shortErr(err)
		return
	}
	defer cleanup()
	index := filepath.Join(gitdir, "index")
	lockPath := index + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		rep.Status = GitRepoSkipped
		rep.Reason = "index.lock appeared before realign: " + shortErr(err)
		return
	}
	built, err := os.ReadFile(tempIndex)
	if err == nil {
		_, err = lock.Write(built)
	}
	if closeErr := lock.Close(); err == nil {
		err = closeErr
	}
	fail := func(status GitRepoStatus, reason string) {
		_ = os.Remove(lockPath)
		rep.Status, rep.Reason = status, reason
	}
	if err != nil {
		fail(GitRepoUnresolvable, "cannot write index lock: "+shortErr(err))
		return
	}
	// With the lock held, HEAD must still be where the plan read it: a
	// commit made during the push would otherwise be left off both the
	// rescue branch and the undo command.
	wantRef := ""
	if rep.branch != "" {
		wantRef = "refs/heads/" + rep.branch
	}
	curRef, _ := r.read(ctx, abs, "symbolic-ref", "-q", "HEAD")
	curSHA, _ := r.read(ctx, abs, "rev-parse", "HEAD")
	if curRef != wantRef || curSHA != rep.Head {
		fail(GitRepoSkipped, "HEAD moved during the rescue; repo untouched (rescue branch "+rep.Rescue+" kept)")
		return
	}
	restoreDef := func() {
		if oldDef == target {
			return
		}
		if oldDef == "" {
			_, _ = r.runOutput(ctx, abs, nil, false, "update-ref", "-d", defRef, target)
		} else {
			_, _ = r.runOutput(ctx, abs, nil, false, "update-ref", defRef, oldDef, target)
		}
	}
	if oldDef != target {
		if _, err := r.runOutput(ctx, abs, nil, false, "update-ref", "-m", "dot peer rescue", defRef, target, oldDef); err != nil {
			fail(GitRepoSkipped, "compare-and-swap on "+def+" failed; repo untouched: "+shortErr(err))
			return
		}
	}
	if _, err := r.runOutput(ctx, abs, nil, false, "symbolic-ref", "-m", "dot peer rescue", "HEAD", defRef); err != nil {
		restoreDef()
		fail(GitRepoUnresolvable, "cannot point HEAD at "+def+"; "+def+" restored: "+shortErr(err))
		return
	}
	restoreHead := "git -C " + shellWord(abs) + " update-ref --no-deref HEAD " + rep.Head
	if rep.branch != "" {
		restoreHead = "git -C " + shellWord(abs) + " symbolic-ref HEAD refs/heads/" + rep.branch
	}
	if err := os.Rename(lockPath, index); err != nil {
		if rep.branch != "" {
			_, _ = r.runOutput(ctx, abs, nil, false, "symbolic-ref", "HEAD", "refs/heads/"+rep.branch)
		} else {
			_, _ = r.runOutput(ctx, abs, nil, false, "update-ref", "--no-deref", "HEAD", rep.Head)
		}
		restoreDef()
		fail(GitRepoUnresolvable, "index rename failed after HEAD moved; HEAD and "+def+" restored: "+shortErr(err))
		return
	}
	if oldDef == "" {
		// A branch created here tracks origin's, as a checkout would have
		// set it up; realign and pull need the upstream.
		_, _ = r.runOutput(ctx, abs, nil, false, "config", "branch."+def+".remote", "origin")
		_, _ = r.runOutput(ctx, abs, nil, false, "config", "branch."+def+".merge", defRef)
	}
	rep.PreviousHead = rep.Head
	rep.Status = GitRepoRealigned
	rep.Undo = restoreHead + " && git -C " + shellWord(abs) + " reset --mixed -q " + rep.Head
}
