package syncer

import (
	"context"
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
