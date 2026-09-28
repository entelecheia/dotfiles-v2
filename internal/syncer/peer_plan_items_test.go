package syncer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPeerRunPlanFlattensThreeWayPlan(t *testing.T) {
	fp := func(n int64) PeerFile { return PeerFile{Present: true, FP: Fingerprint{Size: n}} }
	plan := &PeerPlan{
		Pull: []string{"new", "changed"}, Push: []string{"mine", "both"},
		DeleteLocal: []string{"gone-there"}, DeleteRemote: []string{"gone-here"},
		Conflicts:    []PeerConflict{{RelPath: "both", Reason: "simultaneous edit/edit"}},
		LocalBefore:  PeerSnapshot{"changed": fp(1), "mine": fp(2), "both": fp(3), "gone-there": fp(4)},
		RemoteBefore: PeerSnapshot{"new": fp(5), "changed": fp(6), "both": fp(7), "gone-here": fp(8)},
	}
	rp := newPeerRunPlan(&Config{MaxDelete: 100, Propagation: PropagationPolicy{Delete: true}})
	rp.addPlan(plan, PlanScopeWorkspace, planRun{authorized: true, evidence: []string{"gone-here"}})
	rp.sortItems()
	got := map[string]string{}
	for _, it := range rp.Items {
		got[it.Path] = it.Direction + " " + it.Action
	}
	want := map[string]string{"new": "pull create", "changed": "pull update", "mine": "push create", "both": "push conflict", "gone-there": "pull delete", "gone-here": "push delete"}
	for path, w := range want {
		if got[path] != w {
			t.Errorf("%s = %q, want %q", path, got[path], w)
		}
	}
	if d := rp.Deletes[PlanScopeWorkspace]; d.In != 1 || d.Out != 1 || rp.MaxDelete != 100 {
		t.Fatalf("deletes %+v max %d", d, rp.MaxDelete)
	}
}

func TestParseAdditiveItem(t *testing.T) {
	it, ok, err := parseAdditiveItem("@@>f.st......\t22\t2026/09/28-13:17:26\t.claude.json", "pull")
	if err != nil || !ok || it.Action != "update" || it.Peer == nil || it.Peer.Size != 22 || it.Local != nil {
		t.Fatalf("update = %+v %v %v", it, ok, err)
	}
	it, ok, _ = parseAdditiveItem("@@<f+++++++++\t1\t2026/09/28-13:17:26\t.gitconfig", "push")
	if !ok || it.Action != "create" || it.Local == nil {
		t.Fatalf("create = %+v", it)
	}
	if _, ok, _ := parseAdditiveItem("@@cd+++++++++\t0\t2026/09/28-13:17:26\t.claude/", "pull"); ok {
		t.Fatal("a directory counted as a payload")
	}
}

// peerPlanFixture: one of each action across the three scopes.
func peerPlanFixture(t *testing.T) (*Config, string, string) {
	t.Helper()
	cfg, paths, localHome, peerHome := peerHomeTrackedFixture(t, "mem\n")
	local := strings.TrimRight(cfg.LocalPath, "/")
	peerWS := cfg.Target.Path
	write := func(root, rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(peerWS, "peer-only.txt", "peer\n")
	write(local, "local-only.txt", "local\n")
	write(local, "gone-there.txt", "was on both\n")
	baseline := map[string]Fingerprint{"gone-there.txt": peerHomeFP(t, local, "gone-there.txt")}
	if err := SaveBaselineManifest(paths.BaselineFile, baseline); err != nil {
		t.Fatal(err)
	}
	if err := markPeerBaselineTarget(cfg); err != nil {
		t.Fatal(err)
	}
	writePeerHomeFile(t, localHome, "mem/keep.txt", "keep", peerHomeFixedTime)
	writePeerHomeFile(t, peerHome, "mem/keep.txt", "keep", peerHomeFixedTime)
	writePeerHomeFile(t, peerHome, "mem/peer-new.txt", "new", peerHomeFixedTime)
	seedPeerHomeBaseline(t, cfg, map[string]Fingerprint{"mem/keep.txt": peerHomeFP(t, localHome, "mem/keep.txt")})
	if err := os.WriteFile(PeerHomePathsFile(paths), []byte(".claude.json\n.gitconfig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePeerHomeFile(t, localHome, ".claude.json", `{"a":1}`, peerHomeFixedTime)
	writePeerHomeFile(t, peerHome, ".claude.json", `{"a":1,"b":2}`, peerHomeFixedTime.Add(3600e9))
	writePeerHomeFile(t, localHome, ".gitconfig", "[user]\n", peerHomeFixedTime)
	return cfg, localHome, peerHome
}

func planKeys(p *PeerRunPlan) []string {
	var out []string
	for _, it := range p.Items {
		out = append(out, it.Scope+" "+it.Direction+" "+it.Action+" "+it.Path)
	}
	return out
}

// #180 AC1: peer diff --json lists every planned action, host paths and
// deletions included.
func TestPeerDiff_ItemizesEveryScope(t *testing.T) {
	cfg, _, _ := peerPlanFixture(t)
	res, err := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false), Itemize: true})
	if err != nil {
		t.Fatalf("PeerDiff: %v", err)
	}
	got := planKeys(res.Items)
	for _, want := range []string{
		"workspace pull create peer-only.txt",
		"workspace push create local-only.txt",
		"workspace pull delete gone-there.txt",
		"host-tracked pull create mem/peer-new.txt",
		"host pull update .claude.json",
		"host push create .gitconfig",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("plan lacks %q:\n%s", want, strings.Join(got, "\n"))
		}
	}
	doc, err := json.Marshal(res.Items)
	if err != nil || !strings.Contains(string(doc), `"workspace":{"in":1,"out":0}`) {
		t.Fatalf("json = %s, %v", doc, err)
	}
}

// #180 AC2: the dry-run plan is exactly the plan the real run executes.
func TestPeerSync_DryRunPlanIsTheRunsPlan(t *testing.T) {
	cfg, localHome, _ := peerPlanFixture(t)
	dry, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, Itemize: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	run, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), Itemize: true})
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if a, b := planKeys(dry.Plan), planKeys(run.Plan); !slices.Equal(a, b) {
		t.Fatalf("dry-run plan differs from the run's:\n%s\n---\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
	if _, err := os.Stat(filepath.Join(strings.TrimRight(cfg.LocalPath, "/"), "gone-there.txt")); !os.IsNotExist(err) {
		t.Fatal("the planned deletion did not happen")
	}
	if body := string(gitStateFileBytes(t, filepath.Join(localHome, ".claude.json"))); body != `{"a":1,"b":2}` {
		t.Fatalf("the planned host update did not happen: %s", body)
	}
}

// #180 AC2: a one-directional run lists only its direction, and a deletion
// the run holds says so.
func TestPeerSync_DryRunPlanFollowsTheRunMode(t *testing.T) {
	cfg, _, _ := peerPlanFixture(t)
	res, err := PeerSync(context.Background(), PeerSyncOptions{Config: cfg, Runner: peerScheduleRunner(true), Probe: peerScheduleRunner(false), DryRun: true, PushOnly: true, Itemize: true})
	if err != nil {
		t.Fatal(err)
	}
	got := planKeys(res.Plan)
	if !slices.Contains(got, "workspace push create local-only.txt") {
		t.Fatalf("push missing:\n%s", strings.Join(got, "\n"))
	}
	for _, it := range res.Plan.Items {
		if it.Direction == "pull" {
			t.Errorf("a push-only run lists %s %s %s", it.Scope, it.Action, it.Path)
		}
	}

	cfg, _, _ = peerPlanFixture(t)
	cfg.Propagation.Delete = false
	diff, err := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false), Itemize: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range diff.Items.Items {
		if it.Path == "gone-there.txt" && it.Reason != "held: propagation.delete is off" {
			t.Fatalf("held deletion listed as %+v", it)
		}
	}
}
