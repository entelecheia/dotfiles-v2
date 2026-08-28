package cli

// GUARD-03, sync and peer half: every `--dry-run`-accepting sync subcommand must
// leave the target tree byte-identical, exactly as `dot apply --dry-run` already
// must in dryrun_property_test.go.
//
// The apply half has never covered this tree. Both of its arms
// (TestApplyDryRunLeavesEmptyHomeByteIdentical here and
// tests/scenarios/dry-run-empty-home.sh in the container) drive `apply` and
// nothing else, so every write the sync and peer engines make during a preview
// has been outside the guard since the guard was written. BUG-13's own
// requirement text predicted this ("this class is currently unguarded in the
// sync and peer trees"); Phase 5 closed the store creation it named and left the
// guard gap open, which is how `sync fetch --dry-run` kept re-creating the store.
//
// There is deliberately no known-deviation table here. The apply half keeps one
// because it inherited five real deviations that had to be retired one at a
// time. This half starts empty and stays empty: a preview that writes is a
// defect, and the fix is the engine, not an entry.

import (
	"path/filepath"
	"testing"
)

// TestSyncDryRunLeavesTreeByteIdentical drives every sync subcommand that
// accepts --dry-run against a seeded workspace and asserts the whole HOME comes
// back unchanged.
//
// The fixture seeds a workspace and mirror because the preconditions run before
// the engine: against a bare HOME every one of these commands returns at "Local
// path missing" without reaching the lock, so a bare-HOME version of this test
// would pass while the defect it guards was fully present.
//
// `peer diff` and `peer sync` take the lock the same way (peer_commands.go:332
// and :386) but stop in SSH validation before reaching it without a live peer,
// so they cannot be asserted here. They are covered by the shared fix, not by a
// row that cannot fail.
func TestSyncDryRunLeavesTreeByteIdentical(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"push", []string{"sync", "push", "--dry-run"}},
		{"pull", []string{"sync", "pull", "--dry-run"}},
		{"intake", []string{"sync", "intake", "--dry-run"}},
		{"fetch", []string{"sync", "fetch", "seed.txt", "--dry-run"}},
		{"conflicts-prune", []string{"sync", "conflicts", "prune", "--dry-run"}},
		// The control: sync names already takes its lock below the dry-run early
		// return (sync_names_cmd.go:76), so it must stay clean through the fix.
		{"names", []string{"sync", "names", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncCLIFixture(t)
			writeCLITestFile(t, filepath.Join(f.local, "seed.txt"), "payload\n")
			writeCLITestFile(t, filepath.Join(f.mirror, "seed.txt"), "payload\n")

			args := append(append([]string{}, tc.args...), "--home", f.home)

			before := snapshotTree(t, f.home)
			if _, _, err := runDotForTest(args...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			after := snapshotTree(t, f.home)

			for _, line := range addedSnapshotLines(before, after) {
				t.Errorf("`dot %s --dry-run` wrote %s into the target home\n  full snapshot line: %q\n  A preview must leave the tree byte-identical. Fix the engine; do not record a deviation for it.",
					tc.name, snapshotLinePath(line), line)
			}

			// A second preview must add nothing the first one did not: a preview
			// that keeps growing its footprint on repeat is still writing.
			if _, _, err := runDotForTest(args...); err != nil {
				t.Fatalf("second run %v: %v", args, err)
			}
			if twice := snapshotTree(t, f.home); twice != after {
				t.Errorf("a second `dot %s --dry-run` changed the tree the first one left behind\n  after first:\n%s\n  after second:\n%s", tc.name, after, twice)
			}
		})
	}
}
