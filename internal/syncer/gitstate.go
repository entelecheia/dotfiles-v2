package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GitRepoStatus classifies one discovered repository in a peer git run.
type GitRepoStatus string

const (
	// GitRepoAligned means the worktree already matches HEAD's own content
	// (gitlinks and .gitmodules aside: the children and peer sync's
	// exclusion account for those).
	GitRepoAligned GitRepoStatus = "aligned"
	// GitRepoRealignable means a strict descendant of HEAD matches the
	// worktree at least as well as HEAD does.
	GitRepoRealignable GitRepoStatus = "realignable"
	// GitRepoRealigned means HEAD and the index were moved to the target.
	GitRepoRealigned GitRepoStatus = "realigned"
	// GitRepoNoMatch means no strict descendant candidate improves on HEAD.
	GitRepoNoMatch GitRepoStatus = "no-match"
	// GitRepoSkipped means the repo was left alone with a recorded reason:
	// a lock, an operation in progress, unmerged entries, staged changes, an
	// unborn HEAD or no checkout at the gitlink path.
	GitRepoSkipped GitRepoStatus = "skipped"
	// GitRepoUnresolvable means an object the classification needs is
	// missing (a vanished parent gitlink, an unenumerable upstream chain).
	GitRepoUnresolvable GitRepoStatus = "unresolvable"
	// GitRepoLinkedWorktree means the checkout is a linked worktree (its
	// gitdir carries a commondir file): listed, never realigned.
	GitRepoLinkedWorktree GitRepoStatus = "linked-worktree"
)

// GitRepoReport is the per-repo outcome of a status or realign run.
type GitRepoReport struct {
	Path   string        `json:"path"` // workspace-relative, "." for the root
	Status GitRepoStatus `json:"status"`
	Reason string        `json:"reason,omitempty"`
	Head   string        `json:"head,omitempty"`
	// HeadDiffs counts tracked worktree differences against HEAD.
	HeadDiffs int `json:"headDiffs,omitempty"`
	// Target is the best candidate commit for a realignable repo.
	Target      string `json:"target,omitempty"`
	TargetDiffs int    `json:"targetDiffs,omitempty"`
	Candidates  int    `json:"candidates,omitempty"`
	// TieBreak says which rule chose Target when several candidates matched
	// the worktree equally well (#177); empty when there was no tie.
	TieBreak string `json:"tieBreak,omitempty"`
	// PreviousHead is HEAD's commit before an applied move; Undo is the exact
	// command that restores it (for a branch switch, HEAD's branch too; a
	// default branch the switch created or fast-forwarded stays, and so does
	// a .gitmodules the run restored and synced).
	PreviousHead string `json:"previousHead,omitempty"`
	// Class refines a no-match or skipped outcome and Suggestion is the
	// one-line next step for it (#178).
	Class      string `json:"class,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
	// RescueTarget is the commit on the upstream or default branch the files
	// match, for a diverged or branch-mismatch repo; --rescue moves there.
	RescueTarget string `json:"rescueTarget,omitempty"`
	// Rescue names the branch that keeps HEAD's commits when --rescue moves
	// the repo; RescueRemote is where it is pushed and RescuePushed says it
	// got there.
	Rescue       string `json:"rescue,omitempty"`
	RescueRemote string `json:"rescueRemote,omitempty"` // empty: kept local (--no-push)
	RescuePushed bool   `json:"rescuePushed,omitempty"`
	// Undo is the exact command that reverses an applied realign or rescue.
	Undo string `json:"undo,omitempty"`
	// Gitmodules reports the worktree .gitmodules against the commit the
	// repo sits on (or moves to): "stale" (an older committed version peer
	// sync never replaced), "missing" (absent from the worktree), "restored"
	// (put back from HEAD by --apply) or "modified" (local edits, left
	// alone). URLMoves lists the submodule URLs that version changes, or the
	// origin --fetch re-pointed, "path: old -> new" (#179); URLUndo puts a
	// re-pointed origin back.
	Gitmodules string   `json:"gitmodules,omitempty"`
	URLMoves   []string `json:"urlMoves,omitempty"`
	URLUndo    string   `json:"urlUndo,omitempty"`

	branch       string // HEAD's branch, empty when detached
	rescueBranch string // branch-mismatch: the default branch HEAD moves onto
	remote       string // remote a rescue branch is pushed to
	rescueDiffs  int    // worktree differences against RescueTarget
}

// Classes of no-match and skipped outcomes (#178).
const (
	GitClassAtTip           = "at-tip"
	GitClassAheadUnpushed   = "ahead-unpushed"
	GitClassDiverged        = "diverged"
	GitClassBranchMismatch  = "branch-mismatch"
	GitClassStaleRebaseHead = "stale-rebase-head"
	GitClassURLMoved        = "url-moved"
	GitClassGitlinkMissing  = "gitlink-missing"
)

// GitStateResult is one status or realign run over the workspace tree.
type GitStateResult struct {
	Root string
	// Git is the git binary resolved to an absolute path once per run
	// (launchd and non-interactive shells do not share the user's PATH).
	Git   string
	Repos []*GitRepoReport // parent-first
}

// CountByStatus tallies the reports per status, in a stable order.
func (r *GitStateResult) CountByStatus() map[GitRepoStatus]int {
	counts := map[GitRepoStatus]int{}
	for _, rep := range r.Repos {
		counts[rep.Status]++
	}
	return counts
}

// GitRepoStatuses is the display/iteration order for statuses.
var GitRepoStatuses = []GitRepoStatus{
	GitRepoAligned,
	GitRepoRealignable,
	GitRepoRealigned,
	GitRepoNoMatch,
	GitRepoSkipped,
	GitRepoUnresolvable,
	GitRepoLinkedWorktree,
}

// PeerGitStatus classifies the workspace root and every submodule,
// recursively and parent first. It writes no objects, no index entries and no
// worktree files. repos optionally restricts the report to
// workspace-relative paths ("." is the root).
func PeerGitStatus(ctx context.Context, root string, repos []string) (*GitStateResult, error) {
	return runGitState(ctx, root, repos, RealignOptions{})
}

// RealignOptions controls PeerGitRealign.
type RealignOptions struct {
	// Apply moves HEAD and index; false is a pure preview.
	Apply bool
	// Rescue also moves diverged and branch-mismatch repos to the commit
	// their files match, after keeping HEAD on a rescue branch (#178).
	Rescue bool
	// NoPush keeps rescue branches local instead of pushing them.
	NoPush bool
	// Fetch lets an applied run fetch a child whose parent gitlink object is
	// missing, after pointing it at a moved submodule URL, then retry it.
	Fetch bool
	// Now dates rescue branch names; nil means time.Now.
	Now func() time.Time
}

// PeerGitRealign runs the same classification and, with opts.Apply, moves
// each realignable repo's HEAD and index to its best candidate through git's
// lockfile protocol. Without Apply it is a pure preview and changes nothing
// under .git.
func PeerGitRealign(ctx context.Context, root string, repos []string, opts RealignOptions) (*GitStateResult, error) {
	return runGitState(ctx, root, repos, opts)
}

// gitStateRun carries the per-run state: the resolved git binary, the
// options and the optional repo restriction.
type gitStateRun struct {
	git      string
	opts     RealignOptions
	restrict map[string]bool // nil means all repos
	env      []string        // the environment without git's repo-local variables
}

// gitCleanEnv drops the variables that pin git to one repository (GIT_DIR,
// GIT_WORK_TREE, GIT_INDEX_FILE, ...): run from a git hook, they would aim
// every per-repo command at the hook's repository. Like git entering a
// submodule, it keeps `git -c` settings (GIT_CONFIG_PARAMETERS,
// GIT_CONFIG_COUNT and its keys): they are the caller's config, not a repo.
func gitCleanEnv(ctx context.Context, git string) []string {
	out, err := exec.CommandContext(ctx, git, "rev-parse", "--local-env-vars").Output()
	if err != nil {
		return os.Environ()
	}
	local := map[string]bool{}
	for _, name := range strings.Fields(string(out)) {
		local[name] = name != "GIT_CONFIG_PARAMETERS" && name != "GIT_CONFIG_COUNT"
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !local[name] {
			env = append(env, kv)
		}
	}
	return env
}

func runGitState(ctx context.Context, root string, repos []string, opts RealignOptions) (*GitStateResult, error) {
	root = strings.TrimRight(root, "/")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git not found on PATH: %w", err)
	}
	// LookPath may return a relative path when PATH carries a relative
	// entry; every invocation must resolve to the same absolute binary.
	if gitPath, err = filepath.Abs(gitPath); err != nil {
		return nil, fmt.Errorf("resolving git path: %w", err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	run := &gitStateRun{git: gitPath, opts: opts, env: gitCleanEnv(ctx, gitPath)}
	if len(repos) > 0 {
		run.restrict = map[string]bool{}
		for _, arg := range repos {
			run.restrict[normalizeRepoArg(arg)] = true
		}
	}
	result := &GitStateResult{Root: root, Git: gitPath}

	// Validate the restriction against a read-only discovery walk before any
	// mutation, so a mistyped repo argument cannot realign half the tree.
	if run.restrict != nil {
		known, err := run.discoverPaths(ctx, root)
		if err != nil {
			return nil, err
		}
		for arg := range run.restrict {
			if !known[arg] {
				return nil, fmt.Errorf("no repo at %q under %s", arg, root)
			}
		}
	}

	run.process(ctx, root, ".", "", "", opts.Apply, result)
	return result, nil
}

func normalizeRepoArg(arg string) string {
	arg = strings.TrimRight(strings.TrimSpace(arg), "/")
	if arg == "" || arg == "." {
		return "."
	}
	return filepath.ToSlash(arg)
}

func (r *gitStateRun) included(relPath string) bool {
	if r.restrict == nil {
		return true
	}
	return r.restrict[relPath]
}

// discoveredPaths lists every repo path the run would visit, without
// classifying anything. Used only to reject unknown restriction arguments
// before a realign starts.
func (r *gitStateRun) discoverPaths(ctx context.Context, root string) (map[string]bool, error) {
	known := map[string]bool{}
	var walk func(abs, rel string) error
	walk = func(abs, rel string) error {
		known[rel] = true
		if _, err := r.gitDir(ctx, abs); err != nil {
			return nil // not a checked-out repo: no children to find
		}
		gitlinks, err := r.childGitlinks(ctx, abs, "HEAD")
		if err != nil {
			return nil // unenumerable: the real pass reports the repo itself
		}
		for _, child := range gitlinks {
			childAbs := filepath.Join(abs, filepath.FromSlash(child.path))
			childRel := rel
			if childRel == "." {
				childRel = child.path
			} else {
				childRel += "/" + child.path
			}
			if err := walk(childAbs, childRel); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, "."); err != nil {
		return nil, err
	}
	return known, nil
}

// process classifies (and optionally realigns) one repo, then recurses into
// its submodule gitlinks. Children are enumerated AFTER the parent has been
// processed, so a child's candidate gitlink is read from the parent's
// realigned HEAD.
func (r *gitStateRun) process(ctx context.Context, abs, rel, gitlink, wantURL string, apply bool, result *GitStateResult) {
	rep := &GitRepoReport{Path: rel}
	included := r.included(rel)
	defer func() {
		if included {
			result.Repos = append(result.Repos, rep)
		}
	}()

	gitdir, err := r.gitDir(ctx, abs)
	if err != nil {
		rep.Status = GitRepoSkipped
		if rel == "." {
			rep.Reason = "not a git repository"
		} else {
			rep.Reason = "no checkout at the gitlink path"
		}
		return
	}

	// A linked worktree's gitdir carries a commondir file; a submodule's own
	// gitdir does not. Listed, never realigned, never recursed into.
	if _, statErr := os.Stat(filepath.Join(gitdir, "commondir")); statErr == nil {
		rep.Status = GitRepoLinkedWorktree
		rep.Reason = "linked worktree"
		return
	}

	if included {
		r.classify(ctx, abs, gitdir, gitlink, rep)
		if rep.Status == GitRepoUnresolvable && rep.Reason == gitlinkMissing {
			r.missingGitlink(ctx, abs, gitdir, gitlink, wantURL, apply, rep)
		}
		if r.opts.Rescue && rep.Status == GitRepoNoMatch && rep.RescueTarget != "" {
			r.planRescue(ctx, abs, rep)
		}
		if apply && rep.Status == GitRepoRealignable {
			if rep.Rescue != "" {
				r.rescue(ctx, abs, gitdir, rep)
			} else {
				r.realign(ctx, abs, gitdir, rep)
			}
		}
	}

	// A preview reads the children from the commit the parent would move to,
	// so it shows what --apply does: apply reads them from the moved HEAD.
	rev := "HEAD"
	if included && !apply && rep.Status == GitRepoRealignable {
		rev = rep.Target
	}
	if included {
		r.checkGitmodules(ctx, abs, rev, apply, rep)
	}
	// Each child learns the URL rev's .gitmodules gives it, read even when
	// this repo is outside the restriction, so a child whose gitlink commit
	// is missing can tell a moved URL from a missing fetch.
	urls := r.gitmodulesURLs(ctx, abs, "--blob", rev+":.gitmodules")
	gitlinks, err := r.childGitlinks(ctx, abs, rev)
	if err != nil {
		// Children of a repo whose HEAD cannot be read cannot be discovered;
		// say so on the parent rather than failing the whole run, but do not
		// clobber a more precise classification (an unborn HEAD already says
		// why ls-tree failed).
		if included && (rep.Status == "" || rep.Status == GitRepoAligned) {
			rep.Status = GitRepoUnresolvable
			rep.Reason = "cannot enumerate submodule gitlinks"
		}
		return
	}
	for _, child := range gitlinks {
		childRel := child.path
		if rel != "." {
			childRel = rel + "/" + child.path
		}
		r.process(ctx, filepath.Join(abs, filepath.FromSlash(child.path)), childRel, child.sha, urls[child.path], apply, result)
	}
}

// classify fills rep with the repo's status without mutating anything.
func (r *gitStateRun) classify(ctx context.Context, abs, gitdir, gitlink string, rep *GitRepoReport) {
	if reason := r.blockReason(ctx, abs, gitdir); reason != "" {
		rep.Status = GitRepoSkipped
		rep.Reason = reason
		if reason == staleRebaseHead {
			rep.Class = GitClassStaleRebaseHead
			rep.Suggestion = "no rebase is in progress; clear it: git -C " + shellWord(abs) + " update-ref -d REBASE_HEAD"
		}
		return
	}

	head, err := r.read(ctx, abs, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		rep.Status = GitRepoSkipped
		rep.Reason = "no HEAD (unborn branch)"
		return
	}
	rep.Head = head

	headDiffs, err := r.contentDiffs(ctx, abs, gitdir, head)
	if err != nil {
		rep.Status = GitRepoUnresolvable
		rep.Reason = "cannot read HEAD tree: " + shortErr(err)
		return
	}
	rep.HeadDiffs = headDiffs
	// HEAD's tree matching does not end the search: a strict descendant with
	// the same content (empty commits, a net-unchanged sequence, gitlink-only
	// commits, or the parent gitlink) would otherwise leave HEAD behind
	// forever. Moving is fast-forward onto a descendant, so no commit is ever
	// dropped. While HEAD matches, a candidate that cannot be read keeps the
	// repo aligned instead of unresolvable.
	aligned := func() { rep.Status = GitRepoAligned }
	candidates, err := r.candidates(ctx, abs, head, gitlink)
	if err != nil {
		// A missing parent gitlink commit is reported even when HEAD's own
		// content matches: its content can be identical while the parent
		// already points past HEAD, and only the report leads to the fetch.
		if headDiffs == 0 && err.Error() != gitlinkMissing {
			aligned()
			return
		}
		rep.Status = GitRepoUnresolvable
		rep.Reason = err.Error()
		return
	}
	rep.Candidates = len(candidates)
	if len(candidates) == 0 {
		if headDiffs == 0 {
			aligned()
			return
		}
		rep.Status = GitRepoNoMatch
		rep.Reason = "no strict descendant candidate"
		r.classifyNoMatch(ctx, abs, gitdir, rep)
		return
	}

	var tied []string
	bestDiffs := -1
	for _, cand := range candidates {
		diffs, err := r.contentDiffs(ctx, abs, gitdir, cand)
		if err != nil {
			if headDiffs == 0 {
				aligned()
				return
			}
			rep.Status = GitRepoUnresolvable
			rep.Reason = "cannot read candidate tree: " + shortErr(err)
			return
		}
		switch {
		case bestDiffs < 0 || diffs < bestDiffs:
			bestDiffs, tied = diffs, []string{cand}
		case diffs == bestDiffs:
			tied = append(tied, cand)
		}
	}
	if bestDiffs > headDiffs {
		if headDiffs == 0 {
			aligned()
			return
		}
		rep.Status = GitRepoNoMatch
		rep.Reason = "no descendant commit improves on HEAD"
		rep.Target = tied[0]
		rep.TargetDiffs = bestDiffs
		r.classifyNoMatch(ctx, abs, gitdir, rep)
		return
	}
	// A tie with HEAD (best == headDiffs) is still a realign unless the
	// children say HEAD is right: moving to a strict descendant never makes
	// the own-content classification worse, and a parent whose only drift is
	// in its children's gitlinks must move so each child reads the new
	// gitlinks. But a clean parent must not move past gitlink bumps whose
	// content its children do not have yet.
	headTie := ""
	if bestDiffs == headDiffs {
		headTie = head
	}
	target, rule, stay := r.breakTie(ctx, abs, gitlink, headTie, tied)
	rep.TieBreak = rule
	if stay {
		if headDiffs == 0 {
			aligned()
			return
		}
		rep.Status = GitRepoNoMatch
		rep.Reason = "the children match HEAD's gitlinks better than any descendant's"
		r.classifyNoMatch(ctx, abs, gitdir, rep)
		return
	}
	rep.Status = GitRepoRealignable
	rep.Target = target
	rep.TargetDiffs = bestDiffs
}

// breakTie picks among candidates whose own content matches the worktree
// equally well. Content excludes gitlinks, so a run of gitlink-only commits
// ties, and taking the nearest one (the old rule) moved a parent to its
// oldest candidate and every child to a stale gitlink (#177). In order:
//
//  1. children: the candidates whose gitlinks match the most children's
//     worktree content, when the children tell them apart. When HEAD ties
//     too (head is set) it competes here, and stay reports that it won;
//  2. the parent's gitlink, which keeps the parent's status clean;
//  3. the newest candidate, the upstream tip side of the chain.
//
// It returns the choice and a one-line account of the rule, empty when
// nothing had to be decided.
func (r *gitStateRun) breakTie(ctx context.Context, abs, gitlink, head string, tied []string) (string, string, bool) {
	contenders := tied
	prefix := fmt.Sprintf("%d candidates tie on content; ", len(tied))
	if head != "" {
		contenders = append([]string{head}, tied...)
		prefix = fmt.Sprintf("HEAD and %d candidate(s) tie on content; ", len(tied))
	}
	if len(contenders) == 1 {
		return tied[0], "", false
	}
	pool, children := r.bestByChildren(ctx, abs, contenders)
	if head != "" {
		if len(pool) == 1 && pool[0] == head {
			return "", prefix + children + ", so HEAD stays", true
		}
		pool = slices.DeleteFunc(slices.Clone(pool), func(c string) bool { return c == head })
	}
	if len(pool) == 1 {
		if children == "" {
			return pool[0], "", false
		}
		return pool[0], prefix + children, false
	}
	if children != "" {
		prefix += children + ", then "
	}
	for _, cand := range pool {
		if cand == gitlink {
			return cand, prefix + "parent gitlink", false
		}
	}
	// Candidate order is the parent gitlink first, then the first-parent
	// chain nearest HEAD first; without the gitlink, the last is the newest.
	return pool[len(pool)-1], prefix + "newest candidate", false
}

// bestByChildren scores each tied candidate by how many of its submodule
// gitlinks name a commit the child's worktree already matches, counting only
// the gitlinks that differ between the candidates. It returns the top
// candidates in their original order and, when the scores differ, how the
// winners scored.
func (r *gitStateRun) bestByChildren(ctx context.Context, abs string, tied []string) ([]string, string) {
	links := make([]map[string]string, len(tied))
	varying := map[string]bool{}
	for i, cand := range tied {
		entries, err := r.childGitlinks(ctx, abs, cand)
		if err != nil {
			return tied, ""
		}
		links[i] = map[string]string{}
		for _, e := range entries {
			links[i][e.path] = e.sha
		}
	}
	for path, sha := range links[0] {
		for _, other := range links[1:] {
			if other[path] != sha {
				varying[path] = true
			}
		}
	}
	for _, other := range links[1:] {
		for path := range other {
			if _, ok := links[0][path]; !ok {
				varying[path] = true
			}
		}
	}
	if len(varying) == 0 {
		return tied, ""
	}
	matches := map[string]bool{} // path + " " + sha
	scores := make([]int, len(tied))
	best, worst := -1, -1
	for i := range tied {
		for path := range varying {
			sha, ok := links[i][path]
			if !ok {
				continue
			}
			key := path + " " + sha
			match, seen := matches[key]
			if !seen {
				match = r.childMatches(ctx, abs, path, sha)
				matches[key] = match
			}
			if match {
				scores[i]++
			}
		}
		if best < 0 || scores[i] > best {
			best = scores[i]
		}
		if worst < 0 || scores[i] < worst {
			worst = scores[i]
		}
	}
	if best == worst {
		return tied, ""
	}
	var pool []string
	for i, cand := range tied {
		if scores[i] == best {
			pool = append(pool, cand)
		}
	}
	return pool, fmt.Sprintf("children match %d of %d differing gitlinks", best, len(varying))
}

// childMatches reports whether the child checkout at path already holds the
// content of commit sha. A missing checkout or object is no match.
func (r *gitStateRun) childMatches(ctx context.Context, abs, path, sha string) bool {
	child := filepath.Join(abs, filepath.FromSlash(path))
	gitdir, err := r.gitDir(ctx, child)
	if err != nil {
		return false
	}
	if _, err := r.read(ctx, child, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return false
	}
	diffs, err := r.contentDiffs(ctx, child, gitdir, sha)
	return err == nil && diffs == 0
}

const (
	staleRebaseHead = "stale REBASE_HEAD"
	gitlinkMissing  = "parent gitlink object is not present locally"
)

// blockReason reports why a repo must be skipped and reported rather than
// classified or realigned: a lock, an operation in progress, unmerged
// entries or staged changes (the 09-24 stale-lock incident class).
func (r *gitStateRun) blockReason(ctx context.Context, abs, gitdir string) string {
	if _, err := os.Stat(filepath.Join(gitdir, "index.lock")); err == nil {
		return "index.lock present"
	}
	// Unmerged before the operation markers: a real merge or cherry-pick
	// conflict sets both, and the unmerged entries are the actionable part.
	if out, err := r.read(ctx, abs, "ls-files", "-u"); err != nil {
		return "cannot inspect index entries: " + shortErr(err)
	} else if strings.TrimSpace(out) != "" {
		return "unmerged index entries"
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Stat(filepath.Join(gitdir, marker)); err == nil {
			return "operation in progress (" + marker + ")"
		}
	}
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		if st, err := os.Stat(filepath.Join(gitdir, dir)); err == nil && st.IsDir() {
			return "operation in progress (" + dir + ")"
		}
	}
	// A rebase in progress always has one of the directories above; a
	// REBASE_HEAD without them is a leftover (#178).
	if _, err := os.Stat(filepath.Join(gitdir, "REBASE_HEAD")); err == nil {
		return staleRebaseHead
	}
	if _, err := r.read(ctx, abs, "diff", "--cached", "--quiet"); err != nil {
		var exitErr *gitExitError
		if errors.As(err, &exitErr) && exitErr.code == 1 {
			return "staged changes"
		}
		return "cannot inspect staged state: " + shortErr(err)
	}
	return ""
}

// candidates returns the strict-descendant candidate commits, parent gitlink
// first, then the first-parent chain from HEAD to the branch upstream
// (nearest HEAD first). The command never fetches; the chain reflects
// whatever the caller fetched beforehand.
func (r *gitStateRun) candidates(ctx context.Context, abs, head, gitlink string) ([]string, error) {
	var out []string
	if gitlink != "" && gitlink != head {
		if _, err := r.read(ctx, abs, "cat-file", "-e", gitlink+"^{commit}"); err != nil {
			return nil, errors.New(gitlinkMissing)
		}
		if r.strictDescendant(ctx, abs, head, gitlink) {
			out = append(out, gitlink)
		}
	}
	if _, err := r.read(ctx, abs, "rev-parse", "--verify", "-q", "@{upstream}"); err != nil {
		return out, nil // no upstream configured: gitlink-only
	}
	chain, err := r.read(ctx, abs, "rev-list", "--first-parent", "HEAD..@{upstream}")
	if err != nil {
		return nil, errors.New("cannot enumerate the upstream first-parent chain: " + shortErr(err))
	}
	lines := strings.Split(strings.TrimSpace(chain), "\n")
	for i := len(lines) - 1; i >= 0; i-- { // rev-list is newest-first; want nearest HEAD first
		sha := strings.TrimSpace(lines[i])
		if sha == "" || sha == head || sha == gitlink {
			continue // the parent gitlink is already the first candidate
		}
		if r.strictDescendant(ctx, abs, head, sha) {
			out = append(out, sha)
		}
	}
	return out, nil
}

// strictDescendant reports whether cand is a descendant of head and not head
// itself. This is the fast-forward-only guarantee: realign never moves a ref
// backward or sideways.
func (r *gitStateRun) strictDescendant(ctx context.Context, abs, head, cand string) bool {
	if cand == head {
		return false
	}
	code, err := r.run(ctx, abs, nil, true, "merge-base", "--is-ancestor", head, cand)
	return err == nil && code == 0
}

// contentDiffs counts tracked files whose worktree content differs from a
// commit's tree, using a throwaway index: copy the real index to a temp file,
// read-tree -m into it, refresh stat info, then diff-files. This writes no
// objects and holds no lock on the real index. Untracked files never appear.
func (r *gitStateRun) contentDiffs(ctx context.Context, abs, gitdir, commit string) (int, error) {
	tempIndex, cleanup, err := r.tempIndexFor(ctx, abs, gitdir, commit)
	if err != nil {
		return -1, err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + tempIndex}
	// update-index exits non-zero exactly when files need update; that is the
	// expected signal here, not an error.
	_, _ = r.run(ctx, abs, env, false, "update-index", "-q", "--refresh")
	// Gitlinks are the children's business: a child realigns separately and
	// its HEAD is stale until then, so counting it here would favor the
	// parent commit that changed the fewest gitlinks (#177). .gitmodules is
	// never carried by peer sync, so it is no evidence either (#179).
	out, err := r.readEnv(ctx, abs, env, "diff-files", "--ignore-submodules", "--name-only", "--", ":(exclude).gitmodules")
	if err != nil {
		return -1, err
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// tempIndexFor builds a temp index holding the candidate tree. It first
// tries seeding from the repo's real index so unchanged entries keep their
// stat info; a one-tree read-tree -m refuses to replace entries the worktree
// has modified ("not uptodate"), and that is exactly the realign case, so a
// refusal retries from an empty index and lets the refresh decide by content.
// The caller removes the file via cleanup.
func (r *gitStateRun) tempIndexFor(ctx context.Context, abs, gitdir, commit string) (string, func(), error) {
	seed, err := os.ReadFile(filepath.Join(gitdir, "index"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", func() {}, err
	}
	tempIndex, cleanup, err := r.writeTempIndex(ctx, abs, commit, seed)
	if err == nil {
		return tempIndex, cleanup, nil
	}
	cleanup()
	return r.writeTempIndex(ctx, abs, commit, nil)
}

func (r *gitStateRun) writeTempIndex(ctx context.Context, abs, commit string, seed []byte) (string, func(), error) {
	tmp, err := os.CreateTemp("", "dot-gitstate-index-*")
	if err != nil {
		return "", func() {}, err
	}
	tempIndex := tmp.Name()
	cleanup := func() { _ = os.Remove(tempIndex) }
	if _, err := tmp.Write(seed); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if len(seed) == 0 {
		// A zero-byte index file is corrupt to git; an empty index is a
		// MISSING file. read-tree creates it at the given path.
		if err := os.Remove(tempIndex); err != nil {
			return "", func() {}, err
		}
	}
	env := []string{"GIT_INDEX_FILE=" + tempIndex}
	// read-tree is a write to the temp index only; its lock is the temp's own.
	if _, err := r.runOutput(ctx, abs, env, false, "read-tree", "-m", commit); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tempIndex, cleanup, nil
}

// realign moves HEAD and the index to rep.Target through git's lockfile
// protocol: create index.lock O_EXCL, write the temp index into it,
// compare-and-swap HEAD from the recorded old value, rename the lock over the
// index. The worktree is never written by git. On a compare-and-swap failure
// the lock is removed and the repo is left exactly as it was.
func (r *gitStateRun) realign(ctx context.Context, abs, gitdir string, rep *GitRepoReport) {
	unresolvable := func(reason string) {
		rep.Status = GitRepoUnresolvable
		rep.Reason = reason
	}
	skipped := func(reason string) {
		rep.Status = GitRepoSkipped
		rep.Reason = reason
	}

	tempIndex, cleanup, err := r.tempIndexFor(ctx, abs, gitdir, rep.Target)
	if err != nil {
		unresolvable("cannot build target index: " + shortErr(err))
		return
	}
	defer cleanup()

	index := filepath.Join(gitdir, "index")
	lockPath := index + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		skipped("index.lock appeared before realign: " + shortErr(err))
		return
	}
	built, err := os.ReadFile(tempIndex)
	if err == nil {
		_, err = lock.Write(built)
	}
	if closeErr := lock.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(lockPath)
		unresolvable("cannot write index lock: " + shortErr(err))
		return
	}

	if _, err := r.runOutput(ctx, abs, nil, false, "update-ref", "-m", "dot peer realign", "HEAD", rep.Target, rep.Head); err != nil {
		_ = os.Remove(lockPath)
		skipped("compare-and-swap failed; repo untouched: " + shortErr(err))
		return
	}
	if err := os.Rename(lockPath, index); err != nil {
		// HEAD already moved; roll it back so the repo is left exactly as
		// found, and never report a realign that did not complete.
		if _, rbErr := r.runOutput(ctx, abs, nil, false, "update-ref", "-m", "dot peer realign rollback", "HEAD", rep.Head); rbErr != nil {
			rep.PreviousHead = rep.Head
			rep.Status = GitRepoUnresolvable
			rep.Reason = "index rename failed and HEAD rollback failed (HEAD is " + rep.Target + "; recover manually with git update-ref HEAD " + rep.Head + "): " + shortErr(err)
			return
		}
		_ = os.Remove(lockPath)
		unresolvable("index rename failed after HEAD moved; HEAD rolled back: " + shortErr(err))
		return
	}
	rep.PreviousHead = rep.Head
	rep.Status = GitRepoRealigned
	rep.Undo = "git -C " + shellWord(abs) + " reset --mixed -q " + rep.Head
}

// gitDir resolves the absolute gitdir of the checkout at abs.
//
// abs must be the top level of that checkout. git searches upward from a
// directory without its own .git, so an uninitialized submodule (an empty
// directory, a deinit, files peer sync delivered without the gitfile) would
// otherwise resolve to the parent: the parent would be classified, fetched
// and re-pointed as if it were the child, and ls-tree there lists the
// child's own gitlink as "./", recursing forever.
func (r *gitStateRun) gitDir(ctx context.Context, abs string) (string, error) {
	top, err := r.read(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	want, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	// SameFile, not a path compare: symlinks and a case-insensitive volume
	// spell one directory several ways.
	if got, err := os.Stat(top); err != nil || !os.SameFile(got, want) {
		return "", fmt.Errorf("%s is not the top of a checkout (git resolves it to %s)", abs, top)
	}
	return r.read(ctx, abs, "rev-parse", "--absolute-git-dir")
}

type gitlinkEntry struct {
	path string // relative to the repo root
	sha  string
}

// childGitlinks lists the submodule gitlinks recorded in commit rev: ls-tree
// mode 160000 entries, at any depth within this repo (ls-tree does not cross
// into submodules).
func (r *gitStateRun) childGitlinks(ctx context.Context, abs, rev string) ([]gitlinkEntry, error) {
	out, err := r.read(ctx, abs, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return nil, err
	}
	var entries []gitlinkEntry
	for _, record := range bytes.Split([]byte(out), []byte{0}) {
		meta, path, ok := bytes.Cut(record, []byte("\t"))
		if !ok {
			continue
		}
		fields := bytes.Fields(meta)
		if len(fields) != 3 || string(fields[0]) != "160000" {
			continue
		}
		entries = append(entries, gitlinkEntry{path: string(path), sha: string(fields[2])})
	}
	return entries, nil
}

// read runs a read-only git command and returns trimmed stdout. Read
// commands always carry --no-optional-locks so classification never takes an
// optional index lock (the temp-index refresh writes only the temp file).
func (r *gitStateRun) read(ctx context.Context, abs string, args ...string) (string, error) {
	return r.readEnv(ctx, abs, nil, args...)
}

func (r *gitStateRun) readEnv(ctx context.Context, abs string, env []string, args ...string) (string, error) {
	out, err := r.runOutput(ctx, abs, env, true, args...)
	return strings.TrimSpace(out), err
}

// run executes git and reports the exit code. A non-zero git exit is an
// answer, not an error (diff --quiet, merge-base --is-ancestor); only a
// command that could not run at all returns a non-nil error.
func (r *gitStateRun) run(ctx context.Context, abs string, env []string, readOnly bool, args ...string) (int, error) {
	_, err := r.runOutput(ctx, abs, env, readOnly, args...)
	if err == nil {
		return 0, nil
	}
	var exitErr *gitExitError
	if errors.As(err, &exitErr) {
		return exitErr.code, nil
	}
	return -1, err
}

func (r *gitStateRun) runOutput(ctx context.Context, abs string, env []string, readOnly bool, args ...string) (string, error) {
	full := []string{"-C", abs}
	if readOnly {
		full = []string{"--no-optional-locks", "-C", abs}
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, r.git, full...)
	cmd.Dir = abs
	// After a timeout kills git, its ssh or https helper can hold the output
	// pipes open; stop waiting on them.
	cmd.WaitDelay = 5 * time.Second
	base := r.env
	if base == nil {
		base = os.Environ() // a run built without runGitState
	}
	cmd.Env = append(append([]string{}, base...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), &gitExitError{code: exitErr.ExitCode(), stderr: strings.TrimSpace(stderr.String())}
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// gitExitError carries git's exit code and stderr without the "exit status N"
// wrapper text, so skip/unresolvable reasons stay readable.
type gitExitError struct {
	code   int
	stderr string
}

func (e *gitExitError) Error() string {
	if e.stderr == "" {
		return "git exit status " + strconv.Itoa(e.code)
	}
	return e.stderr
}

const maxReasonLen = 120

func shortErr(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > maxReasonLen {
		msg = msg[:maxReasonLen] + "..."
	}
	return msg
}
