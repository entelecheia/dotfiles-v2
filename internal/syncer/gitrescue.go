package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		// A push after a rewrite can publish what it took out, as a pushed
		// rescue can (#216): the same whole-history twin check (#219).
		switch twins, _, err := r.rewriteTwins(ctx, abs, []string{"HEAD"}, []string{"--remotes"}); {
		case err != nil:
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) on %s; cannot tell whether HEAD's history holds commits a rewrite took out (%s), so check before pushing", ahead, label, shortErr(err))
		case twins > 0:
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) on %s, but %d commit(s) in HEAD's history are pre-rewrite versions of commits on the remote (same author, date and subject): do not push them; move the work you keep onto the rewritten history by hand", ahead, label, twins)
		default:
			rep.Suggestion = fmt.Sprintf("%d local-only commit(s) on %s; push them: git -C %s push", ahead, label, shellWord(abs))
		}
	case ahead > 0:
		rep.Class = GitClassDiverged
		rep.remote = r.pushRemote(ctx, abs, rep.branch)
		facts := fmt.Sprintf("%d local-only commit(s) vs %d on %s", ahead, behind, upName)
		// A rewritten upstream: the local commits are its pre-rewrite
		// versions, and pushing them would publish what the rewrite took
		// out, so a rescue stays local (#216).
		if twins, total, err := r.rewriteTwins(ctx, abs, []string{"HEAD", "--not", "@{upstream}"}, []string{"@{upstream}", "--not", "HEAD"}); err == nil && total > 0 && twins == total {
			rep.Class = GitClassRewrittenUpstream
			facts = fmt.Sprintf("%s was rewritten: the %d local-only commit(s) are pre-rewrite versions of commits on it (same author, date and subject); tags it moved stay at the old commits until git -C %s fetch --tags --force, which replaces them", upName, ahead, shellWord(abs))
		}
		if upOK {
			rep.RescueTarget = upCand
			rep.rescueDiffs = upDiffs
			rep.rescueTie = upTie
			r.suggestRescue(ctx, abs, rep, fmt.Sprintf("%s, and %s %s", facts, matchWords(upDiffs), shortRev(upCand)),
				"keep the local commits on a rescue branch and realign")
		} else if rep.Class == GitClassRewrittenUpstream {
			rep.Suggestion = facts + "; no upstream commit matches the files better than HEAD: move by hand, and do not push these commits"
		} else if twins, _, err := r.rewriteTwins(ctx, abs, []string{"HEAD"}, []string{"--remotes"}); err == nil && twins > 0 {
			rep.Suggestion = fmt.Sprintf("%s, and no upstream commit matches the files better than HEAD; rebase or merge by hand, but %d commit(s) in HEAD's history are pre-rewrite versions of commits on the remote (same author, date and subject): do not push them", facts, twins)
		} else {
			rep.Suggestion = facts + ", and no upstream commit matches the files better than HEAD; rebase or merge by hand"
		}
	}
}

// rewriteTwins counts the commits in the rev range revs that have a twin,
// a different commit with the same author, author date and subject, among
// the commits of against, and how many revs lists: a history rewrite (git
// filter-repo, a rebase) keeps those, so a twin is a pre-rewrite version
// of a commit (#216).
// ponytail: known ceiling. See docs/CEILINGS.md (rewrite twins by author, date and subject).
func (r *gitStateRun) rewriteTwins(ctx context.Context, abs string, revs, against []string) (twins, total int, err error) {
	// Each line is the commit id, a tab, then the key.
	lines := func(revs []string) ([]string, error) {
		// --no-show-signature: log.showSignature would add lines to a
		// signed commit's entry, and a rewrite drops signatures.
		out, err := r.read(ctx, abs, append([]string{"log", "--no-show-signature", "--format=%H%x09%an%x1f%ae%x1f%ad%x1f%s", "--date=raw"}, revs...)...)
		if err != nil || out == "" {
			return nil, err
		}
		return strings.Split(out, "\n"), nil
	}
	local, err := lines(revs)
	if err != nil || len(local) == 0 {
		return 0, 0, err
	}
	theirs, err := lines(against)
	if err != nil {
		return 0, 0, err
	}
	ids := map[string][]string{} // key -> commit ids
	for _, l := range theirs {
		id, key, _ := strings.Cut(l, "\t")
		ids[key] = append(ids[key], id)
	}
	for _, l := range local {
		id, key, _ := strings.Cut(l, "\t")
		if slices.ContainsFunc(ids[key], func(other string) bool { return other != id }) {
			twins++
		}
	}
	return twins, len(local), nil
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
// the remote lacks, and dot does not drive git-lfs (#204). Every refusal
// that applies is named with its step, so following them lifts them all.
func (r *gitStateRun) rescueRefusal(ctx context.Context, abs string, rep *GitRepoReport) (why string, localOnly bool) {
	var steps []string
	if rep.rescueBranch != "" {
		defRef := "refs/heads/" + rep.rescueBranch
		git := "git -C " + shellWord(abs)
		// Each worktree on the branch is named, with a step for that one
		// only: git worktree prune would drop every missing worktree's
		// HEAD and index, one on an unmounted drive included.
		for _, wt := range r.worktreesOnBranch(ctx, abs, defRef) {
			step := rep.rescueBranch + " is checked out in the linked worktree " + wt.path
			if wt.busy != "" {
				step = rep.rescueBranch + " is held by a " + wt.busy + " in the linked worktree " + wt.path
			}
			_, err := os.Stat(wt.path)
			switch present := err == nil || !os.IsNotExist(err); {
			case present && wt.busy != "":
				// Finishing or aborting can leave the branch checked out there.
				step += "; finish or abort it there, and if that leaves " + rep.rescueBranch + " checked out, switch that worktree to another branch"
			case present:
				step += "; switch that worktree to another branch"
			case wt.busy != "":
				// The admin dir a remove deletes holds that operation's
				// state: HEAD so far, an autostash, rewritten refs, the
				// reflog. dot names no command that drops it.
				step += ", which is missing; its admin dir " + wt.admin + " holds that " + wt.busy + "'s state (HEAD " + wt.head +
					", any autostash, rewritten refs and the reflog); if the worktree is gone for good, recover what you need from there first, then remove it with git worktree remove"
				if wt.locked {
					step += " (after git worktree unlock)"
				}
			default:
				step += ", which is missing; if it is gone for good, not just unmounted: "
				if wt.locked {
					step += git + " worktree unlock " + shellWord(wt.path) + "; "
				}
				step += git + " worktree remove " + shellWord(wt.path)
			}
			steps = append(steps, step)
		}
		// Pushing or merging those commits keeps the branch off the
		// target's history, so only moving the branch aside lifts this.
		if oldDef, _ := r.read(ctx, abs, "rev-parse", "--verify", "-q", defRef); oldDef != "" && oldDef != rep.RescueTarget && !r.strictDescendant(ctx, abs, oldDef, rep.RescueTarget) {
			steps = append(steps, "local "+rep.rescueBranch+" has commits "+shortRev(rep.RescueTarget)+" lacks; keep them on another branch first: "+
				git+" branch -m "+shellWord(rep.rescueBranch)+" "+shellWord(r.freeBranchName(ctx, abs, rep.rescueBranch+"-kept")))
		}
	}
	// A branch at a parent path of the rescue name (a branch named rescue)
	// blocks every rescue name under it, suffixed or not (#212).
	if base := r.rescueBase(rep); r.freeBranchName(ctx, abs, base) == "" {
		blocker := base[:strings.Index(base, "/")]
		step := "a branch named " + blocker + " keeps " + base + " from being created; rename it first: "
		// git refuses to rename a branch a rebase or bisect holds.
		for _, wt := range r.worktreesOnBranch(ctx, abs, "refs/heads/"+blocker) {
			if wt.busy != "" {
				step = "a branch named " + blocker + " keeps " + base + " from being created; finish or abort the " + wt.busy + " holding it in the linked worktree " + wt.path + ", then rename it: "
				break
			}
		}
		steps = append(steps, step+"git -C "+shellWord(abs)+" branch -m "+shellWord(blocker)+" "+shellWord(r.freeBranchName(ctx, abs, blocker+"-kept")))
	}
	// Refusals only a push hits, which --no-push lifts: Git LFS, and a
	// branch named rescue on the push remote, as its remote-tracking refs
	// show, which blocks every pushed rescue name there (#214).
	var pushOnly []string
	if rep.remote != "." {
		// A commit in HEAD's history with a twin in a remote-tracking ref's
		// history is a pre-rewrite version: pushing a rescue that holds it
		// publishes what the rewrite took out, whatever the class (#216).
		// Both sides are whole histories, so no ref hides either: not a
		// stale tracking ref (HEAD's own upstream included), not a push
		// remote without tracking refs, not a rewrite merged into the old
		// history.
		twins, total, err := r.rewriteTwins(ctx, abs, []string{"HEAD"}, []string{"--remotes"})
		failed := err != nil
		switch {
		case rep.Class == GitClassRewrittenUpstream:
			pushOnly = append(pushOnly, "a pushed rescue branch would publish the history the rewrite took out")
		case twins > 0:
			pushOnly = append(pushOnly, fmt.Sprintf("%d of the %d commit(s) in HEAD's history are pre-rewrite versions of commits on the remote (same author, date and subject), so a pushed rescue would publish the history a rewrite took out", twins, total))
		case failed:
			pushOnly = append(pushOnly, "cannot tell whether a pushed rescue would publish history a rewrite took out")
		}
		if why := r.lfsRefusal(ctx, abs); why != "" {
			pushOnly = append(pushOnly, why)
		}
		if base := r.rescueBase(rep); r.freeBranchName(ctx, abs, base, "refs/remotes/"+rep.remote+"/") == "" {
			// Only that tracking ref is named: fetch --prune would drop
			// every stale one of the remote, with its reflog.
			blocker := base[:strings.Index(base, "/")]
			pushOnly = append(pushOnly, "the remote "+rep.remote+" has a branch named "+blocker+" (as last fetched; if it was deleted there: git -C "+shellWord(abs)+" branch -d -r "+shellWord(rep.remote+"/"+blocker)+"), which keeps "+base+" from being pushed")
		}
	}
	push := strings.Join(pushOnly, "; ")
	switch {
	case len(steps) == 0:
		return push, push != ""
	case push != "":
		steps = append(steps, push+"; rescue with --no-push after that")
	}
	if len(steps) == 1 {
		return steps[0], false
	}
	for i := range steps {
		steps[i] = "(" + strconv.Itoa(i+1) + ") " + steps[i]
	}
	return strings.Join(steps, "; "), false
}

// remoteMoved reads, over the network, the remote branch the rescue target
// is on (the upstream, or origin's default branch for a branch mismatch)
// and says how it differs from what the last fetch saw, with the remote to
// fetch: a rewrite is seen here only once fetched (#216), so a rescue is
// not pushed past one this repo has not seen, nor when the remote cannot be
// read (#220). "" when it has not moved, or the branch has no remote.
func (r *gitStateRun) remoteMoved(ctx context.Context, abs string, rep *GitRepoReport) (why, remote string) {
	branch, tracking := "", "@{upstream}"
	if rep.rescueBranch != "" {
		remote, branch, tracking = "origin", "refs/heads/"+rep.rescueBranch, "refs/remotes/origin/"+rep.rescueBranch
	} else {
		remote, _ = r.read(ctx, abs, "config", "--get", "branch."+rep.branch+".remote")
		branch, _ = r.read(ctx, abs, "config", "--get", "branch."+rep.branch+".merge")
	}
	if remote == "" || remote == "." || branch == "" {
		return "", ""
	}
	name := strings.TrimPrefix(branch, "refs/heads/")
	seen, _ := r.read(ctx, abs, "rev-parse", "--verify", "-q", tracking)
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	out, err := r.runOutput(pctx, abs, []string{"GIT_TERMINAL_PROMPT=0"}, true, "ls-remote", remote, branch)
	cancel()
	if err != nil {
		return "cannot read " + remote + "'s " + name + " to check it: " + shortErr(err), remote
	}
	now := ""
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if sha, ref, ok := strings.Cut(line, "\t"); ok && ref == branch {
			now = sha
		}
	}
	switch now {
	case seen:
		return "", ""
	case "":
		return remote + " no longer has " + name + " (the last fetch saw " + shortRev(seen) + ")", remote
	}
	return remote + "'s " + name + " is at " + shortRev(now) + " but the last fetch saw " + shortRev(seen), remote
}

// rescueBase is the rescue branch name before a free-name suffix:
// rescue/<yymmdd>-<branch>.
func (r *gitStateRun) rescueBase(rep *GitRepoReport) string {
	name := rep.branch
	if name == "" {
		name = "detached"
	}
	return "rescue/" + r.opts.Now().Format("060102") + "-" + strings.ReplaceAll(name, "/", "-")
}

// freeBranchName is base, or base-2, base-3, ..., the first name no ref
// keeps from being created in spaces (refs/heads/ when none is given; a
// pushed rescue adds the push remote's remote-tracking refs): git refs are
// files and directories, so a ref of that name, one under it (for-each-ref
// matches up to a slash) or one at a parent path blocks it. It is "" when a
// parent blocks base, since no suffix helps then.
func (r *gitStateRun) freeBranchName(ctx context.Context, abs, base string, spaces ...string) string {
	if len(spaces) == 0 {
		spaces = []string{"refs/heads/"}
	}
	for i := 0; i < len(base); i++ {
		if base[i] != '/' {
			continue
		}
		for _, space := range spaces {
			if _, err := r.read(ctx, abs, "rev-parse", "--verify", "-q", space+base[:i]); err == nil {
				return ""
			}
		}
	}
	name := base
	for i := 2; ; i++ {
		args := []string{"for-each-ref", "--count=1", "--format=x"}
		for _, space := range spaces {
			args = append(args, space+name)
		}
		if taken, _ := r.read(ctx, abs, args...); taken == "" {
			return name
		}
		name = base + "-" + strconv.Itoa(i)
	}
}

// ShellWord is shellWord for callers outside the package (a command hint).
func ShellWord(s string) string { return shellWord(s) }

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
	// A pushed rescue also skips names the push remote holds (#214).
	spaces := []string{"refs/heads/"}
	if !r.opts.NoPush && rep.remote != "." {
		spaces = append(spaces, "refs/remotes/"+rep.remote+"/")
	}
	if rep.Rescue = r.freeBranchName(ctx, abs, r.rescueBase(rep), spaces...); rep.Rescue == "" {
		return // blocked; rescueRefusal already refused it
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
	if rep.RescueRemote != "" {
		if why, remote := r.remoteMoved(ctx, abs, rep); why != "" {
			rep.Status = GitRepoUnresolvable
			rep.Reason = "not rescued: " + why + ", and a rewrite shows here only once fetched; HEAD not moved (git -C " + shellWord(abs) + " fetch " + shellWord(remote) + ", then run again, or use --no-push)"
			return
		}
	}
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
			// The remote may hold the name, or a rescue branch, unseen. The
			// kept rescue branch already moves a rerun to the next name; only
			// the fetch shows a remote rescue branch, which then turns the
			// rerun into the --no-push refusal. Other failures (access, a
			// server rule) need their own fix. A remote whose fetch does not
			// map its branches to refs/remotes/<remote>/ shows neither.
			rep.Reason = "rescue branch " + rep.Rescue + " kept locally but the push to " + rep.remote + " failed; HEAD not moved (if " + rep.remote + " holds this name or a rescue branch: git -C " + shellWord(abs) + " fetch " + shellWord(rep.remote) +
				", then run again, which picks a free name or names --no-push; otherwise fix what the error below says; or use --no-push): " + shortErr(err)
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

// linkedWorktree is a worktree other than the repo's own, from
// git worktree list --porcelain.
type linkedWorktree struct {
	path   string
	head   string // its HEAD commit
	admin  string // its admin dir, <common-dir>/worktrees/<id>
	locked bool   // git worktree lock
	busy   string // "rebase" or "bisect" when one holds the branch
}

// worktreesOnBranch lists the worktrees other than abs that hold ref: those
// with it checked out, and, as git counts them, those whose rebase or bisect
// holds it, which the porcelain may list as detached or on another branch
// (#211).
func (r *gitStateRun) worktreesOnBranch(ctx context.Context, abs, ref string) []linkedWorktree {
	out, err := r.read(ctx, abs, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	self, _ := filepath.EvalSymlinks(abs)
	var on []linkedWorktree
	var admins map[string]string
	for _, block := range strings.Split(out, "\n\n") {
		var wt linkedWorktree
		onRef := false
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				wt.path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "HEAD "):
				wt.head = strings.TrimPrefix(line, "HEAD ")
			case line == "branch "+ref:
				onRef = true
			case line == "locked" || strings.HasPrefix(line, "locked "):
				wt.locked = true
			}
		}
		if admins == nil {
			admins = r.worktreeAdmins(ctx, abs)
		}
		wt.admin = admins[wt.path]
		if wt.busy = busyOn(wt.admin, ref); wt.busy != "" {
			onRef = true
		}
		if p, _ := filepath.EvalSymlinks(wt.path); onRef && p != self {
			on = append(on, wt)
		}
	}
	return on
}

// worktreeAdmins maps each linked worktree's path to its admin dir
// (<common-dir>/worktrees/<id>), whose gitdir file names <path>/.git,
// relative to the admin dir for a worktree added with --relative-paths
// (the porcelain lists that one resolved).
func (r *gitStateRun) worktreeAdmins(ctx context.Context, abs string) map[string]string {
	admins := map[string]string{}
	common, err := r.read(ctx, abs, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return admins
	}
	// ReadDir, not Glob: the common dir's path is not a pattern.
	entries, _ := os.ReadDir(filepath.Join(common, "worktrees"))
	for _, e := range entries {
		dir := filepath.Join(common, "worktrees", e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "gitdir"))
		if err != nil {
			continue
		}
		path := strings.TrimSuffix(strings.TrimSpace(string(b)), "/.git")
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
			if real, err := filepath.EvalSymlinks(path); err == nil {
				path = real
			}
		}
		admins[path] = dir
	}
	return admins
}

// busyOn says whether the worktree with admin dir admin is rebasing or
// bisecting ref, read the way git branch -f does before it moves one: the
// branch a rebase started from, one a rebase --update-refs will move, or the
// one a bisect started from.
func busyOn(admin, ref string) string {
	if admin == "" {
		return ""
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(admin, name))
		return strings.TrimSpace(string(b))
	}
	switch {
	case read("rebase-merge/head-name") == ref, read("rebase-apply/head-name") == ref,
		slices.Contains(strings.Split(read("rebase-merge/update-refs"), "\n"), ref):
		return "rebase"
	case read("BISECT_START") == strings.TrimPrefix(ref, "refs/heads/"):
		return "bisect"
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
	if on := r.worktreesOnBranch(ctx, abs, defRef); len(on) > 0 {
		rep.Status = GitRepoSkipped
		rep.Reason = def + " is held by the linked worktree " + on[0].path + "; HEAD not moved (rescue branch " + rep.Rescue + " kept)"
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
