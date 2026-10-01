package syncer

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
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
	names := []string{"notes/[x] a.md", "notes/a*b.md", "notes/what?.md", `notes/back\slash[1].md`}
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
		t.Fatalf("Deletes = %v, Conflicts = %+v; want the four names deleted", plan.Deletes, plan.Conflicts)
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
