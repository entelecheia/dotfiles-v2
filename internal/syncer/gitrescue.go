package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// rescueChainLimit caps how many commits bestOnChain reads, newest first.
//
// ponytail: known ceiling. See docs/CEILINGS.md (rescue reads the newest
// 50 upstream commits).
const rescueChainLimit = 50

// classifyNoMatch refines a no-match repo into one of the #178 classes with a
// one-line next step. It reads refs, counts and content comparisons; under
// --apply --fetch its children's evidence can fetch a child first (#201).
// Diverged and branch-mismatch repos get a RescueTarget when a commit on the
// upstream or default branch matches the files better than HEAD.
func (r *gitStateRun) classifyNoMatch(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	rep.branch, _ = r.read(ctx, abs, "symbolic-ref", "-q", "--short", "HEAD")
	label := rep.branch
	if label == "" {
		label = "a detached HEAD"
	}

	// The branch's own upstream is read first: a feature branch rebased and
	// force-pushed on the other Mac must realign to its own upstream, not be
	// taken for a branch whose work landed on the default branch.
	upstream, _ := r.read(ctx, abs, "rev-parse", "--symbolic-full-name", "@{upstream}")
	upCand, upDiffs, upTie, upOK := "", 0, "", false
	// upBest is how well the branch's own upstream matches the files; the
	// default branch must beat it to call the checkout a branch mismatch.
	upBest := -1
	if upstream != "" {
		upCand, upDiffs, upTie, upOK = r.bestOnChain(ctx, abs, gitdir, rep.Head, upstream, rep.HeadDiffs)
		switch {
		case upOK:
			upBest = upDiffs
		case r.isAncestor(ctx, abs, upstream, "HEAD"):
			// HEAD is at or ahead of its upstream, so the chain above is
			// empty: score the upstream itself (the files peer sync
			// delivered from the other Mac's pushed branch).
			if d, err := r.rescueDiffs(ctx, abs, gitdir, rep.Head, upstream); err == nil {
				upBest = d
			}
		}
	}

	// A checkout on another branch (the dev/maru case: a squash-merged
	// feature branch) whose files match the default branch better than
	// anything on its own upstream. A detached HEAD, the normal state of a
	// submodule, is not a branch to rescue.
	if def, ref := r.defaultBranch(ctx, abs); ref != "" && rep.branch != "" && rep.branch != def {
		if cand, diffs, tie, ok := r.bestOnChain(ctx, abs, gitdir, rep.Head, ref, rep.HeadDiffs); ok && (upBest < 0 || diffs < upBest) {
			rep.Class = GitClassBranchMismatch
			rep.RescueTarget, rep.rescueBranch, rep.remote = cand, def, r.pushRemote(ctx, abs, rep.branch)
			rep.rescueDiffs = diffs
			rep.rescueTie = tie
			r.suggestRescue(ctx, abs, rep, fmt.Sprintf("on %s, but %s %s at %s", label, matchWords(diffs), def, shortRev(cand)),
				"keep HEAD on a rescue branch and switch")
			return
		}
	}

	if upstream == "" {
		return
	}
	upName := strings.TrimPrefix(strings.TrimPrefix(upstream, "refs/remotes/"), "refs/heads/")
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
		rep.remote = r.pushRemote(ctx, abs, rep.branch)
		if upOK {
			rep.RescueTarget = upCand
			rep.rescueDiffs = upDiffs
			rep.rescueTie = upTie
			r.suggestRescue(ctx, abs, rep, fmt.Sprintf("%d local-only commit(s) vs %d on %s, and %s %s", ahead, behind, upName, matchWords(upDiffs), shortRev(upCand)),
				"keep the local commits on a rescue branch and realign")
		} else {
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) vs %d on %s, and no upstream commit matches the files better than HEAD; rebase or merge by hand",
				ahead, behind, upName)
		}
	}
}

// pushRemote is where git would push branch: branch.<b>.pushRemote, then
// remote.pushDefault, then branch.<b>.remote, then origin.
func (r *gitStateRun) pushRemote(ctx context.Context, abs, branch string) string {
	keys := []string{"remote.pushDefault"}
	if branch != "" {
		keys = []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"}
	}
	for _, key := range keys {
		// "." is a remote too: git pushes such a branch into this repo, and
		// planRescue keeps its rescue branch local.
		if remote, err := r.read(ctx, abs, "config", key); err == nil && remote != "" {
			return remote
		}
	}
	return "origin"
}

func rescueCommand(path string, noPush bool) string {
	if noPush {
		return "dot peer git realign --rescue --no-push --apply " + shellWord(path)
	}
	return "dot peer git realign --rescue --apply " + shellWord(path)
}

// suggestRescue ends a rescue class's suggestion with a command the run
// accepts: the rescue, the rescue kept local when only a push is refused
// (Git LFS), or, when a rescue would be refused either way, the refusal and
// no command (#209).
func (r *gitStateRun) suggestRescue(ctx context.Context, abs string, rep *GitRepoReport, facts, action string) {
	rep.rescueRefused, rep.rescueLocalOnly = r.rescueRefusal(ctx, abs, rep)
	switch {
	case rep.rescueRefused == "":
		rep.Suggestion = facts + "; " + action + ": " + rescueCommand(rep.Path, false)
	case rep.rescueLocalOnly:
		rep.Suggestion = facts + "; " + rep.rescueRefused + "; " + action + ", keeping it local: " + rescueCommand(rep.Path, true)
	default:
		rep.Suggestion = facts + "; not rescued: " + rep.rescueRefused
	}
}

// rescueRefusal says, read-only, why a rescue of rep would be refused, and
// whether --no-push avoids it: the switch's own preconditions (the default
// branch in a linked worktree, or holding commits the target lacks) refuse
// any rescue; a pushed rescue branch in a Git LFS repo would point at objects
// the remote lacks, and dot does not drive git-lfs (#204).
func (r *gitStateRun) rescueRefusal(ctx context.Context, abs string, rep *GitRepoReport) (why string, localOnly bool) {
	if rep.rescueBranch != "" {
		defRef := "refs/heads/" + rep.rescueBranch
		if other := r.worktreeOnBranch(ctx, abs, defRef); other != "" {
			return rep.rescueBranch + " is checked out in the linked worktree " + other + "; switch that worktree to another branch, or run git worktree prune if its directory is gone", false
		}
		// Pushing or merging those commits keeps the branch off the
		// target's history, so only moving the branch aside lifts this.
		if oldDef, _ := r.read(ctx, abs, "rev-parse", "--verify", "-q", defRef); oldDef != "" && oldDef != rep.RescueTarget && !r.strictDescendant(ctx, abs, oldDef, rep.RescueTarget) {
			return "local " + rep.rescueBranch + " has commits " + shortRev(rep.RescueTarget) + " lacks; keep them on another branch first: git -C " + shellWord(abs) + " branch -m " + shellWord(rep.rescueBranch) + " " + shellWord(rep.rescueBranch+"-kept"), false
		}
	}
	if rep.remote != "." {
		if why := r.lfsRefusal(ctx, abs); why != "" {
			return why, true
		}
	}
	return "", false
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
// from head, whose content is closest to the worktree. Commits that tie go
// through breakTie like a realign's: a commit recording a gitlink its child
// has not reached (or a child the run leaves alone) loses to one that does
// not, then the newest wins. It succeeds only when that commit beats HEAD,
// and says how a tie was broken.
func (r *gitStateRun) bestOnChain(ctx context.Context, abs, gitdir, head, tip string, headDiffs int) (string, int, string, bool) {
	out, err := r.read(ctx, abs, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(rescueChainLimit), tip, "--not", head)
	if err != nil || out == "" {
		return "", 0, "", false
	}
	var tied []string
	bestDiffs := -1
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- { // oldest first, as realign lists candidates
		sha := strings.TrimSpace(lines[i])
		diffs, err := r.rescueDiffs(ctx, abs, gitdir, head, sha)
		switch {
		case err != nil:
		case bestDiffs < 0 || diffs < bestDiffs:
			tied, bestDiffs = []string{sha}, diffs
		case diffs == bestDiffs:
			tied = append(tied, sha)
		}
	}
	if len(tied) == 0 || bestDiffs >= headDiffs {
		return "", 0, "", false
	}
	best, rule, _ := r.breakTie(ctx, abs, "", "", false, tied)
	return best, bestDiffs, rule, true
}

// rescueDiffs scores a commit a rescue would move HEAD to: contentDiffs
// counts only the files that commit tracks, so a file HEAD tracks, the
// commit lacks and the worktree still holds (a feature branch's own file,
// seen from the default branch) would go unnoticed and become untracked
// after the move. Those count as differences too.
func (r *gitStateRun) rescueDiffs(ctx context.Context, abs, gitdir, head, commit string) (int, error) {
	key := abs + "\x00" + head + "\x00" + commit
	if n, ok := r.rescued[key]; ok {
		return n, nil
	}
	diffs, err := r.contentDiffs(ctx, abs, gitdir, commit)
	if err != nil {
		return -1, err
	}
	out, err := r.read(ctx, abs, "diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", "--ignore-submodules", head, commit, "--", ":(exclude).gitmodules")
	if err != nil {
		return -1, err
	}
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(abs, filepath.FromSlash(rel))); err == nil {
			diffs++
		}
	}
	if isCommitID(head) && isCommitID(commit) {
		if r.rescued == nil {
			r.rescued = map[string]int{}
		}
		r.rescued[key] = diffs
	}
	return diffs, nil
}

func (r *gitStateRun) isAncestor(ctx context.Context, abs, a, b string) bool {
	_, err := r.runOutput(ctx, abs, nil, true, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// planRescue turns a classified no-match into a realignable move under
// --rescue: the target is the matching commit and HEAD's commits get a
// rescue branch named after today and the branch.
func (r *gitStateRun) planRescue(ctx context.Context, abs string, rep *GitRepoReport) {
	// classifyNoMatch read the refusals (rescueRefusal) before a rescue
	// branch is created or pushed for a move that would be refused, and its
	// suggestion already says why; one only a push hits passes --no-push.
	if rep.rescueRefused != "" && (!rep.rescueLocalOnly || !r.opts.NoPush) {
		return
	}
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
	// The rescue's own tie line (and the children it passes) replaces the
	// realign's, which described HEAD, not this move.
	rep.TieBreak = rep.rescueTie
	rep.Target = rep.RescueTarget
	rep.TargetDiffs = rep.rescueDiffs
	// A branch tracking "." has no remote to keep a copy on.
	if !r.opts.NoPush && rep.remote != "." {
		rep.RescueRemote = rep.remote
	}
}

// lfsRefusal says why a rescue push is refused over Git LFS: a tracked
// .gitattributes, at any depth, whose attribute line sets filter=lfs (a
// pushed rescue branch would lack its LFS objects), or a check that could
// not tell, which refuses too. Empty when the push may go.
func (r *gitStateRun) lfsRefusal(ctx context.Context, abs string) string {
	code, err := r.run(ctx, abs, nil, true, "grep", "--cached", "-q", "-E", `^[[:space:]]*[^#[:space:]].*[[:space:]]filter=lfs([[:space:]]|$)`, "--", ":(glob)**/.gitattributes")
	switch {
	case err != nil || code > 1:
		return "cannot tell whether the repo uses Git LFS"
	case code == 0:
		return "the repo uses Git LFS, and a pushed rescue branch would lack its LFS objects"
	}
	return ""
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
	if rep.RescueRemote != "" {
		// No prompt can be answered here (a hook, a scheduled shell): fail
		// instead of waiting on credentials, and bound a hung remote.
		pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err := r.runOutput(pctx, abs, []string{"GIT_TERMINAL_PROMPT=0"}, false, "push", "-q", "--no-verify", "--recurse-submodules=no", rep.RescueRemote, ref+":"+ref)
		cancel()
		if err != nil {
			rep.Status = GitRepoUnresolvable
			rep.Reason = "rescue branch " + rep.Rescue + " kept locally but the push to " + rep.remote + " failed; HEAD not moved (retry, or use --no-push): " + shortErr(err)
			return
		}
		rep.RescuePushed = true
	}
	if rep.rescueBranch == "" {
		r.realign(ctx, abs, gitdir, rep)
		if rep.Status != GitRepoRealigned {
			rep.Reason += " (rescue branch " + rep.Rescue + " kept)"
		}
		return
	}
	r.switchBranch(ctx, abs, gitdir, rep)
}

// worktreeOnBranch names a worktree other than abs that has ref checked out.
func (r *gitStateRun) worktreeOnBranch(ctx context.Context, abs, ref string) string {
	out, err := r.read(ctx, abs, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	self, _ := filepath.EvalSymlinks(abs)
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch "+ref:
			if p, _ := filepath.EvalSymlinks(path); p != self {
				return path
			}
		}
	}
	return ""
}

// switchBranch points HEAD at the default branch, created at or
// fast-forwarded to the target, with the index built from the target, under
// the same index.lock protocol realign uses. A local default branch with
// commits the target lacks is refused.
func (r *gitStateRun) switchBranch(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	def, target := rep.rescueBranch, rep.Target
	defRef := "refs/heads/" + def
	// A branch checked out in a linked worktree must not move under it:
	// that worktree would see the move as staged changes undoing it.
	if other := r.worktreeOnBranch(ctx, abs, defRef); other != "" {
		rep.Status = GitRepoSkipped
		rep.Reason = def + " is checked out in the linked worktree " + other + "; HEAD not moved (rescue branch " + rep.Rescue + " kept)"
		return
	}
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
	// restoreDef puts the default branch back and says whether it could.
	restoreDef := func() string {
		if oldDef == target {
			return def + " unchanged"
		}
		var err error
		manual := "git -C " + shellWord(abs) + " update-ref -d " + defRef
		if oldDef == "" {
			_, err = r.runOutput(ctx, abs, nil, false, "update-ref", "-d", defRef, target)
		} else {
			manual = "git -C " + shellWord(abs) + " update-ref " + defRef + " " + oldDef
			_, err = r.runOutput(ctx, abs, nil, false, "update-ref", defRef, oldDef, target)
		}
		if err != nil {
			return "restoring " + def + " failed (" + shortErr(err) + "; by hand: " + manual + ")"
		}
		return def + " restored"
	}
	if oldDef != target {
		if _, err := r.runOutput(ctx, abs, nil, false, "update-ref", "-m", "dot peer rescue", defRef, target, oldDef); err != nil {
			fail(GitRepoSkipped, "compare-and-swap on "+def+" failed; repo untouched: "+shortErr(err))
			return
		}
	}
	if _, err := r.runOutput(ctx, abs, nil, false, "symbolic-ref", "-m", "dot peer rescue", "HEAD", defRef); err != nil {
		fail(GitRepoUnresolvable, "cannot point HEAD at "+def+"; "+restoreDef()+": "+shortErr(err))
		return
	}
	restoreHead := "git -C " + shellWord(abs) + " update-ref --no-deref HEAD " + rep.Head
	if rep.branch != "" {
		restoreHead = "git -C " + shellWord(abs) + " symbolic-ref HEAD " + shellWord("refs/heads/"+rep.branch)
	}
	if err := os.Rename(lockPath, index); err != nil {
		var herr error
		if rep.branch != "" {
			_, herr = r.runOutput(ctx, abs, nil, false, "symbolic-ref", "HEAD", "refs/heads/"+rep.branch)
		} else {
			_, herr = r.runOutput(ctx, abs, nil, false, "update-ref", "--no-deref", "HEAD", rep.Head)
		}
		headState := "HEAD restored"
		if herr != nil {
			headState = "restoring HEAD failed (" + shortErr(herr) + "; by hand: " + restoreHead + ")"
		}
		fail(GitRepoUnresolvable, "index rename failed after HEAD moved; "+headState+", "+restoreDef()+": "+shortErr(err))
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
