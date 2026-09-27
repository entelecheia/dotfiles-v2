package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanWorkspaceNameTrim_DeepestFirstAndApply(t *testing.T) {
	root := t.TempDir()
	writeNFDTestFile(t, root, "notes /inner.txt ", "payload")
	writeNFDTestFile(t, root, "report.md ", "payload")

	plan, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Renames) != 3 {
		t.Fatalf("got %d renames, want file + parent dir + root file: %#v", len(plan.Renames), plan.Renames)
	}
	// Deepest first: the file inside the trailing-space dir moves before the dir.
	if plan.Renames[0].OldRel != "notes /inner.txt " {
		t.Fatalf("deepest rename first = %#v", plan.Renames)
	}
	if plan.Renames[0].NewRel != "notes/inner.txt" {
		t.Errorf("child target = %q, want %q", plan.Renames[0].NewRel, "notes/inner.txt")
	}

	result, err := TrimWorkspaceNames(nfdTestConfig(t, root), false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Applied != 3 {
		t.Fatalf("applied = %d, want 3", result.Applied)
	}
	for _, rel := range []string{"notes/inner.txt", "report.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("trimmed path %s missing: %v", rel, err)
		}
	}
	for _, stale := range []string{"notes ", "report.md "} {
		if hasRawNFDTestEntry(t, root, stale) {
			t.Errorf("old spelling %q still exists", stale)
		}
	}
	// A trim is idempotent hygiene, not an opt-in migration: no marker.
	if result.MarkerPath != "" {
		t.Errorf("trim wrote a marker path %q", result.MarkerPath)
	}
	if NFDMigrationMarked(root) {
		t.Error("trim wrote an NFD migration marker")
	}
}

func TestTrimWorkspaceNames_DryRun(t *testing.T) {
	root := t.TempDir()
	old := writeNFDTestFile(t, root, "report.md ", "payload")

	result, err := TrimWorkspaceNames(nfdTestConfig(t, root), true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !result.DryRun || len(result.Plan.Renames) != 1 {
		t.Fatalf("dry-run result = %#v", result)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("dry-run changed source: %v", err)
	}
}

func TestPlanWorkspaceNameTrim_CollisionsArePreflighted(t *testing.T) {
	t.Run("trimmed name meets untouched sibling", func(t *testing.T) {
		root := t.TempDir()
		first := writeNFDTestFile(t, root, "foo.md ", "padded")
		second := writeNFDTestFile(t, root, "foo.md", "plain")

		_, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
		preflight, ok := err.(*NameTrimPreflightError)
		if !ok {
			t.Fatalf("error = %T %v, want *NameTrimPreflightError", err, err)
		}
		if len(preflight.Collisions) != 1 {
			t.Fatalf("collisions = %#v, want one", preflight.Collisions)
		}
		for _, path := range []string{first, second} {
			if _, statErr := os.Stat(path); statErr != nil {
				t.Errorf("preflight changed %s: %v", path, statErr)
			}
		}
	})

	t.Run("two padded siblings trim to one name", func(t *testing.T) {
		root := t.TempDir()
		writeNFDTestFile(t, root, "foo.md ", "space")
		writeNFDTestFile(t, root, "foo.md\t", "tab")

		_, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
		preflight, ok := err.(*NameTrimPreflightError)
		if !ok {
			t.Fatalf("error = %T %v, want *NameTrimPreflightError", err, err)
		}
		if len(preflight.Collisions) != 1 {
			t.Fatalf("collisions = %#v, want one", preflight.Collisions)
		}
	})
}

func TestPlanWorkspaceNameTrim_WhitespaceOnlyNameIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, " "), []byte("bad"), 0o644); err != nil {
		t.Skipf("filesystem rejects whitespace-only names: %v", err)
	}

	_, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
	preflight, ok := err.(*NameTrimPreflightError)
	if !ok {
		t.Fatalf("error = %T %v, want *NameTrimPreflightError", err, err)
	}
	if len(preflight.EmptyTargets) != 1 {
		t.Fatalf("empty targets = %#v, want one", preflight.EmptyTargets)
	}
	if !strings.Contains(err.Error(), "whitespace") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// Regression for codex P2 on #144: a whitespace-only name OUTSIDE the sync
// set is irrelevant to the transfer and must not abort the trim.
func TestPlanWorkspaceNameTrim_FilteredOutWhitespaceOnlyNameIsIgnored(t *testing.T) {
	root := t.TempDir()
	writeNFDTestFile(t, root, "node_modules/ ", "bad")
	writeNFDTestFile(t, root, "report.md ", "payload")

	plan, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Renames) != 1 || plan.Renames[0].OldRel != "report.md " {
		t.Fatalf("renames = %#v, want only report.md", plan.Renames)
	}
}

func TestPlanWorkspaceNameTrim_ExcludedTreesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeNFDTestFile(t, root, ".dotfiles/state .json", "protected")
	writeNFDTestFile(t, root, ".sync-conflicts/2026/backup .md", "protected")
	writeNFDTestFile(t, root, "node_modules/pkg .js", "protected")
	target := writeNFDTestFile(t, root, "outside/target.txt", "target")
	if err := os.Symlink(target, filepath.Join(root, "link .txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	plan, err := PlanWorkspaceNameTrim(nfdTestConfig(t, root))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	for _, rename := range plan.Renames {
		if strings.HasPrefix(rename.OldRel, ".dotfiles/") ||
			strings.HasPrefix(rename.OldRel, ".sync-conflicts/") ||
			strings.HasPrefix(rename.OldRel, "node_modules/") ||
			strings.HasPrefix(rename.OldRel, "link") {
			t.Errorf("excluded/symlink rename planned: %#v", rename)
		}
	}
	if !hasRawNFDTestEntry(t, root, "link .txt") {
		t.Error("symlink entry changed during planning")
	}
}

func TestTrimWorkspaceNames_NilConfig(t *testing.T) {
	if _, err := PlanWorkspaceNameTrim(nil); err == nil {
		t.Fatal("nil config plan unexpectedly succeeded")
	}
	if _, err := TrimWorkspaceNames(nil, false); err == nil {
		t.Fatal("nil config trim unexpectedly succeeded")
	}
}

func TestNameTrimPreflightError_EmptyMessage(t *testing.T) {
	var err *NameTrimPreflightError
	if err.Error() == "" {
		t.Error("nil preflight error rendered an empty message")
	}
	if (&NameTrimPreflightError{}).Error() == "" {
		t.Error("empty preflight error rendered an empty message")
	}
}
