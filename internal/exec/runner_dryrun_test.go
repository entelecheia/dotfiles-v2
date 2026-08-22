package exec

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// runnerMethods classifies every method on *Runner as mutating (true) or
// read-only (false). The judgement lives here because no naming heuristic can
// make it: RunQuery carries the Run prefix and is deliberately read-only, and
// it is the one method that executes a subprocess even under DryRun.
//
// The probe-runner constructor is a package function, not a method, so
// reflection never reports it and an entry for it would trip the stale-entry
// check below.
var runnerMethods = map[string]bool{
	// Mutating: each must have a dry-run branch and an exercise below.
	"Run":             true,
	"RunAttached":     true,
	"RunInteractive":  true,
	"RunShell":        true,
	"WriteFile":       true,
	"WriteFileAtomic": true,
	"MkdirAll":        true,
	"Symlink":         true,
	"Remove":          true,
	"RemoveAll":       true,

	// Read-only: never dry-run gated, because reads are always real.
	"RunQuery":      false,
	"CommandExists": false,
	"FileExists":    false,
	"IsDir":         false,
	"ReadFile":      false,
	"Readlink":      false,
	"IsSymlink":     false,
}

// TestRunner_MethodSetMatchesTable keeps the classification table honest
// against the live type in both directions. Without the forward check a new
// Ensure*/Sync*/Copy* method could ship with no dry-run branch and no test
// would notice; without the reverse check a deleted method would leave a stale
// entry that makes the table look more complete than it is.
//
// Both loops use t.Errorf, never t.Fatalf: Go randomizes map iteration order,
// so failing fast would report an arbitrary one of several problems and the
// guard's output would vary run to run.
func TestRunner_MethodSetMatchesTable(t *testing.T) {
	rt := reflect.TypeOf(&Runner{})
	live := map[string]bool{}
	for i := 0; i < rt.NumMethod(); i++ {
		live[rt.Method(i).Name] = true
	}

	if len(live) == 0 {
		t.Fatal("reflection reported no methods on *Runner; the guard would pass vacuously")
	}

	for name := range live {
		if _, ok := runnerMethods[name]; !ok {
			t.Errorf("unclassified exec.Runner method %q: add it to runnerMethods as mutating or read-only, and if mutating give it a dry-run branch and an exercise in TestRunner_MutatingAreNoOpsUnderDryRun", name)
		}
	}
	for name := range runnerMethods {
		if !live[name] {
			t.Errorf("classification table lists %q, which is no longer a method on *Runner", name)
		}
	}

	if len(runnerMethods) != len(live) {
		t.Errorf("table has %d entries, live method set has %d", len(runnerMethods), len(live))
	}
}

// TestRunner_MutatingAreNoOpsUnderDryRun is the assertion behind the tool's
// central promise: with DryRun set, nothing on disk changes. It checks three
// things, and the first is the one that is easy to forget - a mutating method
// with no exercise here would let the tree comparison pass while never calling
// the method at all.
func TestRunner_MutatingAreNoOpsUnderDryRun(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	p := func(n string) string { return filepath.Join(dir, n) }

	calls := map[string]func() error{
		"Run":             func() error { _, err := r.Run(ctx, "touch", p("a")); return err },
		"RunAttached":     func() error { return r.RunAttached(ctx, "touch", p("b")) },
		"RunInteractive":  func() error { return r.RunInteractive(ctx, "touch", p("c")) },
		"RunShell":        func() error { _, err := r.RunShell(ctx, "touch "+p("d")); return err },
		"WriteFile":       func() error { return r.WriteFile(p("e"), []byte("x"), 0o644) },
		"WriteFileAtomic": func() error { return r.WriteFileAtomic(p("f"), []byte("x"), 0o644) },
		"MkdirAll":        func() error { return r.MkdirAll(p("g"), 0o755) },
		"Symlink":         func() error { return r.Symlink(p("e"), p("h")) },
		"Remove":          func() error { return r.Remove(dir) },
		"RemoveAll":       func() error { return r.RemoveAll(dir) },
	}

	// Fail-closed edge: an empty or partial exercise map must not pass.
	for name, mutating := range runnerMethods {
		if !mutating {
			continue
		}
		if _, ok := calls[name]; !ok {
			t.Errorf("mutating method %q has no dry-run exercise in this test", name)
		}
	}

	// Seed one regular file and one directory so the snapshot has content to
	// protect; Remove and RemoveAll target dir itself.
	if err := os.WriteFile(p("seed.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p("seeddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p("seed.txt"), p("seedlink")); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, dir)
	for name, fn := range calls {
		if err := fn(); err != nil {
			t.Errorf("%s under dry-run returned error: %v", name, err)
		}
	}
	if after := snapshotTree(t, dir); before != after {
		t.Errorf("dry-run mutated the tree:\nbefore=%s\nafter=%s", before, after)
	}
}

// snapshotTree captures name + mode for every entry under root, plus a content
// hash for regular files. DirEntry.Info() has Lstat semantics, so a symlink is
// recorded as a symlink rather than followed - which is the whole point for a
// tool that writes them. ModTime and Size are deliberately excluded: size is
// implied by the hash and mtime is the classic flaky-snapshot source.
//
// Plan 01-03 keeps a character-identical copy in package cli. The duplication
// is deliberate: the two packages share no internal test-helper package, and
// one pattern in two places reads better than a new package for 25 lines.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := rel + "\t" + info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line += "\t" + fmt.Sprintf("%x", sha256.Sum256(b))
		}
		// A symlink's payload is its target, just as a regular file's payload is
		// its bytes. Mode().String() carries the L type bit but not where the link
		// points, so without this a retargeted symlink compares as unchanged --
		// the blind spot that matters most for a tool whose main artifact is a
		// symlink. WalkDir's DirEntry is lstat-based, so this never follows.
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			line += "\t-> " + target
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines) // WalkDir is lexical per directory; sort makes it total
	return strings.Join(lines, "\n")
}

// TestSnapshotTree_CatchesSymlinkRetarget keeps snapshotTree honest about the
// one artifact this tool primarily creates.
//
// The register recorded symlinks as covered (T-01-18, T-01-30) while every
// snapshot implementation stored a link's path, type and mode but not its
// target, so a retargeted symlink compared as identical. That was fixed, but
// the fix was proven once by hand -- and a one-time proof does not survive a
// refactor. This is the standing version.
//
// Both trees hold byte-identical regular files, so the ONLY difference is
// where the link points. Before the fix this assertion failed.
func TestSnapshotTree_CatchesSymlinkRetarget(t *testing.T) {
	build := func(target string) string {
		dir := t.TempDir()
		for _, name := range []string{"real-one", "real-two"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("same bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	before := snapshotTree(t, build("real-one"))
	after := snapshotTree(t, build("real-two"))

	if before == after {
		t.Errorf("snapshotTree reported two trees as identical when their symlink points elsewhere;\n"+
			"a retargeted symlink must be visible (T-01-18, T-01-30)\nsnapshot:\n%s", before)
	}
	if !strings.Contains(before, "-> real-one") || !strings.Contains(after, "-> real-two") {
		t.Errorf("snapshotTree did not record link targets;\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
