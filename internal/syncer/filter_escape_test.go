package syncer

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tracked include and submodule exclude layers escape rsync wildcards, and
// the include layer leaves out a name that cannot be one filter line (#228).
func TestMaterializedFilterLayersEscapeWildcards(t *testing.T) {
	dir := t.TempDir()
	path, err := MaterializeTrackedIncludesFile(dir, []string{"notes/[x] a.md", "plain.md", "bad\nname.md"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, want := range []string{"\n/notes/\\[x] a.md\n", "\n/plain.md\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("tracked includes lack %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "bad") {
		t.Errorf("a name with a line separator reached the include layer:\n%s", body)
	}

	path, err = MaterializeSubmodulesDynFile(dir, []string{"vendor/[lib]"})
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); !strings.Contains(string(body), "\n/vendor/\\[lib]/\n") {
		t.Errorf("submodule excludes are not escaped:\n%s", body)
	}
	if _, err := MaterializeSubmodulesDynFile(dir, []string{"bad\nsub"}); err == nil {
		t.Error("a submodule path with a line separator was written")
	}
}

// Real rsync, include mode: names only the tracked layer admits, holding each
// rsync wildcard, are uploaded and later deleted exactly as the plan says, and
// a name a wildcard would also have matched is not uploaded (#228).
func TestPush_TrackedNamesWithRsyncWildcardsMatchOnlyThemselves(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.FilterMode = FilterModeInclude
	f.cfg.IncludePatterns = []string{"*.pdf"}
	f.cfg.Propagation.Delete = true
	if err := osexec.Command("git", "-C", f.local, "init", "-q").Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	names := []string{"notes/[x] a.md", "notes/a*b.md", "notes/what?.md", `notes/back\slash[1].md`, `notes/back\slash.md`}
	for _, rel := range names {
		f.writeLocal(rel, rel)
	}
	if err := osexec.Command("git", append([]string{"-C", f.local, "add", "--"}, names...)...).Run(); err != nil {
		t.Skipf("git add unavailable: %v", err)
	}
	// Untracked and not a payload extension: a wildcard in an unescaped line
	// ("a*b.md", "what?.md") would admit it.
	f.writeLocal("notes/aXb.md", "untracked")
	f.writeLocal("notes/whatX.md", "untracked")

	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	for _, rel := range names {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); err != nil {
			t.Errorf("%s was not uploaded: %v", rel, err)
		}
	}
	for _, rel := range []string{"notes/aXb.md", "notes/whatX.md"} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was uploaded through a wildcard (err=%v)", rel, err)
		}
	}

	for _, rel := range names {
		if err := os.Remove(filepath.Join(f.local, rel)); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Deletes) != len(names) || len(plan.Conflicts) != 0 {
		t.Fatalf("Deletes = %v, Conflicts = %+v; want every name deleted", plan.Deletes, plan.Conflicts)
	}
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("push 2: %v", err)
	}
	if out, _ := osexec.Command("rsync", "--version").Output(); strings.Contains(string(out), "openrsync") {
		t.Skip("openrsync deletes nothing with --backup (#227)")
	}
	for _, rel := range names {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the mirror after the delete push (err=%v)", rel, err)
		}
	}
}

// A shared entry names a literal path: both filter sides exclude exactly it, so
// a push neither writes into "team [ops]/" nor moves its mirror-only content,
// and a folder the wildcard would also match ("team o/") still syncs (#228).
func TestPush_SharedEntryWithRsyncWildcardsIsLiteral(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.cfg.SharedExcludes = []string{"team [ops]", "x*y"}
	f.writeLocal("team [ops]/w.pdf", "workspace")
	f.writeMirror("team [ops]/m.pdf", "theirs")
	f.writeLocal("team o/x.pdf", "ours")
	f.writeLocal("x*y/in.pdf", "shared")
	f.writeLocal("xZy/in.pdf", "ours")
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range append(append(plan.Creates, plan.Deletes...), plan.Updates...) {
		if strings.HasPrefix(rel, "team [ops]/") {
			t.Errorf("the plan lists %s inside a shared folder", rel)
		}
	}
	if !slices.Contains(plan.Creates, "team o/x.pdf") || !slices.Contains(plan.Creates, "xZy/in.pdf") || slices.Contains(plan.Creates, "x*y/in.pdf") {
		t.Errorf("the plan does not treat shared entries literally: Creates = %v", plan.Creates)
	}
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "team [ops]/w.pdf")); !os.IsNotExist(err) {
		t.Errorf("the push wrote into the shared folder (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "team [ops]/m.pdf")); err != nil {
		t.Errorf("the push moved the shared folder's content: %v", err)
	}
	for _, rel := range []string{"team o/x.pdf", "xZy/in.pdf"} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); err != nil {
			t.Errorf("%s, which a wildcard would match, was excluded: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.mirror, "x*y/in.pdf")); !os.IsNotExist(err) {
		t.Errorf("the push wrote into the shared x*y folder (err=%v)", err)
	}
}

// A hand-edited shared entry is cleaned where both sides read it, and one
// that names nothing under the workspace is dropped, so no run fails on it.
func TestPush_HandEditedSharedEntriesAreCleaned(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.SharedExcludes = []string{"team//ops", "/abs", "../outside", "./tools/x"}
	f.writeLocal("team/ops/w.pdf", "shared")
	f.writeLocal("tools/x/t.pdf", "shared")
	f.writeLocal("notes/n.pdf", "ours")
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Creates, "notes/n.pdf") || slices.Contains(plan.Creates, "team/ops/w.pdf") || slices.Contains(plan.Creates, "tools/x/t.pdf") {
		t.Errorf("Creates = %v, want notes/n.pdf and nothing under a shared entry", plan.Creates)
	}
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	for _, rel := range []string{"team/ops/w.pdf", "tools/x/t.pdf"} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was pushed past its shared entry (err=%v)", rel, err)
		}
	}
}

// A tracked name that cannot be one filter line is left out of both sides,
// so the plan never lists a create rsync will not send (#228).
func TestPlanPush_TrackedNameWithALineSeparatorIsNotPlanned(t *testing.T) {
	f := newIntakeFixture(t)
	f.cfg.FilterMode = FilterModeInclude
	f.cfg.IncludePatterns = []string{"*.pdf"}
	if err := osexec.Command("git", "-C", f.local, "init", "-q").Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	f.writeLocal("notes/a\nb.md", "x")
	f.writeLocal("notes/ok.md", "x")
	if err := osexec.Command("git", "-C", f.local, "add", "--", "notes/a\nb.md", "notes/ok.md").Run(); err != nil {
		t.Skipf("git add unavailable: %v", err)
	}
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Creates, []string{"notes/ok.md"}) {
		t.Errorf("Creates = %q, want only notes/ok.md", plan.Creates)
	}
}

// A fetch transfers exactly the requested names, wildcards and backslashes
// included, and nothing a wildcard would also match (#228).
func TestFetch_RequestedNamesWithRsyncWildcardsAreLiteral(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	for _, rel := range []string{"notes/[x] a.pdf", "notes/a*b.pdf", "notes/aXb.pdf", "notes/what?.pdf", "notes/whatX.pdf", `back\slash/in.pdf`, "d[1]/in.pdf", "d1/in.pdf"} {
		f.writeMirror(rel, rel)
	}
	res, err := Fetch(context.Background(), f.runner, f.cfg, []string{"notes/[x] a.pdf", "notes/a*b.pdf", "notes/what?.pdf", `back\slash`, "d[1]"}, false)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("Missing = %v", res.Missing)
	}
	for _, rel := range []string{"notes/[x] a.pdf", "notes/a*b.pdf", "notes/what?.pdf", `back\slash/in.pdf`, "d[1]/in.pdf"} {
		if _, err := os.Stat(filepath.Join(f.local, rel)); err != nil {
			t.Errorf("%s was not fetched: %v", rel, err)
		}
	}
	for _, rel := range []string{"notes/aXb.pdf", "notes/whatX.pdf", "d1/in.pdf"} {
		if _, err := os.Stat(filepath.Join(f.local, rel)); !os.IsNotExist(err) {
			t.Errorf("unrequested %s came through a wildcard (err=%v)", rel, err)
		}
	}
}

// Submodule paths are cleaned where they are read, so both filter sides see
// the same path, and a path outside the tree is dropped instead of failing
// every run.
func TestGitSubmodulePathsCleansHandEditedPaths(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitmodules := "[submodule \"a\"]\n\tpath = vendor//lib\n[submodule \"b\"]\n\tpath = ../outside\n[submodule \"c\"]\n\tpath = ./tools/x\n[submodule \"d\"]\n\tpath = /abs/lib\n"
	if err := os.WriteFile(filepath.Join(root, ".gitmodules"), []byte(gitmodules), 0o644); err != nil {
		t.Fatal(err)
	}
	got := gitSubmodulePaths(root)
	if strings.Join(got, ",") != "tools/x,vendor/lib" {
		t.Fatalf("gitSubmodulePaths = %v, want [tools/x vendor/lib]", got)
	}
	if _, err := MaterializeSubmodulesDynFile(t.TempDir(), got); err != nil {
		t.Fatalf("cleaned submodule paths were refused: %v", err)
	}
}
