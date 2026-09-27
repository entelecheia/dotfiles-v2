package admission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var diagT0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))

func writeOrCompareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("drifted from golden %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestDeferOutcomeJSON_Golden(t *testing.T) {
	outcome := DeferOutcome{
		Outcome:           "deferred",
		Scope:             "/Users/u/work/repo/.git",
		Class:             ClassHeavy,
		Owner:             "builder@mac-mini",
		Reason:            "slot busy: heavy class held by builder@mac-mini (pid 4242, since 2026-09-27T11:30:00+09:00)",
		RetryAfterSeconds: 15,
	}
	data, err := json.MarshalIndent(outcome, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeOrCompareGolden(t, "defer_outcome.json.golden", append(data, '\n'))
}

func TestBuildBundleJSON_Golden(t *testing.T) {
	snap := PressureSnapshot{
		Platform:         "darwin",
		MemoryLevel:      MemoryNormal,
		MemoryAvailable:  true,
		MemoryFreePct:    55,
		ThermalCPULimit:  100,
		ThermalAvailable: true,
		Load1:            3.2,
		NumCPU:           10,
		LoadAvailable:    true,
		IdlePct:          82,
		IdleAvailable:    true,
		WSScanOK:         true,
		BootAvailable:    true,
		BootTime:         diagT0.Add(-2 * time.Hour),
	}
	decision := Decision{Admit: true}
	owners := []Lease{{
		Owner: "builder@mac-mini", Session: "sess-1", PID: 4242,
		PIDStart: "Sun Sep 27 11:00:00 2026", PGID: 4242,
		Class: ClassHeavy, Scope: "/Users/u/work/repo/.git", CWD: "/Users/u/work/repo",
		AcquiredAt:  diagT0.Add(-time.Hour),
		HeartbeatAt: diagT0.Add(-time.Minute),
		Deadline:    diagT0.Add(time.Minute),
	}}
	bundle := BuildBundle(snap, decision, History{}, owners, diagT0)
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeOrCompareGolden(t, "status_bundle.json.golden", append(data, '\n'))
}

// TestBuildBundleAlwaysCarriesTheNote pins the bypass-visibility
// requirement: every status view states that uncovered workloads are
// invisible to the controller.
func TestBuildBundleAlwaysCarriesTheNote(t *testing.T) {
	b := BuildBundle(PressureSnapshot{}, Decision{Admit: true}, History{}, nil, diagT0)
	if b.Note != UncoveredNote || b.Note == "" {
		t.Errorf("note = %q, want the uncovered-workloads note", b.Note)
	}
	if b.Owners == nil {
		t.Error("owners must serialize as [], not null")
	}
}
