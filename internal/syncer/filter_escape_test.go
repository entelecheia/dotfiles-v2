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
	// git reads *, ?, [ and \ in a pathspec as globs; a name that stops
	// matching must fail the test, not skip it.
	if out, err := osexec.Command("git", append([]string{"-C", f.local, "--literal-pathspecs", "add", "--"}, names...)...).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
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
	f.cfg.SharedExcludes = []string{"team [ops]", "x*y", "what?", `back\slash`}
	f.writeLocal("team [ops]/w.pdf", "workspace")
	f.writeMirror("team [ops]/m.pdf", "theirs")
	f.writeLocal("team o/x.pdf", "ours")
	f.writeLocal("x*y/in.pdf", "shared")
	f.writeLocal("xZy/in.pdf", "ours")
	f.writeLocal("what?/in.pdf", "shared")
	f.writeLocal("whatX/in.pdf", "ours")
	f.writeLocal(`back\slash/in.pdf`, "shared")
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
	for _, rel := range []string{"team o/x.pdf", "xZy/in.pdf", "whatX/in.pdf"} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); err != nil {
			t.Errorf("%s, which a wildcard would match, was excluded: %v", rel, err)
		}
	}
	for _, rel := range []string{"x*y/in.pdf", "what?/in.pdf", `back\slash/in.pdf`} {
		if _, err := os.Stat(filepath.Join(f.mirror, rel)); !os.IsNotExist(err) {
			t.Errorf("the push wrote %s into a shared folder (err=%v)", rel, err)
		}
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

// Real rsync, the whole filter chain: the push writes exactly the plan's
// creates and moves nothing. A shared folder stays out even where an allow
// re-include lets rsync descend into it, through a parent dir (team/ops) or
// the folder itself (lab), and only the allowed file goes in (#228).
func TestPush_PlanMatchesRsyncThroughTheFilterChain(t *testing.T) {
	requireRsync(t)
	f := newIntakeFixture(t)
	f.cfg.Propagation.Delete = true
	f.cfg.SharedExcludes = []string{"team/ops", "lab", "team [x]"}
	f.cfg.AllowPatterns = []string{"/team/ops/.env", "/lab/"}
	for _, rel := range []string{
		"team/ops/.env", "team/ops/w.pdf", "team/ops/sub/d.pdf",
		"lab/w.pdf", "team [x]/w.pdf", "team x/k.pdf",
		"[x] a.md", "a*b.md", "what?.md", `back\slash.md`, "keep.md",
	} {
		f.writeLocal(rel, "ours")
	}
	for _, rel := range []string{"team/ops/m.pdf", "lab/m.pdf", "team [x]/m.pdf"} {
		f.writeMirror(rel, "theirs")
	}
	mirrorFiles := func() []string {
		var out []string
		_ = filepath.WalkDir(f.mirror, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(f.mirror, path)
				out = append(out, filepath.ToSlash(rel))
			}
			return err
		})
		slices.Sort(out)
		return out
	}
	plan, err := PlanPush(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Updates)+len(plan.Deletes)+len(plan.Conflicts) > 0 {
		t.Errorf("the plan touches the mirror's own files: Updates=%v Deletes=%v Conflicts=%v", plan.Updates, plan.Deletes, plan.Conflicts)
	}
	before := mirrorFiles()
	if err := Push(context.Background(), f.runner, f.cfg, false); err != nil {
		t.Fatalf("Push: %v", err)
	}
	want := slices.Sorted(slices.Values(append(slices.Clone(before), plan.Creates...)))
	if got := mirrorFiles(); !slices.Equal(got, want) {
		t.Errorf("mirror after push = %v\nwant mirror before + plan creates = %v", got, want)
	}
	if !slices.Contains(plan.Creates, "team/ops/.env") || slices.Contains(plan.Creates, "team/ops/w.pdf") || slices.Contains(plan.Creates, "lab/w.pdf") {
		t.Errorf("Creates = %v, want team/ops/.env and nothing else under a shared entry", plan.Creates)
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
	if out, err := osexec.Command("git", "-C", f.local, "--literal-pathspecs", "add", "--", "notes/a\nb.md", "notes/ok.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
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
	res, err := Fetch(context.Background(), f.runner, f.cfg, []string{"notes//[x] a.pdf", "notes/a*b.pdf", "notes/what?.pdf", `back\slash`, "./d[1]"}, false)
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
	// A path outside the workspace is refused, and the result still names
	// what the run had resolved before it.
	res, err = Fetch(context.Background(), f.runner, f.cfg, []string{"notes/gone.pdf", "../outside"}, false)
	if err == nil || !strings.Contains(err.Error(), `"../outside" is not a path below the workspace root`) || res == nil || !slices.Equal(res.Missing, []string{"notes/gone.pdf"}) {
		t.Errorf("Fetch outside the workspace = %+v, %v", res, err)
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

// A shared entry has one form everywhere: add, remove, count, list and both
// filter sides all use the cleaned path, so a hand-edited "team//ops" is
// listed, counted and removed as "team/ops", and an entry that cannot be one
// filter line is refused when it is added (#228).
func TestSharedEntriesHaveOneFormEverywhere(t *testing.T) {
	f := newIntakeFixture(t)
	if err := SaveLocalConfig(f.cfg.LocalPaths, &LocalConfig{
		Propagation:    DefaultPropagationPolicy(),
		SharedExcludes: []string{"team//ops", "./team/ops"},
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := ScanShared(f.mirror, []string{"team//ops", "./team/ops"})
	if len(entries) != 1 || entries[0].RelPath != "team/ops" {
		t.Fatalf("ScanShared = %+v, want team/ops", entries)
	}
	added, err := SharedAdd(f.cfg, []string{"team/ops", ".//x"})
	if err != nil || !slices.Equal(added, []string{"x"}) {
		t.Fatalf("SharedAdd = %v, %v; want only x added", added, err)
	}
	if n, _ := SharedCount(f.cfg); n != 2 {
		t.Errorf("SharedCount = %d, want 2", n)
	}
	removed, err := SharedRemove(f.cfg, []string{"team/ops"})
	if err != nil || !slices.Equal(removed, []string{"team/ops"}) {
		t.Fatalf("SharedRemove = %v, %v", removed, err)
	}
	if stored, _, _ := LoadLocalConfig(f.cfg.LocalPaths); !slices.Equal(stored.SharedExcludes, []string{"x"}) {
		t.Errorf("stored entries = %q, want [x]", stored.SharedExcludes)
	}
	if _, err := SharedAdd(f.cfg, []string{"team\nops"}); err == nil || !strings.Contains(err.Error(), "cannot be a shared exclude") {
		t.Errorf("SharedAdd with a line separator = %v, want a refusal", err)
	}
}

// A stored entry the cleaning drops (an absolute path, one outside the tree)
// is listed, counted, removed by its stored text and cleared, so the CLI can
// see and remove every stored entry (#231).
func TestSharedEntriesTheCleaningDropsStayVisible(t *testing.T) {
	f := newIntakeFixture(t)
	if err := SaveLocalConfig(f.cfg.LocalPaths, &LocalConfig{
		Propagation:    DefaultPropagationPolicy(),
		SharedExcludes: []string{"team/ops", "/team/ops", " ../x ", "  "},
	}); err != nil {
		t.Fatal(err)
	}
	if dropped := DroppedSharedEntries([]string{"team/ops", "/team/ops", " ../x ", "../x", "  "}); !slices.Equal(dropped, []string{"", "../x", "/team/ops"}) {
		t.Errorf("DroppedSharedEntries = %q", dropped)
	}
	if n, _ := SharedCount(f.cfg); n != 4 {
		t.Errorf("SharedCount = %d, want 4 (team/ops and three dropped entries)", n)
	}
	removed, err := SharedRemove(f.cfg, []string{"/team/ops"})
	if err != nil || !slices.Equal(removed, []string{"/team/ops"}) {
		t.Fatalf("SharedRemove(/team/ops) = %v, %v", removed, err)
	}
	if stored, _, _ := LoadLocalConfig(f.cfg.LocalPaths); !slices.Equal(stored.SharedExcludes, []string{"team/ops", " ../x ", "  "}) {
		t.Errorf("stored entries = %q", stored.SharedExcludes)
	}
	if err := SharedClear(f.cfg); err != nil {
		t.Fatal(err)
	}
	if n, _ := SharedCount(f.cfg); n != 0 {
		t.Errorf("SharedCount after clear = %d", n)
	}
}
