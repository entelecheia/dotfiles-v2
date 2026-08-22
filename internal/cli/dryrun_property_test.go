package cli

// GUARD-03, Go half: `dot apply --dry-run` against an empty HOME must leave that
// HOME byte-identical. `--dry-run must remain truthful` is the promise that makes
// a preview safe on a live machine with no undo, and GUARD-02 only covers what
// goes through exec.Runner. This guard covers everything that does not.
//
// Two pre-existing defects make the strict assertion false today. Neither is
// silenced: each is a requirement in .planning/REQUIREMENTS.md routed to Phase 5,
// and knownDryRunDeviations below is fail-closed in BOTH directions - an
// unattributed write fails the test, and an entry that stops occurring also fails
// the test, so Phase 5's fix prunes the table instead of leaving a stale excuse.
//
// The container half, tests/scenarios/dry-run-empty-home.sh, carries the same
// assertion unconditionally in a clean ubuntu:22.04 image where no Homebrew prefix
// exists, so the BUG-06 skip below never blinds CI.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// knownDryRunDeviations records every write a truthful --dry-run should not make
// but currently does, keyed on the FULL snapshot line snapshotTree emits
// ("path\tmode" for a directory, "path\tmode\thash" for a regular file) rather
// than on the path alone. Keying on the path would forgive a mode change or a
// content change on a tolerated path - a regression that made the state write
// emit *different* content would ride through. Full-line keys close that, at the
// cost of one entry per profile for the state file, whose payload names the
// profile.
//
// Values are self-describing from the tracked tree: .planning/ is git-ignored in
// this repo, so a bare "BUG-05" in a committed test file is unresolvable to
// anyone who clones it. Each value therefore carries the requirement ID, the
// file:line, and a one-clause description.
//
// This table is a record, not a suppression. Do not add an entry for a deviation
// that is not attributable to an existing BUG requirement: mint the requirement
// first. A table that grows to cover whatever the guard happens to find has
// stopped being a guard.
var knownDryRunDeviations = map[string]string{
	".config\tdrwxr-xr-x":          "BUG-05 internal/cli/apply.go:112,:116 - apply saves state on both branches of the homeOverride fork before the runner is constructed at :137; saveStateAt (internal/config/state.go:459) has no dry-run awareness - the state dir os.MkdirAll creates",
	".config/dotfiles\tdrwxr-xr-x": "BUG-05 internal/cli/apply.go:112,:116 - apply saves state on both branches of the homeOverride fork before the runner is constructed at :137; saveStateAt (internal/config/state.go:459) has no dry-run awareness - the second level of that same state dir",
	".config/dotfiles/config.yaml\t-rw-r--r--\tf041ab184aa2cf78746a93103afca250014ef42a51aa75a7355729993e0b3ca4": "BUG-05 internal/cli/apply.go:112,:116 - apply saves state on both branches of the homeOverride fork before the runner is constructed at :137; saveStateAt (internal/config/state.go:459) has no dry-run awareness - the state file itself, minimal profile payload",
	".config/dotfiles/config.yaml\t-rw-r--r--\td69657e98b6103f19c2be1b23455d37cac68553cfb35fd389305ac6ef9be01dd": "BUG-05 internal/cli/apply.go:112,:116 - apply saves state on both branches of the homeOverride fork before the runner is constructed at :137; saveStateAt (internal/config/state.go:459) has no dry-run awareness - the state file itself, full profile payload",
	".config/dotfiles/config.yaml\t-rw-r--r--\t96c54d6293100d2dafe88f136086a46d887155e3105418514c431152ba4033c5": "BUG-05 internal/cli/apply.go:112,:116 - apply saves state on both branches of the homeOverride fork before the runner is constructed at :137; saveStateAt (internal/config/state.go:459) has no dry-run awareness - the state file itself, server profile payload",
}

// snapshotTree is a character-identical copy of the package exec helper in
// internal/exec/runner_dryrun_test.go. The two packages share no internal test
// helper package, so each keeps its own copy and a diff of the two bodies is an
// acceptance criterion of the plan that added this file.
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

// blockNetwork fails the test the moment anything reaches for the network.
//
// The framing matters: this is not "the network is unavailable so the test
// passes", it is "a --dry-run that reaches for the network has already failed".
// A preview has no reason to make a request, and a request during preview is a
// side effect the user did not consent to.
//
// internal/fileutil/download.go:32 uses http.DefaultClient, so replacing
// http.DefaultTransport's dialer reaches every in-process HTTP attempt. Ceiling:
// it does NOT cover network reached by a subprocess - `brew` fetching its API
// JSON is exactly that. Subprocess network is BUG-06's territory and the
// container scenario's, not this hook's.
//
// The saved dialer is restored in t.Cleanup because http.DefaultTransport is
// process-global; without the restore a later test in the same binary would run
// with a poisoned dialer.
func blockNetwork(t *testing.T) {
	t.Helper()
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("http.DefaultTransport is not *http.Transport, so the network block cannot be installed")
	}
	saved := tr.DialContext
	tr.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
		t.Errorf("a truthful --dry-run made a network request: %s %s", network, addr)
		return nil, fmt.Errorf("network blocked by GUARD-03")
	}
	t.Cleanup(func() { tr.DialContext = saved })
}

// TestApply_DryRunLeavesEmptyHomeUntouched asserts the dry-run filesystem
// invariant across all three profiles. `full` carries the download-shaped modules
// that reach internal/fileutil/download.go - the paths that bypass exec.Runner
// and the stated reason this guard exists separately from GUARD-02 - so it must
// not be dropped for speed.
//
// Without this test, a refactor can move a write above the dry-run branch and
// still compile, and nothing catches it until a user's live machine is mutated by
// a preview.
func TestApply_DryRunLeavesEmptyHomeUntouched(t *testing.T) {
	// BUG-06 internal/exec/brew.go:293 - RefreshPath re-opens the PATH sandbox
	// from an os.Stat, so read-only probes run real third-party binaries that
	// write into HOME and reach the network. A test cannot close this without
	// editing the production code Phase 1 is forbidden from touching. This is a
	// runtime probe, so it self-corrects on whichever runner does or does not
	// ship Homebrew; tests/scenarios/dry-run-empty-home.sh carries the
	// unconditional assertion in a clean ubuntu:22.04 container regardless.
	for _, prefix := range []string{"/opt/homebrew/bin", "/home/linuxbrew/.linuxbrew/bin"} {
		if _, err := os.Stat(prefix); err == nil {
			t.Skipf("host homebrew at %s defeats the PATH sandbox: BUG-06 internal/exec/brew.go:293 - RefreshPath re-opens the PATH sandbox from an os.Stat, so read-only probes run real third-party binaries; tests/scenarios/dry-run-empty-home.sh carries the unconditional assertion in a clean container", prefix)
		}
	}

	// Pin the umask so the recorded mode is host-independent. Under `umask 077` a
	// developer records different directory modes than CI does, the full-line
	// keys stop matching, and the pressure is to widen the table - the exact
	// failure this guard exists to prevent. This repo runs no test concurrently
	// with another, so a process-global umask for the test's duration is safe.
	prevUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(prevUmask) })

	observed := make(map[string]bool, len(knownDryRunDeviations))

	for _, profile := range []string{"minimal", "full", "server"} {
		t.Run(profile, func(t *testing.T) {
			blockNetwork(t)

			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", t.TempDir()) // empty: no third-party tool is reachable
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

			before := snapshotTree(t, home)
			if out, errOut, err := runDotForTest("apply", "--profile", profile, "--yes", "--dry-run", "--home", home); err != nil {
				t.Fatalf("apply --dry-run %s: %v\nstdout=%s\nstderr=%s", profile, err, out, errOut)
			}
			after := snapshotTree(t, home)

			for _, line := range addedSnapshotLines(before, after) {
				observed[line] = true
				if _, known := knownDryRunDeviations[line]; !known {
					t.Errorf("dry-run (%s) wrote %s into an empty HOME and it is not attributable to a recorded requirement\n  full snapshot line: %q\n  Mint a BUG entry in .planning/REQUIREMENTS.md for it first, then record it in knownDryRunDeviations naming that ID and its file:line. Do not widen the table to absorb an unattributed write.",
						profile, snapshotLinePath(line), line)
				}
			}

			// A second dry-run against the same HOME must add nothing new: a
			// preview that keeps growing its footprint on repeat is still writing.
			if out, errOut, err := runDotForTest("apply", "--profile", profile, "--yes", "--dry-run", "--home", home); err != nil {
				t.Fatalf("second apply --dry-run %s: %v\nstdout=%s\nstderr=%s", profile, err, out, errOut)
			}
			if twice := snapshotTree(t, home); twice != after {
				t.Errorf("a second dry-run (%s) changed the HOME the first one left behind\n  after first run:\n%s\n  after second run:\n%s", profile, after, twice)
			}
		})
	}

	// Reverse direction: an entry that stops occurring fails the test. This is
	// what makes the table self-pruning when Phase 5 lands BUG-05's fix - without
	// it a stale excuse would sit here forever, silently forgiving a write that
	// no longer happens and would be a real finding if it came back.
	stale := make([]string, 0, len(knownDryRunDeviations))
	for line := range knownDryRunDeviations {
		if !observed[line] {
			stale = append(stale, line)
		}
	}
	sort.Strings(stale)
	for _, line := range stale {
		t.Errorf("knownDryRunDeviations entry never occurred during this run, so the defect it records appears fixed - remove the entry\n  entry: %q\n  recorded as: %s", line, knownDryRunDeviations[line])
	}
}

// addedSnapshotLines returns every line present in after but not in before.
func addedSnapshotLines(before, after string) []string {
	prev := make(map[string]bool)
	for _, line := range strings.Split(before, "\n") {
		if line != "" {
			prev[line] = true
		}
	}
	var added []string
	for _, line := range strings.Split(after, "\n") {
		if line != "" && !prev[line] {
			added = append(added, line)
		}
	}
	sort.Strings(added)
	return added
}

// snapshotLinePath returns the path field of a snapshot line, for readable
// failure messages. The comparison itself always uses the whole line.
func snapshotLinePath(line string) string {
	return strings.SplitN(line, "\t", 2)[0]
}

// TestSnapshotTree_CatchesSymlinkRetarget is the internal/cli twin of the
// identical assertion in internal/exec. Both copies of snapshotTree recorded a
// symlink's path, type and mode but not its target, so a retargeted symlink
// compared as identical (T-01-18, T-01-30).
//
// This copy is tested separately rather than relying on the exec one because
// Phase 3 moves 16,387 lines out of internal/cli: this is the copy most likely
// to be edited, and "the other file has a test" is not a property the compiler
// enforces.
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
