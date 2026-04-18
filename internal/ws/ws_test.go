package ws

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

func TestValidateRelPath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		wantOut string
	}{
		{"empty", "", true, ""},
		{"dot", ".", true, ""},
		{"slash", "/foo", true, ""},
		{"absolute", "/etc/passwd", true, ""},
		{"traversal", "..", true, ""},
		{"traversal-prefix", "../escape", true, ""},
		{"hidden-traversal", "foo/../../bar", true, ""},
		{"simple", "foo", false, "foo"},
		{"nested", "a/b/c", false, "a/b/c"},
		{"with-dot", "./foo", false, "foo"},
		{"with-slash-end", "foo/", false, "foo"},
		{"double-slash", "a//b", false, "a/b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateRelPath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.wantOut {
				t.Fatalf("want %q, got %q", tc.wantOut, got)
			}
		})
	}
}

func setupRoots(t *testing.T) (Roots, *exec.Runner) {
	t.Helper()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	gdrive := filepath.Join(tmp, "gdrive")
	if err := os.MkdirAll(work, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gdrive, 0755); err != nil {
		t.Fatal(err)
	}
	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return Roots{Work: work, Gdrive: gdrive}, runner
}

func TestMkdirBothSides(t *testing.T) {
	roots, runner := setupRoots(t)

	msgs, err := Mkdir(runner, roots, "projects/foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 msgs, got %d: %v", len(msgs), msgs)
	}
	wa, ga := roots.ResolvePair("projects/foo")
	if !fileutil.IsDir(wa) {
		t.Errorf("work side missing: %s", wa)
	}
	if !fileutil.IsDir(ga) {
		t.Errorf("gdrive side missing: %s", ga)
	}

	// Idempotent second call
	if _, err := Mkdir(runner, roots, "projects/foo"); err != nil {
		t.Fatalf("second mkdir should be idempotent, got: %v", err)
	}
}

func TestMkdirOnlyMissingSide(t *testing.T) {
	roots, runner := setupRoots(t)
	// Pre-create on work side only
	if err := os.MkdirAll(filepath.Join(roots.Work, "existing"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Mkdir(runner, roots, "existing"); err != nil {
		t.Fatal(err)
	}
	if !fileutil.IsDir(filepath.Join(roots.Gdrive, "existing")) {
		t.Errorf("gdrive side should have been created")
	}
}

func TestMoveBothSides(t *testing.T) {
	roots, runner := setupRoots(t)
	if _, err := Mkdir(runner, roots, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := Move(context.Background(), runner, roots, "old", "new"); err != nil {
		t.Fatal(err)
	}
	wa, ga := roots.ResolvePair("new")
	if !fileutil.IsDir(wa) || !fileutil.IsDir(ga) {
		t.Errorf("new not on both sides")
	}
	oldWa, oldGa := roots.ResolvePair("old")
	if fileutil.IsDir(oldWa) || fileutil.IsDir(oldGa) {
		t.Errorf("old should be gone")
	}
}

func TestMoveFailsWhenDstExists(t *testing.T) {
	roots, runner := setupRoots(t)
	if _, err := Mkdir(runner, roots, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Mkdir(runner, roots, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := Move(context.Background(), runner, roots, "a", "b"); err == nil {
		t.Fatal("expected error when dst exists")
	}
}

func TestRemoveEmpty(t *testing.T) {
	roots, runner := setupRoots(t)
	if _, err := Mkdir(runner, roots, "empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(context.Background(), runner, roots, "empty", false); err != nil {
		t.Fatal(err)
	}
	wa, ga := roots.ResolvePair("empty")
	if fileutil.IsDir(wa) || fileutil.IsDir(ga) {
		t.Errorf("not removed")
	}
}

func TestRemoveNonEmptyFailsWithoutRecursive(t *testing.T) {
	roots, runner := setupRoots(t)
	if _, err := Mkdir(runner, roots, "full"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(roots.Work, "full", "x.txt"))
	_ = f.Close()
	if _, err := Remove(context.Background(), runner, roots, "full", false); err == nil {
		t.Fatal("expected error for non-empty without --recursive")
	}
}

func TestAuditDetectsDrift(t *testing.T) {
	roots, runner := setupRoots(t)
	if _, err := Mkdir(runner, roots, "both"); err != nil {
		t.Fatal(err)
	}
	// Create drift: workspace-only and gdrive-only
	if err := os.MkdirAll(filepath.Join(roots.Work, "work-only"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(roots.Gdrive, "gdrive-only"), 0755); err != nil {
		t.Fatal(err)
	}

	mismatches, err := Audit(roots, AuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 2 {
		t.Fatalf("want 2 mismatches, got %d: %v", len(mismatches), mismatches)
	}
	// Verify sides
	found := map[string]Side{}
	for _, m := range mismatches {
		found[m.RelPath] = m.OnlyOn
	}
	if found["work-only"] != SideWork {
		t.Errorf("work-only should be SideWork, got %v", found["work-only"])
	}
	if found["gdrive-only"] != SideGdrive {
		t.Errorf("gdrive-only should be SideGdrive, got %v", found["gdrive-only"])
	}
}

func TestAuditSkipsIgnoreDirs(t *testing.T) {
	roots, _ := setupRoots(t)
	if err := os.MkdirAll(filepath.Join(roots.Work, "node_modules/foo"), 0755); err != nil {
		t.Fatal(err)
	}
	mismatches, err := Audit(roots, AuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("node_modules should be ignored, got: %v", mismatches)
	}
}

func TestAuditSkipsSymlinks(t *testing.T) {
	roots, _ := setupRoots(t)
	// Symlink in work side only — should NOT be flagged as mismatch
	target := filepath.Join(roots.Gdrive, "real-target")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(roots.Work, "linky")); err != nil {
		t.Fatal(err)
	}
	mismatches, err := Audit(roots, AuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mismatches {
		if m.RelPath == "linky" {
			t.Fatalf("symlink should not be flagged: %v", m)
		}
	}
}

func TestAuditScopeLimitsSearch(t *testing.T) {
	roots, _ := setupRoots(t)
	// Drift in two scopes
	os.MkdirAll(filepath.Join(roots.Work, "a/only"), 0755)
	os.MkdirAll(filepath.Join(roots.Work, "b/only"), 0755)
	os.MkdirAll(filepath.Join(roots.Gdrive, "a"), 0755)
	os.MkdirAll(filepath.Join(roots.Gdrive, "b"), 0755)

	mismatches, err := Audit(roots, AuditOptions{Scope: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 || mismatches[0].RelPath != "a/only" {
		t.Fatalf("scope should limit to a/, got: %v", mismatches)
	}
}
