package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// urlMove is one submodule whose URL a committed .gitmodules changed.
type urlMove struct{ old, new string }

// checkGitmodules compares the worktree .gitmodules with the one in rev (the
// commit the repo sits on, or moves to in a preview). Peer sync never carries
// .gitmodules, so after a realign the worktree keeps the old copy, or none
// when the peer added the first submodule, and git shows a local edit that
// reverts the peer's commit (#179). A copy equal to an older committed
// version, or a missing one, is stale: --apply restores it from the index
// and syncs the moved submodule URLs. Anything else is a local edit and left
// alone.
func (r *gitStateRun) checkGitmodules(ctx context.Context, abs, rev string, apply bool, rep *GitRepoReport) {
	switch rep.Status {
	case GitRepoAligned, GitRepoRealignable, GitRepoRealigned:
	default:
		return
	}
	want, err := r.runOutput(ctx, abs, nil, true, "show", rev+":.gitmodules")
	if err != nil {
		return // rev has no .gitmodules: nothing to restore
	}
	path := filepath.Join(abs, ".gitmodules")
	worktree, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return
	}
	if !missing && string(worktree) == want {
		return
	}
	if !missing && !r.staleGitmodules(ctx, abs, rev, string(worktree)) {
		rep.Gitmodules = "modified"
		return
	}
	var paths []string
	if missing {
		rep.Gitmodules = "missing"
	} else {
		rep.Gitmodules = "stale"
		oldURLs := r.gitmodulesURLs(ctx, abs, "-f", path)
		for p, url := range r.gitmodulesURLs(ctx, abs, "--blob", rev+":.gitmodules") {
			if old, ok := oldURLs[p]; ok && old != url {
				paths = append(paths, p)
				rep.URLMoves = append(rep.URLMoves, p+": "+old+" -> "+url)
			}
		}
		sort.Strings(paths)
		sort.Strings(rep.URLMoves)
	}
	if !apply {
		return
	}
	// After a realign the index holds rev's .gitmodules; checkout writes
	// that one file and nothing else in the worktree.
	if _, err := r.runOutput(ctx, abs, nil, false, "checkout", "-q", "--", ".gitmodules"); err != nil {
		rep.Gitmodules += " (restore failed: " + shortErr(err) + ")"
		return
	}
	rep.Gitmodules = "restored"
	if len(paths) > 0 {
		args := append([]string{"submodule", "sync", "-q", "--"}, paths...)
		if _, err := r.runOutput(ctx, abs, nil, false, args...); err != nil {
			rep.Gitmodules = "restored (submodule sync failed: " + shortErr(err) + ")"
		}
	}
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
// switch: the new commit exists only at the new remote. The child compares
// its origin URL with the one the parent's commit records (wantURL; a
// relative URL is not compared). With --apply --fetch it points origin at a
// moved URL, fetches, and is classified again; otherwise the report names
// the exact commands.
func (r *gitStateRun) missingGitlink(ctx context.Context, abs, gitdir, gitlink, wantURL string, apply bool, rep *GitRepoReport) {
	var move *urlMove
	if wantURL != "" && !strings.HasPrefix(wantURL, "./") && !strings.HasPrefix(wantURL, "../") {
		if have, err := r.read(ctx, abs, "remote", "get-url", "origin"); err == nil && have != wantURL {
			move = &urlMove{old: have, new: wantURL}
		}
	}
	commands := "git -C " + shellWord(abs) + " fetch origin"
	if move != nil {
		commands = "git -C " + shellWord(abs) + " remote set-url origin " + shellWord(move.new) + " && " + commands
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
	if fresh.Reason == gitlinkMissing {
		fresh.Class = GitClassGitlinkMissing
		fresh.Suggestion = "origin was fetched but " + shortRev(gitlink) + " is still missing; check that the parent's commit was pushed with its submodule"
	}
	*rep = *fresh
}
