package syncer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// urlMove is one submodule whose URL a committed .gitmodules changed.
type urlMove struct{ old, new string }

// checkGitmodules compares the worktree .gitmodules with the one in rev (the
// commit the repo sits on, or moves to in a preview). Peer sync never carries
// .gitmodules, so after a realign the worktree keeps the old copy and git
// shows it as a local edit that reverts the peer's commit (#179). A worktree
// copy equal to an older committed version is stale: --apply restores it
// from the index and syncs the moved submodule URLs. Anything else is a
// local edit and left alone. It returns the URL moves by submodule path.
func (r *gitStateRun) checkGitmodules(ctx context.Context, abs, rev string, apply bool, rep *GitRepoReport) map[string]*urlMove {
	switch rep.Status {
	case GitRepoAligned, GitRepoRealignable, GitRepoRealigned:
	default:
		return nil
	}
	worktree, err := os.ReadFile(filepath.Join(abs, ".gitmodules"))
	if err != nil {
		return nil
	}
	want, err := r.runOutput(ctx, abs, nil, true, "show", rev+":.gitmodules")
	if err != nil || want == string(worktree) {
		return nil
	}
	if !r.staleGitmodules(ctx, abs, rev, string(worktree)) {
		rep.Gitmodules = "modified"
		return nil
	}
	oldURLs := r.gitmodulesURLs(ctx, abs, "-f", filepath.Join(abs, ".gitmodules"))
	newURLs := r.gitmodulesURLs(ctx, abs, "--blob", rev+":.gitmodules")
	moves := map[string]*urlMove{}
	var paths []string
	for path, url := range newURLs {
		if old, ok := oldURLs[path]; ok && old != url {
			moves[path] = &urlMove{old: old, new: url}
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	rep.Gitmodules = "stale"
	for _, path := range paths {
		rep.URLMoves = append(rep.URLMoves, path+": "+moves[path].old+" -> "+moves[path].new)
	}
	if !apply {
		return moves
	}
	// After a realign the index holds rev's .gitmodules; checkout writes
	// that one file and nothing else in the worktree.
	if _, err := r.runOutput(ctx, abs, nil, false, "checkout", "-q", "--", ".gitmodules"); err != nil {
		rep.Gitmodules = "stale (restore failed: " + shortErr(err) + ")"
		return moves
	}
	rep.Gitmodules = "restored"
	if len(paths) > 0 {
		args := append([]string{"submodule", "sync", "-q", "--"}, paths...)
		if _, err := r.runOutput(ctx, abs, nil, false, args...); err != nil {
			rep.Gitmodules = "restored (submodule sync failed: " + shortErr(err) + ")"
		}
	}
	return moves
}

// staleGitmodules reports whether content equals a version of .gitmodules
// that rev's history replaced: the file before each of the last ten commits
// that touched it.
func (r *gitStateRun) staleGitmodules(ctx context.Context, abs, rev, content string) bool {
	out, err := r.read(ctx, abs, "rev-list", "-n", "10", rev, "--", ".gitmodules")
	if err != nil {
		return false
	}
	for _, commit := range strings.Fields(out) {
		if old, err := r.runOutput(ctx, abs, nil, true, "show", commit+"^:.gitmodules"); err == nil && old == content {
			return true
		}
	}
	return false
}

// gitmodulesURLs maps submodule path to URL from one .gitmodules source:
// "-f <file>" or "--blob <rev>:.gitmodules".
func (r *gitStateRun) gitmodulesURLs(ctx context.Context, abs, flag, source string) map[string]string {
	out, err := r.read(ctx, abs, "config", flag, source, "--get-regexp", `^submodule\..*\.(path|url)$`)
	if err != nil {
		return nil
	}
	paths, urls := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name, ok := strings.CutPrefix(key, "submodule.")
		if !ok {
			continue
		}
		if n, ok := strings.CutSuffix(name, ".path"); ok {
			paths[n] = value
		} else if n, ok := strings.CutSuffix(name, ".url"); ok {
			urls[n] = value
		}
	}
	byPath := map[string]string{}
	for name, path := range paths {
		if url, ok := urls[name]; ok {
			byPath[path] = url
		}
	}
	return byPath
}

// missingGitlink handles a child whose parent gitlink commit is not in its
// object store. A moved submodule URL is the usual cause after a peer
// switch: the new commit exists only at the new remote. With --apply --fetch
// the child points origin at the moved URL, fetches, and is classified
// again; otherwise the report names the exact commands.
func (r *gitStateRun) missingGitlink(ctx context.Context, abs, gitdir, gitlink string, move *urlMove, apply bool, rep *GitRepoReport) {
	commands := "git -C " + abs + " fetch origin"
	if move != nil {
		commands = "git -C " + abs + " remote set-url origin " + move.new + " && " + commands
	}
	if !apply || !r.opts.Fetch {
		if move != nil {
			rep.Class = GitClassURLMoved
			rep.Suggestion = "the submodule URL moved from " + move.old + " to " + move.new + "; run " + commands + " and realign again, or realign with --apply --fetch"
		} else {
			rep.Class = GitClassGitlinkMissing
			rep.Suggestion = "fetch it: " + commands + ", or realign with --apply --fetch"
		}
		return
	}
	if move != nil {
		if _, err := r.runOutput(ctx, abs, nil, false, "remote", "set-url", "origin", move.new); err != nil {
			rep.Reason = gitlinkMissing + "; setting the moved URL failed: " + shortErr(err)
			return
		}
	}
	if _, err := r.runOutput(ctx, abs, nil, false, "fetch", "-q", "origin"); err != nil {
		rep.Reason = gitlinkMissing + "; fetch failed: " + shortErr(err)
		return
	}
	fresh := &GitRepoReport{Path: rep.Path}
	r.classify(ctx, abs, gitdir, gitlink, fresh)
	*rep = *fresh
}
