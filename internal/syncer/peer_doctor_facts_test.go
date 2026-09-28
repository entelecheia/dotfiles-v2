package syncer

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Under an unmarked coordinator the other Mac's names are pulled as they
	// are and refused by its next sync, whatever the other Mac's marker.
	local, peer = doctorFacts()
	peer.NFDMarked, peer.NonNFD = true, 3
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if c := checkFor(checks, "nfd", DoctorFail); c == nil || c.Fix != "on m3x23: dot sync names normalize --profile=peer --yes" {
		t.Errorf("the other Mac's names under an unmarked coordinator: %+v", c)
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

	// The coordinator's own push preflight (nfdPushRefusal) and plan errors
	// stop its sync: the doctor fails where the sync stops.
	for _, tc := range []struct {
		name   string
		mutate func(local, peer *PeerSideFacts)
		fix    string
	}{
		{"unmarked coordinator with names", func(l, _ *PeerSideFacts) { l.NonNFD = 16 }, "on m5x26: dot sync names normalize --profile=peer --yes"},
		{"coordinator cannot plan", func(l, _ *PeerSideFacts) { l.NonNFDError = "permission denied" }, ""},
		{"marked coordinator, peer cannot plan", func(l, p *PeerSideFacts) { l.NFDMarked, p.NFDMarked, p.NonNFDError = true, true, "collision" }, ""},
	} {
		local, peer = doctorFacts()
		tc.mutate(local, peer)
		c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "nfd", DoctorFail)
		if c == nil || c.Fix != tc.fix {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}

	// The fence's topology check comes first: a peer profile that does not
	// point back here refuses every sync.
	local, peer = doctorFacts()
	local.WorkspacePath, local.TargetPath, local.TargetHost = "/Users/a/work", "/Users/b/work", "m3x23.ts.net"
	peer.WorkspacePath, peer.TargetPath, peer.TargetHost = "/Users/b/work", "/Users/a/elsewhere", "m5x26.ts.net"
	// The fix names the Mac that is wrong and the target, and leaves the
	// owner and epoch alone (dot peer init would claim the owner).
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "roles", DoctorFail); c == nil || !strings.Contains(c.Detail, "does not point back") || c.Fix != "on m3x23: dot sync target --profile=peer ssh:m5x26.ts.net:/Users/a/work" {
		t.Errorf("topology mismatch: %+v", c)
	}
	// Also with both Macs coordinating after a takeover: the fence refuses
	// on both sides before any demotion.
	peer.Coordinator, peer.OwnerEpoch = true, 3
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "roles", DoctorFail); c == nil || !strings.Contains(c.Detail, "does not point back") {
		t.Errorf("topology mismatch with two coordinators passed: %+v", c)
	}

	// The other Mac holds a higher epoch: the coordinator's next sync
	// demotes it. It stays the owner when it answers to the recorded name,
	// else no coordinator is left.
	for _, tc := range []struct {
		owner, level string
	}{{"m5x26", DoctorWarn}, {"old-name", DoctorFail}} {
		local, peer = doctorFacts()
		local.MachineNames = []string{"m5x26"}
		peer.Owner, peer.OwnerEpoch = tc.owner, 3
		checks := evaluatePeerSides(local, peer, "m5x26", "m3x23")
		c := checkFor(checks, "roles", tc.level)
		if c == nil || c.Fix != "on m3x23: dot peer adopt --owner m5x26 --epoch 2" || !strings.Contains(c.Detail, "demotes") {
			t.Errorf("peer owner %q at a higher epoch: %+v", tc.owner, c)
		}
		// The other rows judge the state that fix leaves: m5x26 stays the
		// coordinator, so its scheduler stays and it needs no replica.
		for _, c := range checks {
			if (c.Name == "scheduler" || c.Name == "replica") && c.Level != DoctorPass {
				t.Errorf("peer owner %q at a higher epoch contradicts the roles fix: %+v", tc.owner, c)
			}
		}
	}

	// A coordinator the fence demotes still runs that sync, which checks
	// rsync before the fence: without rsync 3.x it never gets demoted.
	local, peer = doctorFacts()
	local.OwnerEpoch = 3
	peer.Coordinator, peer.Scheduler, peer.RsyncError = true, true, "openrsync"
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "rsync", DoctorFail); c == nil {
		t.Errorf("the demoting coordinator's missing rsync passed")
	}

	// A takeover from a coordinator that never had an epoch: the higher
	// side's fence refuses until the lower one's sync demotes it.
	local, peer = doctorFacts()
	local.OwnerEpoch, local.FencePending = 1, true
	peer.Coordinator, peer.Scheduler, peer.Owner, peer.OwnerEpoch = true, true, "m3x23", 0
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "roles", DoctorWarn); c == nil || !strings.Contains(c.Detail, "refused until m3x23's next sync") || c.Fix != "on m3x23: dot peer sync (its fence demotes it)" {
		t.Errorf("the higher side's refusal went unsaid: %+v", c)
	}
	// The coordinator holds the higher epoch: it proceeds; the other Mac is
	// told to catch up, since it never syncs.
	local, peer = doctorFacts()
	local.OwnerEpoch = 3
	checks = evaluatePeerSides(local, peer, "m5x26", "m3x23")
	if checkFor(checks, "roles", DoctorPass) == nil || checkFor(checks, "roles", DoctorWarn) == nil || checkFor(checks, "roles", DoctorFail) != nil {
		t.Errorf("coordinator ahead: %+v", checks)
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

// Every row judges the state the roles fix leaves: across coordinator
// shapes no host is told to remove its scheduler and to install it or adopt
// the role, and a Mac the fence demotes is not told to set up.
func TestEvaluatePeerSidesFixesDoNotContradict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(l, p *PeerSideFacts)
		never  []string
		want   string
	}{
		{"one coordinator", func(l, p *PeerSideFacts) {}, nil, ""},
		{"neither, this Mac keeps its plist", func(l, p *PeerSideFacts) { l.Coordinator = false }, []string{"on m5x26: dot peer setup --off"}, ""},
		{"two, the lower without a plist", func(l, p *PeerSideFacts) {
			l.Scheduler = false
			p.Coordinator, p.Scheduler, p.OwnerEpoch = true, true, 3
		}, []string{"on m5x26: dot peer setup"}, ""},
		{"two, the higher without a plist", func(l, p *PeerSideFacts) {
			l.OwnerEpoch, l.Scheduler = 3, false
			p.Coordinator, p.Scheduler = true, true
		}, nil, "after the roles fix, on m5x26: dot peer setup"},
		{"two at one epoch", func(l, p *PeerSideFacts) { p.Coordinator, p.Scheduler = true, true }, nil, ""},
		{"sole coordinator demoted to no one", func(l, p *PeerSideFacts) {
			l.MachineNames = []string{"m5x26"}
			p.Owner, p.OwnerEpoch = "old-name", 3
		}, []string{"on m5x26: dot peer setup --off"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, peer := doctorFacts()
			tc.mutate(local, peer)
			var fixes []string
			for _, c := range evaluatePeerSides(local, peer, "m5x26", "m3x23") {
				if c.Fix != "" {
					fixes = append(fixes, c.Fix)
				}
			}
			all := strings.Join(fixes, "\n")
			for _, host := range []string{"m5x26", "m3x23"} {
				off := strings.Contains(all, "on "+host+": dot peer setup --off")
				on := strings.Contains(strings.ReplaceAll(all, "setup --off", ""), "on "+host+": dot peer setup")
				adopt := host == "m5x26" && strings.Contains(all, "adopt --self")
				if off && (on || adopt) {
					t.Errorf("%s is told both ways:\n%s", host, all)
				}
			}
			if !strings.Contains(all, tc.want) {
				t.Errorf("no fix %q:\n%s", tc.want, all)
			}
			for _, never := range tc.never {
				if strings.Contains(strings.ReplaceAll(all, never+" --off", ""), never) {
					t.Errorf("fix %q given:\n%s", never, all)
				}
			}
		})
	}

	// The lower Mac answers to the higher one's owner: its demotion leaves
	// two writers at one epoch, which the fence lets through.
	local, peer := doctorFacts()
	local.MachineNames = []string{"m5x26"}
	peer.Coordinator, peer.Scheduler, peer.OwnerEpoch = true, true, 3
	if c := checkFor(evaluatePeerSides(local, peer, "m5x26", "m3x23"), "roles", DoctorFail); c == nil || !strings.Contains(c.Detail, "leaves two coordinators") {
		t.Errorf("a shared owner name passed: %+v", c)
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

// Parity (#182): the doctor's NFD verdict is FAIL exactly when a real peer
// diff, or one of two real syncs in a row, stops, for each coordinator
// marker and each Mac holding a name not in NFD. Real rsync, fake ssh.
func TestDoctorNFDVerdictMatchesTheSync(t *testing.T) {
	requirePeerRsync(t)
	nfc := norm.NFC.String("한글.md")
	// The other Mac is a plain peer, or a coordinator the fence will demote
	// (a takeover's reunion: it still passes its own owner guard at a lower
	// epoch, NFD-marked). Its names are judged against the Mac that syncs.
	for _, demoted := range []bool{false, true} {
		for _, marked := range []bool{false, true} {
			for _, where := range []string{"none", "coordinator", "other"} {
				t.Run(fmt.Sprintf("otherDemoted=%v marked=%v names=%s", demoted, marked, where), func(t *testing.T) {
					fields, localEpoch := peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1
					if demoted {
						fields.owner, localEpoch = "old-coordinator", 2
					}
					sb := newPeerHandoverSandbox(t, fields, localEpoch)
					if marked {
						if err := MarkNFDMigration(sb.local); err != nil {
							t.Fatal(err)
						}
					}
					other := &PeerSideFacts{Owner: sb.owner, OwnerEpoch: 1}
					if demoted {
						other = &PeerSideFacts{Owner: "old-coordinator", OwnerEpoch: 1, Coordinator: true, NFDMarked: true}
					}
					switch where {
					case "coordinator":
						if err := os.WriteFile(filepath.Join(sb.local, nfc), []byte("x"), 0o644); err != nil {
							t.Fatal(err)
						}
					case "other":
						if err := os.WriteFile(filepath.Join(sb.peer, nfc), []byte("x"), 0o644); err != nil {
							t.Fatal(err)
						}
						other.NonNFD, other.NonNFDSample = 1, []string{nfc}
					}
					facts := LocalPeerSideFacts(context.Background(), peerScheduleRunner(false), sb.cfg, "9.9.9")
					if !facts.Coordinator {
						t.Fatal("the sandbox Mac is not the coordinator")
					}
					doctorFails := false
					for _, c := range evaluatePeerSides(facts, other, "here", "there") {
						doctorFails = doctorFails || c.Name == "nfd" && c.Level == DoctorFail
					}

					_, diffErr := PeerDiff(context.Background(), PeerDiffOptions{Config: sb.cfg, Probe: peerScheduleRunner(false)})
					stops := diffErr != nil
					for run := 1; run <= 2 && !stops; run++ {
						_, err := PeerSync(context.Background(), PeerSyncOptions{Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), SkipHome: true})
						stops = err != nil
					}
					if doctorFails != stops {
						t.Fatalf("doctor fails %v, sync stops %v (diff: %v)", doctorFails, stops, diffErr)
					}
				})
			}
		}
	}
}
