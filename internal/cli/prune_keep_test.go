package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file pins BUG-12 / D-06: `--keep` below 1 is REJECTED at flag
// validation on both prune commands, not silently floored. The prompt used to
// compute its deletion count from the raw flag while the engine clamped, so
// three snapshots with --keep -1 asked the operator to confirm 4 deletions and
// then performed 2. The engine floors stay where they are as unreachable
// defense; the rejection is the mechanism.

const pruneKeepRejection = "--keep must be at least 1"

// seedSnapshots writes n snapshot directories under
// <root>/<tree>/<host>/<version>, each with the meta.yaml that both engines
// require before they will list a directory as a snapshot.
func seedSnapshots(t *testing.T, root, tree, host string, n int) string {
	t.Helper()
	hostRoot := filepath.Join(root, tree, host)
	for i := range n {
		version := fmt.Sprintf("2026-01-%02d", i+1)
		dir := filepath.Join(hostRoot, version)
		writeCLITestFile(t, filepath.Join(dir, "meta.yaml"), fmt.Sprintf("version: %q\nhostname: %q\n", version, host))
	}
	return hostRoot
}

func countSnapshotDirs(t *testing.T, hostRoot string) int {
	t.Helper()
	entries, err := os.ReadDir(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// pruneCommand describes one of the two prune surfaces under test. Both must
// answer a bad --keep identically, so an operator sees one message regardless
// of which prune they ran.
type pruneCommand struct {
	name string
	tree string
	args []string
}

var pruneCommands = []pruneCommand{
	{name: "ai prune", tree: "ai-config", args: []string{"ai", "prune"}},
	{name: "profile prune", tree: "profiles", args: []string{"profile", "prune"}},
}

func TestPruneRejectsKeepBelowOne(t *testing.T) {
	for _, pc := range pruneCommands {
		for _, keep := range []string{"0", "-1"} {
			t.Run(pc.name+" --keep "+keep, func(t *testing.T) {
				home := t.TempDir()
				root := t.TempDir()
				hostRoot := seedSnapshots(t, root, pc.tree, "h", 3)

				args := append([]string{"--home", home}, pc.args...)
				args = append(args, "--from", root, "--host", "h", "--keep", keep, "--yes")
				out, errOut, err := runDotForTest(args...)
				if err == nil {
					t.Fatalf("--keep %s should be rejected\nstdout=%s\nstderr=%s", keep, out, errOut)
				}
				msg := err.Error()
				if !strings.Contains(msg, pruneKeepRejection) {
					t.Errorf("error should name the flag and its minimum, got: %v", err)
				}
				if !strings.Contains(msg, "(got "+keep+")") {
					t.Errorf("error should quote the observed value, got: %v", err)
				}
				if strings.Contains(out, "About to delete") || strings.Contains(out, "Pruned") {
					t.Errorf("a rejected run must not confirm or perform a deletion:\n%s", out)
				}
				if n := countSnapshotDirs(t, hostRoot); n != 3 {
					t.Errorf("%d snapshot(s) left on disk, want all 3 kept", n)
				}
			})
		}
	}
}

// TestPruneKeepRejectionWordingIsIdentical guards the half of D-06 that a
// per-command test cannot see: the two commands could each reject and still
// disagree about how to say so.
func TestPruneKeepRejectionWordingIsIdentical(t *testing.T) {
	msgs := make([]string, 0, len(pruneCommands))
	for _, pc := range pruneCommands {
		home := t.TempDir()
		root := t.TempDir()
		seedSnapshots(t, root, pc.tree, "h", 3)
		args := append([]string{"--home", home}, pc.args...)
		args = append(args, "--from", root, "--host", "h", "--keep", "0", "--yes")
		_, _, err := runDotForTest(args...)
		if err == nil {
			t.Fatalf("%s: --keep 0 should be rejected", pc.name)
		}
		msgs = append(msgs, err.Error())
	}
	if msgs[0] != msgs[1] {
		t.Errorf("the two prune commands word the same rejection differently:\n  %s: %s\n  %s: %s",
			pruneCommands[0].name, msgs[0], pruneCommands[1].name, msgs[1])
	}
}

// TestPruneKeepRejectionKeepsTodaysErrorPrecedence pins that adding the check
// did not move which error a bad run reports first. Both commands validate the
// host before they read or act on --keep today, so a run that is wrong in both
// ways must still report the host.
func TestPruneKeepRejectionKeepsTodaysErrorPrecedence(t *testing.T) {
	for _, pc := range pruneCommands {
		t.Run(pc.name, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			args := append([]string{"--home", home}, pc.args...)
			args = append(args, "--from", root, "--host", "..", "--keep", "-1", "--yes")
			_, _, err := runDotForTest(args...)
			if err == nil {
				t.Fatal("a bad host with a bad keep should still fail")
			}
			if !strings.Contains(err.Error(), "invalid --host") {
				t.Errorf("host validation should still win, got: %v", err)
			}
		})
	}
}

// TestPruneWithValidKeepStillDeletes is the non-vacuity row: the rejection
// must not have disabled the command it guards.
func TestPruneWithValidKeepStillDeletes(t *testing.T) {
	for _, pc := range pruneCommands {
		t.Run(pc.name, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			hostRoot := seedSnapshots(t, root, pc.tree, "h", 3)

			args := append([]string{"--home", home}, pc.args...)
			args = append(args, "--from", root, "--host", "h", "--keep", "1", "--yes")
			out, errOut, err := runDotForTest(args...)
			if err != nil {
				t.Fatalf("prune with a valid keep: %v\nstdout=%s\nstderr=%s", err, out, errOut)
			}
			if !strings.Contains(out, "Pruned 2 snapshot(s)") {
				t.Errorf("expected two snapshots pruned:\n%s", out)
			}
			if n := countSnapshotDirs(t, hostRoot); n != 1 {
				t.Errorf("%d snapshot(s) left on disk, want 1", n)
			}
		})
	}
}

// TestPruneWithKeepAboveTotalIsUnchanged pins the other end of the valid
// range: a keep the archive cannot reach still reports nothing to prune
// rather than erroring.
func TestPruneWithKeepAboveTotalIsUnchanged(t *testing.T) {
	for _, pc := range pruneCommands {
		t.Run(pc.name, func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			seedSnapshots(t, root, pc.tree, "h", 3)

			args := append([]string{"--home", home}, pc.args...)
			args = append(args, "--from", root, "--host", "h", "--keep", "10", "--yes")
			out, errOut, err := runDotForTest(args...)
			if err != nil {
				t.Fatalf("prune --keep 10: %v\nstdout=%s\nstderr=%s", err, out, errOut)
			}
			if !strings.Contains(out, "Nothing to prune") {
				t.Errorf("expected the nothing-to-prune message:\n%s", out)
			}
		})
	}
}
