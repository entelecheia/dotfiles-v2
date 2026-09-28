package syncer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// urlMoveFixture reproduces #179: the peer's commit p1 moves the submodule
// URL and bumps the gitlink to s2, which exists only at the new URL. The
// switched-to workspace sits at p0 with the old .gitmodules (peer sync never
// carries it) and the child's files already at s2.
func urlMoveFixture(t *testing.T) (ws, sub, oldURL, newURL, p1, s2 string) {
	t.Helper()
	tmp := t.TempDir()
	oldURL = filepath.Join(tmp, "sub-old")
	gitStateInitRepo(t, oldURL)
	gitStateCommitFile(t, oldURL, "file.txt", "v1\n", "s1")
	newURL = filepath.Join(tmp, "sub-new")
	gitStateRun_(t, tmp, "clone", "-q", oldURL, newURL)
	s2 = gitStateCommitFile(t, newURL, "file.txt", "v2\n", "s2")

	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
	gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", oldURL, "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "p0 add sub")
	p0 := gitStateHead(t, origin)
	gitStateRun_(t, origin, "config", "-f", ".gitmodules", "submodule.sub.url", newURL)
	gitStateRun_(t, filepath.Join(origin, "sub"), "fetch", "-q", newURL)
	gitStateRun_(t, filepath.Join(origin, "sub"), "checkout", "-q", s2)
	gitStateRun_(t, origin, "add", ".gitmodules", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "p1 move sub url and bump")
	p1 = gitStateHead(t, origin)

	ws = filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "clone", "-q", origin, ws)
	gitStateRun_(t, ws, "reset", "-q", "--hard", p0)
	gitStateRun_(t, ws, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	sub = filepath.Join(ws, "sub")
	// --fetch applies git submodule's protocol rules; the fixtures' remotes
	// are local paths, which those rules refuse unless allowed.
	gitStateRun_(t, sub, "config", "protocol.file.allow", "always")
	gitStateRewriteTracked(t, filepath.Join(sub, "file.txt"), "v2\n")
	return ws, sub, oldURL, newURL, p1, s2
}

func TestPeerGitRealign_URLMovePreviewPrintsCommands(t *testing.T) {
	ws, _, oldURL, newURL, p1, _ := urlMoveFixture(t)
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root := gitStateReport(t, res, ".")
	if root.Status != GitRepoRealignable || root.Target != p1 || root.Gitmodules != "stale" ||
		!slices.Contains(root.URLMoves, "sub: "+oldURL+" -> "+newURL) {
		t.Fatalf("root = %+v", root)
	}
	child := gitStateReport(t, res, "sub")
	if child.Class != GitClassURLMoved || !strings.Contains(child.Suggestion, "remote set-url origin "+newURL) {
		t.Fatalf("child = %+v", child)
	}
}

func TestPeerGitRealign_URLMoveApplyWithoutFetch(t *testing.T) {
	ws, sub, _, newURL, p1, _ := urlMoveFixture(t)
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if root := gitStateReport(t, res, "."); root.Status != GitRepoRealigned || root.Gitmodules != "restored" || gitStateHead(t, ws) != p1 {
		t.Fatalf("root = %+v", root)
	}
	if out := gitStateRun_(t, ws, "status", "--porcelain", "--", ".gitmodules"); out != "" {
		t.Fatalf(".gitmodules still modified: %s", out)
	}
	if got := gitStateRun_(t, sub, "remote", "get-url", "origin"); got != newURL {
		t.Fatalf("child origin = %s, want the synced %s", got, newURL)
	}
	if child := gitStateReport(t, res, "sub"); child.Status != GitRepoUnresolvable || !strings.Contains(child.Suggestion, "fetch origin") {
		t.Fatalf("child = %+v", child)
	}
}

func TestPeerGitRealign_URLMoveApplyWithFetchAlignsChild(t *testing.T) {
	ws, sub, _, newURL, p1, s2 := urlMoveFixture(t)
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true, Fetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if gitStateHead(t, ws) != p1 || gitStateHead(t, sub) != s2 {
		t.Fatalf("HEADs %s / %s, want %s / %s: %+v", gitStateHead(t, ws), gitStateHead(t, sub), p1, s2, gitStateReport(t, res, "sub"))
	}
	if got := gitStateRun_(t, sub, "remote", "get-url", "origin"); got != newURL {
		t.Fatalf("child origin = %s, want %s", got, newURL)
	}
	if out := gitStateRun_(t, ws, "status", "--porcelain"); out != "" {
		t.Fatalf("workspace not clean:\n%s", out)
	}
}

// A local .gitmodules edit (no committed version matches) is left alone.
func TestPeerGitRealign_LocalGitmodulesEditLeftAlone(t *testing.T) {
	ws, _, _, _ := gitStateBehindFixture(t)
	path := filepath.Join(ws, ".gitmodules")
	gitStateRewriteTracked(t, path, string(gitStateFileBytes(t, path))+"\tbranch = local\n")
	before := gitStateFileBytes(t, path)
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if root := gitStateReport(t, res, "."); root.Gitmodules != "modified" {
		t.Fatalf("root = %+v", root)
	}
	if got := gitStateFileBytes(t, path); string(got) != string(before) {
		t.Fatal("a local .gitmodules edit was overwritten")
	}
}

// A child detects the moved URL from the parent's commit, not only through a
// stale worktree .gitmodules: here the parent was realigned and .gitmodules
// restored by hand without `submodule sync`, and a later run restricted to
// the child fetches from the moved URL.
func TestPeerGitRealign_URLMoveDetectedFromTheParentCommit(t *testing.T) {
	ws, sub, _, newURL, p1, s2 := urlMoveFixture(t)
	gitStateRun_(t, ws, "reset", "-q", "--mixed", p1)
	gitStateRun_(t, ws, "checkout", "-q", "--", ".gitmodules")

	res, err := PeerGitRealign(context.Background(), ws, []string{"sub"}, RealignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if child := gitStateReport(t, res, "sub"); child.Class != GitClassURLMoved || !strings.Contains(child.Suggestion, newURL) {
		t.Fatalf("child = %+v", child)
	}
	applied, err := PeerGitRealign(context.Background(), ws, []string{"sub"}, RealignOptions{Apply: true, Fetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitStateHead(t, sub); got != s2 {
		t.Fatalf("child HEAD = %s, want %s", got, s2)
	}
	// The child re-pointed its own origin and moved HEAD: the report keeps
	// both undo commands.
	child := gitStateReport(t, applied, "sub")
	if len(child.URLMoves) != 1 || !strings.Contains(child.URLUndo, "remote set-url origin") {
		t.Fatalf("the re-pointed origin left no record: %+v", child)
	}
	if child.Status != GitRepoRealigned || !strings.Contains(child.Undo, "reset --mixed -q") {
		t.Fatalf("the HEAD move left no undo: %+v", child)
	}
}

// The peer added the first submodule: its .gitmodules never reached this
// worktree. --apply restores it.
func TestPeerGitRealign_RestoresMissingGitmodules(t *testing.T) {
	ws, _, _, _, _, _ := urlMoveFixture(t)
	if err := os.Remove(filepath.Join(ws, ".gitmodules")); err != nil {
		t.Fatal(err)
	}
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if root := gitStateReport(t, res, "."); root.Gitmodules != "restored" {
		t.Fatalf("root = %+v", root)
	}
	if _, err := os.Stat(filepath.Join(ws, ".gitmodules")); err != nil {
		t.Fatalf(".gitmodules not restored: %v", err)
	}
}

// gitlinkBumpFixture: the parent's upstream bumps sub to s2, a commit the
// child does not have yet. The parent is realigned to that bump already;
// the child's files match s2 (empty is true when s2 changes no file).
func gitlinkBumpFixture(t *testing.T, empty bool) (ws, sub, subSrc, s2 string) {
	t.Helper()
	tmp := t.TempDir()
	subSrc = filepath.Join(tmp, "sub-src")
	gitStateInitRepo(t, subSrc)
	gitStateCommitFile(t, subSrc, "file.txt", "v1\n", "s1")
	origin := filepath.Join(tmp, "origin")
	gitStateInitRepo(t, origin)
	gitStateCommitFile(t, origin, "readme.md", "parent\n", "base")
	gitStateRun_(t, origin, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc, "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "p0")
	ws = filepath.Join(tmp, "ws")
	gitStateRun_(t, tmp, "-c", "protocol.file.allow=always", "clone", "-q", "--recurse-submodules", origin, ws)
	if empty {
		gitStateRun_(t, subSrc, "commit", "-q", "--allow-empty", "-m", "s2 empty")
		s2 = gitStateHead(t, subSrc)
	} else {
		s2 = gitStateCommitFile(t, subSrc, "file.txt", "v2\n", "s2")
	}
	gitStateRun_(t, filepath.Join(origin, "sub"), "fetch", "-q", "origin")
	gitStateRun_(t, filepath.Join(origin, "sub"), "checkout", "-q", s2)
	gitStateRun_(t, origin, "add", "sub")
	gitStateRun_(t, origin, "commit", "-q", "-m", "p1 bump")
	gitStateRun_(t, ws, "fetch", "-q", "--no-recurse-submodules", "origin")
	gitStateRun_(t, ws, "reset", "-q", "--mixed", "origin/main")
	sub = filepath.Join(ws, "sub")
	gitStateRun_(t, sub, "config", "protocol.file.allow", "always")
	if !empty {
		gitStateRewriteTracked(t, filepath.Join(sub, "file.txt"), "v2\n")
	}
	return ws, sub, subSrc, s2
}

// A child whose missing gitlink commit changes none of its files still
// reports the missing commit (it used to read as aligned), and --fetch
// brings it.
func TestPeerGitRealign_MissingGitlinkWithMatchingContent(t *testing.T) {
	ws, sub, _, s2 := gitlinkBumpFixture(t, true)
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if child := gitStateReport(t, res, "sub"); child.Class != GitClassGitlinkMissing {
		t.Fatalf("child = %+v, want gitlink-missing", child)
	}
	if _, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true, Fetch: true}); err != nil {
		t.Fatal(err)
	}
	if got := gitStateHead(t, sub); got != s2 {
		t.Fatalf("child HEAD = %s, want %s", got, s2)
	}
}

// An origin rewritten by url.<base>.insteadOf is not a moved URL: git
// resolves both sides the same way, and --fetch must not touch origin.
func TestPeerGitRealign_InsteadOfIsNotAMove(t *testing.T) {
	ws, sub, subSrc, s2 := gitlinkBumpFixture(t, false)
	const alias = "https://example.invalid/sub.git"
	gitStateRun_(t, ws, "config", "-f", ".gitmodules", "submodule.sub.url", alias)
	gitStateRun_(t, ws, "commit", "-q", "-m", "gitmodules alias", "--", ".gitmodules")
	gitStateRun_(t, sub, "config", "url."+subSrc+".insteadOf", alias)
	gitStateRun_(t, sub, "config", "remote.origin.url", alias)

	res, err := PeerGitRealign(context.Background(), ws, []string{"sub"}, RealignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if child := gitStateReport(t, res, "sub"); child.Class != GitClassGitlinkMissing {
		t.Fatalf("child = %+v, want gitlink-missing, not url-moved", child)
	}
	if _, err := PeerGitRealign(context.Background(), ws, []string{"sub"}, RealignOptions{Apply: true, Fetch: true}); err != nil {
		t.Fatal(err)
	}
	if got := gitStateRun_(t, sub, "config", "--get", "remote.origin.url"); got != alias {
		t.Fatalf("origin rewritten to %s", got)
	}
	if got := gitStateHead(t, sub); got != s2 {
		t.Fatalf("child HEAD = %s, want %s", got, s2)
	}
}

// An uninitialized submodule (an empty directory) is no checkout: git would
// resolve it to the parent, and --apply --fetch must not re-point or fetch
// the parent as if it were the child, nor recurse into it forever.
func TestPeerGitRealign_UninitializedSubmoduleNeverTouchesTheParent(t *testing.T) {
	ws, sub, _, _ := gitlinkBumpFixture(t, false)
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	before := gitStateRun_(t, ws, "config", "--get", "remote.origin.url")
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true, Fetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if child := gitStateReport(t, res, "sub"); child.Status != GitRepoSkipped || child.Reason != "no checkout at the gitlink path" {
		t.Fatalf("child = %+v", child)
	}
	if got := gitStateRun_(t, ws, "config", "--get", "remote.origin.url"); got != before {
		t.Fatalf("the parent's origin moved from %s to %s", before, got)
	}
	if len(res.Repos) != 2 {
		t.Fatalf("repos = %d, want the root and the child once each", len(res.Repos))
	}
}

// A root already at its tip (an earlier realign, or a reset by hand) with
// other local edits still gets its stale .gitmodules restored.
func TestPeerGitRealign_RestoresStaleGitmodulesAtTip(t *testing.T) {
	ws, _, _, _, p1, _ := urlMoveFixture(t)
	gitStateRun_(t, ws, "reset", "-q", "--mixed", p1)
	gitStateRewriteTracked(t, filepath.Join(ws, "readme.md"), "wip\n")
	res, err := PeerGitRealign(context.Background(), ws, []string{"."}, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	// Restricted to the root: .gitmodules is restored, the child's origin is
	// the child's to change.
	if root := gitStateReport(t, res, "."); root.Class != GitClassAtTip || root.Gitmodules != "restored; submodule sync skipped for sub (not named)" {
		t.Fatalf("root = %+v", root)
	}
	if out := gitStateRun_(t, ws, "status", "--porcelain", "--", ".gitmodules"); out != "" {
		t.Fatalf(".gitmodules still differs: %q", out)
	}
}

// submodule sync follows the child's own rules: a locked child's origin is
// left alone and reported, and a rewritten origin gets an undo command.
func TestPeerGitRealign_SubmoduleSyncRespectsTheChild(t *testing.T) {
	ws, sub, oldURL, _, _, _ := urlMoveFixture(t)
	gitStateRun_(t, sub, "remote", "set-url", "origin", oldURL)
	lock := filepath.Join(gitStateRun_(t, sub, "rev-parse", "--absolute-git-dir"), "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PeerGitRealign(context.Background(), ws, nil, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if root := gitStateReport(t, res, "."); !strings.Contains(root.Gitmodules, "submodule sync skipped for sub (index.lock present)") {
		t.Fatalf("root = %+v", root)
	}
	if got := gitStateRun_(t, sub, "remote", "get-url", "origin"); got != oldURL {
		t.Fatalf("a locked child's origin was rewritten to %s", got)
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	gitStateRun_(t, ws, "checkout", "-q", "--", ".")
	ws2, sub2, oldURL2, _, _, _ := urlMoveFixture(t)
	gitStateRun_(t, sub2, "remote", "set-url", "origin", oldURL2)
	res, err = PeerGitRealign(context.Background(), ws2, nil, RealignOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if root := gitStateReport(t, res, "."); !strings.Contains(root.URLUndo, "remote set-url origin "+shellWord(oldURL2)) {
		t.Fatalf("no undo for the rewritten origin: %+v", root)
	}
}
