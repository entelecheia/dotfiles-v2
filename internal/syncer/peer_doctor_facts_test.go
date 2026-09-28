package syncer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func doctorFacts() (*PeerSideFacts, *PeerSideFacts) {
	base := func() *PeerSideFacts {
		return &PeerSideFacts{RsyncPath: "/opt/homebrew/bin/rsync", RsyncVersion: "rsync  version 3.4.4", Owner: "m5x26", OwnerEpoch: 2,
			MaxDelete: 100, Propagation: PropagationPolicy{Create: true, Update: true, Delete: true}, Filters: map[string]string{"exclude.txt": "a"}}
	}
	local, peer := base(), base()
	local.Coordinator, local.Scheduler = true, true
	peer.Replica = &PeerReplicaFacts{Generation: 7, Coordinator: "m5x26"}
	return local, peer
}

func checkFor(checks []DoctorCheck, name, level string) *DoctorCheck {
	for i := range checks {
		if checks[i].Name == name && checks[i].Level == level {
			return &checks[i]
		}
	}
	return nil
}

func TestEvaluatePeerSides(t *testing.T) {
	local, peer := doctorFacts()
	checks := evaluatePeerSides(local, peer, "m5x26", "m3x23")
	for _, c := range checks {
		if c.Level != DoctorPass {
			t.Fatalf("a healthy pair reported %+v", c)
		}
	}

	local, peer = doctorFacts()
	peer.Coordinator, peer.Scheduler = true, true
	// The reunion: the marked coordinator's inventory stops on the peer's names.
	local.NFDMarked, local.NonNFD, local.NonNFDSample = true, 1, []string{"a.md"}
	peer.NonNFD, peer.NonNFDSample = 16, []string{"sites/x/한글.md"}
	peer.MaxDelete, peer.Filters = 2000, map[string]string{"exclude.txt": "b", "allow.txt": "c"}
	peer.RsyncError = "local rsync is openrsync or 2.x"
	peer.Replica = nil
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	for _, want := range []struct{ name, level, fix string }{
		{"roles", DoctorFail, "dot sync owner --profile=peer --set"},
		{"nfd", DoctorFail, "on m3x23: dot sync names normalize --profile=peer"},
		{"nfd", DoctorWarn, "on m5x26: dot sync names normalize --profile=peer"},
		{"nfd", DoctorWarn, "on m3x23: dot sync names normalize --profile=peer"},
		{"rsync", DoctorFail, "on m3x23: brew install rsync"},
		{"config", DoctorWarn, ""},
	} {
		found := false
		for _, c := range checks {
			found = found || c.Name == want.name && c.Level == want.level && strings.Contains(c.Fix, want.fix)
		}
		if !found {
			t.Errorf("no %s/%s check with fix %q in %+v", want.name, want.level, want.fix, checks)
		}
	}

	// Only the coordinator's marker stops an inventory: a marked
	// non-coordinator's own names are a warning.
	local, peer = doctorFacts()
	peer.NFDMarked, peer.NonNFD = true, 3
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if c := checkFor(checks, "nfd", DoctorFail); c != nil {
		t.Errorf("a marked non-coordinator's names failed: %+v", c)
	}

	// A takeover's pending fence is the fence's to settle, not two
	// coordinators: the lower epoch demotes at its next run.
	local, peer = doctorFacts()
	peer.Coordinator, peer.Scheduler, peer.OwnerEpoch, peer.FencePending = true, true, 3, true
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if c := checkFor(checks, "roles", DoctorFail); c != nil {
		t.Errorf("a pending fence failed: %+v", c)
	}
	if c := checkFor(checks, "roles", DoctorWarn); c == nil || c.Fix != "on m5x26: dot peer sync (its fence demotes it)" {
		t.Errorf("pending fence not explained: %+v", checks)
	}

	// Owners the fence compares and refuses: a stale name at the same epoch,
	// or an empty one on a peer without an epoch.
	for _, tc := range []struct {
		owner string
		epoch int
	}{{"m5x26-old", 2}, {"", 0}} {
		local, peer = doctorFacts()
		peer.Owner, peer.OwnerEpoch = tc.owner, tc.epoch
		c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "roles", DoctorFail)
		if c == nil || c.Fix != "on m3x23: dot peer adopt --owner m5x26 --epoch 2" {
			t.Errorf("owner %q epoch %d: fence refusal not flagged: %+v", tc.owner, tc.epoch, c)
		}
	}

	// No coordinator: no sync runs. The other Mac's rsync client only
	// matters once it coordinates.
	local, peer = doctorFacts()
	local.Coordinator, peer.RsyncError = false, "openrsync"
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if checkFor(checks, "roles", DoctorFail) == nil || checkFor(checks, "rsync", DoctorWarn) == nil || checkFor(checks, "rsync", DoctorFail) != nil {
		t.Errorf("no coordinator / peer rsync: %+v", checks)
	}

	// A replica the takeover would refuse, or one the peer staged itself.
	local, peer = doctorFacts()
	peer.Replica, peer.ReplicaError = nil, "peer replica: exclude.txt sha256 mismatch"
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "replica", DoctorWarn); c == nil || !strings.Contains(c.Detail, "sha256 mismatch") {
		t.Errorf("refused replica passed")
	}
	local, peer = doctorFacts()
	peer.MachineNames, peer.Replica.Coordinator = []string{"m3x23"}, "m3x23"
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "replica", DoctorWarn); c == nil {
		t.Errorf("self-staged replica passed")
	}

	local, peer = doctorFacts()
	local.Scheduler, peer.Scheduler, peer.Replica = false, true, nil
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if c := checkFor(checks, "scheduler", DoctorWarn); c == nil || !strings.Contains(c.Fix, "on m5x26: dot peer setup") {
		t.Errorf("missing coordinator scheduler not flagged: %+v", checks)
	}
	if c := checkFor(checks, "replica", DoctorWarn); c == nil || !strings.Contains(c.Detail, "m3x23") {
		t.Errorf("missing replica not flagged: %+v", checks)
	}
}

// #182 AC1: the doctor reads the peer's facts over ssh and compares them.
func TestPeerDoctor_ComparesBothMachines(t *testing.T) {
	cfg, _ := peerDryRunSandbox(t)
	_, peer := doctorFacts()
	peer.Coordinator, peer.Scheduler, peer.OwnerEpoch = true, true, cfg.OwnerEpoch
	doc, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	dot := filepath.Join(t.TempDir(), "dot")
	writeStub(t, dot, "#!/bin/sh\n"+
		"[ \"$1\" = --version ] && { echo 'dot version 9.9.9 (fake)'; exit 0; }\n"+
		"[ \"$*\" = 'peer doctor --self' ] && { printf '%s\\n' '"+string(doc)+"'; exit 0; }\n"+
		"exit 1\n")
	useRemoteDotCandidates(t, dot)
	report, err := PeerDoctor(context.Background(), PeerDoctorOptions{Config: cfg, Probe: peerScheduleRunner(false), LocalDotVersion: "9.9.9 (fake)"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Peer == nil || report.Local == nil {
		t.Fatalf("facts missing: peer %v (%v), local %v", report.Peer, report.PeerFactsErr, report.Local)
	}
	if report.Local.Coordinator && checkFor(report.Checks, "roles", DoctorFail) == nil {
		t.Fatalf("two coordinators not flagged: %+v", report.Checks)
	}
	if report.Problems == 0 {
		t.Fatal("a failing check did not count as a problem")
	}
}

// #182 AC2: the NFD inventory error names the host and the exact command.
func TestPeerInventoryNFDErrorNamesHostAndCommand(t *testing.T) {
	requirePeerRsync(t)
	cfg, _ := peerDryRunSandbox(t)
	if err := MarkNFDMigration(cfg.LocalPaths.WorkspaceRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Target.Path, norm.NFC.String("한글.md")), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PeerDiff(context.Background(), PeerDiffOptions{Config: cfg, Probe: peerScheduleRunner(false)})
	if err == nil || !strings.Contains(err.Error(), "on fake-peer; run there: dot sync names normalize --profile=peer") {
		t.Fatalf("err = %v", err)
	}
}

// The sync summary counts the deletions it quarantined (issue comment).
func TestPeerSyncCountsQuarantinedDeletions(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1)
	gone := filepath.Join(sb.local, "gone-on-peer.txt")
	if err := os.WriteFile(gone, []byte("was on both\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := FingerprintFile(gone, FingerprintFast)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBaselineManifest(sb.paths.BaselineFile, map[string]Fingerprint{"gone-on-peer.txt": fp}); err != nil {
		t.Fatal(err)
	}
	if err := markPeerBaselineTarget(sb.cfg); err != nil {
		t.Fatal(err)
	}
	res, err := PeerSync(context.Background(), PeerSyncOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), SkipHome: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.QuarantinedHere != 1 || res.QuarantinedOnPeer != 0 || res.ConflictStamp == "" {
		t.Fatalf("result = %+v, want one deletion quarantined here", res)
	}
}
