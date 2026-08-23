package cli

// GUARD-03's sibling for the sync and peer trees, which the apply-shaped guard in
// dryrun_property_test.go never reached. `--dry-run must remain truthful` is a
// promise about every command, and BUG-13 measured twelve files left behind by a
// single `dot peer sync --dry-run` on an empty HOME.
//
// These commands do not honour --home yet (BUG-07/BUG-08, plan 05-04), so the
// sandbox is built from HOME plus the XDG variables the way the GUARD-03 test
// builds it, not by passing the flag.

import (
	"path/filepath"
	"strings"
	"testing"
)

// syncDryRunSandbox points every home-derived resolution at a fresh temp dir.
// XDG_CONFIG_HOME outranks HOME in config.StateDir and syncer's own path
// resolution, so a HOME-only sandbox would still read the developer's real
// config.
func syncDryRunSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	return home
}

// TestSyncAndPeer_DryRunLeavesEmptyHomeUntouched pins BUG-13 and D-03.
//
// The exit code is deliberately not asserted. Several of these rows fail on an
// unconfigured target, and that is fine: the store creation BUG-13 records
// happens during profile resolution, before any command body runs, so a run that
// errors out has already written by the time it errors. The claim is about the
// tree.
func TestSyncAndPeer_DryRunLeavesEmptyHomeUntouched(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"sync push", []string{"sync", "push", "--dry-run"}},
		{"peer sync", []string{"peer", "sync", "--dry-run"}},
		// D-03: creating the store is what init is for, but a preview of that
		// creation is still a preview. The ROADMAP note that would have exempted
		// these two is overridden.
		{"sync init", []string{"sync", "init", "--dry-run"}},
		{"peer init", []string{"peer", "init", "--host", "someone@peer.example", "--dry-run"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := syncDryRunSandbox(t)
			before := snapshotTree(t, home)
			_, _, _ = runDotForTest(tc.args...)
			added := addedSnapshotLines(before, snapshotTree(t, home))
			if len(added) > 0 {
				t.Errorf("dot %s wrote %d entr(ies) into an empty HOME:\n  %s",
					strings.Join(tc.args, " "), len(added), strings.Join(added, "\n  "))
			}
		})
	}
}

// TestSyncInit_WithoutDryRunStillCreatesTheStore is the non-vacuity row for the
// preview arms above: without it, a preview arm that never returned to the real
// work would leave every row green while `dot sync init` had stopped working.
func TestSyncInit_WithoutDryRunStillCreatesTheStore(t *testing.T) {
	home := syncDryRunSandbox(t)
	before := snapshotTree(t, home)
	if out, errOut, err := runDotForTest("sync", "init"); err != nil {
		t.Fatalf("dot sync init: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if added := addedSnapshotLines(before, snapshotTree(t, home)); len(added) == 0 {
		t.Error("dot sync init created nothing, so the dry-run preview arm disabled the store creation outright rather than skipping it for the preview")
	}
}
