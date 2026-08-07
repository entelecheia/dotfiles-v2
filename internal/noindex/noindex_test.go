package noindex

import (
	"os"
	"path/filepath"
	"testing"
)

// tree builds a directory layout under a temp root. The root is resolved
// because Sweep resolves its walk roots, and on macOS t.TempDir() hands back a
// /var path that is really /private/var.
func tree(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func marked(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, Marker))
	return err == nil
}

func TestSweepMarksAndPrunes(t *testing.T) {
	root := tree(t,
		"proj/node_modules/left-pad/node_modules",
		"proj/.venv/lib",
		"proj/src",
		"proj/.git/objects",
	)

	res := Sweep(Options{WalkRoots: []string{root}})

	if !marked(t, filepath.Join(root, "proj/node_modules")) {
		t.Error("node_modules not marked")
	}
	if !marked(t, filepath.Join(root, "proj/.venv")) {
		t.Error(".venv not marked")
	}
	if marked(t, filepath.Join(root, "proj/src")) {
		t.Error("plain source dir should not be marked")
	}
	// A matched directory is pruned, so nested copies never get their own
	// marker. The parent's marker already covers the whole subtree.
	if marked(t, filepath.Join(root, "proj/node_modules/left-pad/node_modules")) {
		t.Error("walk descended into a matched directory")
	}
	if len(res.Marked) != 2 {
		t.Errorf("Marked = %v, want 2 entries", res.Marked)
	}
}

func TestSweepIsIdempotent(t *testing.T) {
	root := tree(t, "proj/node_modules")

	Sweep(Options{WalkRoots: []string{root}})
	res := Sweep(Options{WalkRoots: []string{root}})

	if len(res.Marked) != 0 {
		t.Errorf("second sweep marked %v, want nothing", res.Marked)
	}
	if res.Present != 1 {
		t.Errorf("Present = %d, want 1", res.Present)
	}
}

func TestSweepDryRunWritesNothing(t *testing.T) {
	root := tree(t, "proj/node_modules")

	res := Sweep(Options{WalkRoots: []string{root}, DryRun: true})

	if len(res.Marked) != 1 {
		t.Fatalf("Marked = %v, want 1 entry", res.Marked)
	}
	if marked(t, filepath.Join(root, "proj/node_modules")) {
		t.Error("dry run created a marker")
	}
}

func TestSweepSkipsGitAndSymlinks(t *testing.T) {
	root := tree(t, "proj/.git/node_modules", "elsewhere/node_modules", "proj/src")
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "proj/link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	Sweep(Options{WalkRoots: []string{filepath.Join(root, "proj")}})

	if marked(t, filepath.Join(root, "proj/.git/node_modules")) {
		t.Error("walked into .git")
	}
	if marked(t, filepath.Join(root, "elsewhere/node_modules")) {
		t.Error("followed a symlink out of the root")
	}
}

// env/ is too common a directory name to mark on sight; clean.Pattern's
// NeedProbe makes it conditional on pyvenv.cfg, and that carries over here.
func TestSweepProbesEnvDir(t *testing.T) {
	root := tree(t, "plain/env/data", "python/env")
	if err := os.WriteFile(filepath.Join(root, "python/env/pyvenv.cfg"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	Sweep(Options{WalkRoots: []string{root}})

	if marked(t, filepath.Join(root, "plain/env")) {
		t.Error("plain env/ marked without pyvenv.cfg")
	}
	if !marked(t, filepath.Join(root, "python/env")) {
		t.Error("real virtualenv not marked")
	}
}

// Deliverables land in build/ and out/ (rendered decks, exports), so those stay
// searchable even though `dot clean` treats them as junk.
func TestSweepLeavesDeliverableDirsIndexed(t *testing.T) {
	root := tree(t, "deck/build", "deck/out", "deck/dist", "deck/target")

	Sweep(Options{WalkRoots: []string{root}})

	for _, name := range []string{"build", "out"} {
		if marked(t, filepath.Join(root, "deck", name)) {
			t.Errorf("%s/ marked; deliverables must stay searchable", name)
		}
	}
	for _, name := range []string{"dist", "target"} {
		if !marked(t, filepath.Join(root, "deck", name)) {
			t.Errorf("%s/ not marked", name)
		}
	}
}

func TestCacheRootsAreStampedNotWalked(t *testing.T) {
	root := tree(t, "cache/deep/node_modules")

	res := Sweep(Options{CacheRoots: []string{filepath.Join(root, "cache")}})

	if !marked(t, filepath.Join(root, "cache")) {
		t.Error("cache root not marked")
	}
	if marked(t, filepath.Join(root, "cache/deep/node_modules")) {
		t.Error("cache root was walked; one marker at the top is the point")
	}
	if len(res.Marked) != 1 {
		t.Errorf("Marked = %v, want 1 entry", res.Marked)
	}
}

// `dot apply` can create ~/workspace as a symlink, and WalkDir lstats its root:
// without resolving it first the entire default sweep silently does nothing.
func TestSweepFollowsSymlinkedRoot(t *testing.T) {
	root := tree(t, "real/proj/node_modules", "link-parent")
	link := filepath.Join(root, "link-parent/workspace")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res := Sweep(Options{WalkRoots: []string{link}})

	if !marked(t, filepath.Join(root, "real/proj/node_modules")) {
		t.Error("symlinked root was not walked")
	}
	if len(res.Marked) != 1 {
		t.Errorf("Marked = %v, want 1 entry", res.Marked)
	}
}

func TestSweepReportsUnwritableDirs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := tree(t, "proj/node_modules")
	locked := filepath.Join(root, "proj/node_modules")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	res := Sweep(Options{WalkRoots: []string{root}})

	if len(res.Marked) != 0 {
		t.Errorf("Marked = %v, want nothing", res.Marked)
	}
	if len(res.Failed) != 1 || res.Failed[0] != locked {
		t.Errorf("Failed = %v, want [%s]", res.Failed, locked)
	}
}

func TestDefaultRootsDropMissingPaths(t *testing.T) {
	home := tree(t, "workspace", ".local")

	walk := DefaultWalkRoots(home)
	if len(walk) != 1 || walk[0] != filepath.Join(home, "workspace") {
		t.Errorf("DefaultWalkRoots = %v, want just workspace", walk)
	}

	cache := DefaultCacheRoots(home)
	if len(cache) != 1 || cache[0] != filepath.Join(home, ".local") {
		t.Errorf("DefaultCacheRoots = %v, want just .local", cache)
	}
	for _, c := range cache {
		if filepath.Base(c) == ".claude" {
			t.Error(".claude must stay searchable")
		}
	}
}
