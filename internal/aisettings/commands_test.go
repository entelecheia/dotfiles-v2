package aisettings

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestPlanPrune_DeleteFlooredAtZeroButNegativeKeepLeftAlone pins both halves
// of PrunePlan.Delete's contract, which pull in opposite directions.
//
// Keep above Total must report 0, not a negative count: Delete is exported
// and documented as what a prune would remove, so a negative value would be
// a lie to any caller that acted on it.
//
// Keep below 1 must stay UNCLAMPED. The cli prints Delete verbatim in its
// confirmation line, and the pre-decomposition binary overstates there — with
// three snapshots and --keep -1 it prompts 4 and removes 2. Slice 03-04 is
// gated on byte-identity with that binary, so the overstatement is preserved
// on purpose and recorded as BUG-12 for Phase 5. Clamping this half would
// turn the differential harness red; a future "cleanup" that does so must
// fail here first.
func TestPlanPrune_DeleteFlooredAtZeroButNegativeKeepLeftAlone(t *testing.T) {
	e := planPruneTestEngine(t, 3)

	for _, tc := range []struct {
		name string
		keep int
		want int
	}{
		{"keep above total floors to zero", 10, 0},
		{"keep equal to total is zero", 3, 0},
		{"normal keep", 1, 2},
		{"negative keep stays unclamped (BUG-12)", -1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := e.PlanPrune(PruneOptions{Keep: tc.keep})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Delete != tc.want {
				t.Errorf("PlanPrune(Keep=%d).Delete = %d, want %d", tc.keep, plan.Delete, tc.want)
			}
			if plan.Keep != tc.keep {
				t.Errorf("PlanPrune(Keep=%d).Keep = %d, want it echoed back verbatim", tc.keep, plan.Keep)
			}
		})
	}
}

// planPruneTestEngine builds an engine whose host root holds n snapshots.
// list() only counts a directory that carries a readable meta.yaml, so each
// fixture needs one; anything less is silently skipped and the counts under
// test would all collapse to zero.
func planPruneTestEngine(t *testing.T, n int) *Engine {
	t.Helper()
	home := t.TempDir()
	e := &Engine{HomeDir: home, Root: filepath.Join(home, "bk"), Hostname: "h"}
	for i := range n {
		version := fmt.Sprintf("2026-01-%02d", i+1)
		dir := filepath.Join(e.HostRoot(), version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := fmt.Sprintf("version: %q\n", version)
		if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := e.List(); err != nil || len(got) != n {
		t.Fatalf("fixture seeded %d snapshots, List() returned %d (err %v)", n, len(got), err)
	}
	return e
}
