package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func seedProfile(t *testing.T, root, profile string, cfg *LocalConfig) *LocalPaths {
	t.Helper()
	paths := ResolveLocalPathsForProfile(root, profile)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatal(err)
	}
	cfg.Propagation = DefaultPropagationPolicy()
	if err := SaveLocalConfig(paths, cfg); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestRenameOwner_RewritesEveryOwnedProfileAndKeepsAlias(t *testing.T) {
	root := t.TempDir()
	sync := seedProfile(t, root, DefaultProfile, &LocalConfig{Owner: "Youngs-MacBook-Pro"})
	peer := seedProfile(t, root, PeerProfile, &LocalConfig{Owner: "youngs-macbook-pro", OwnerEpoch: 2, Target: "ssh:m3x23:/w"})
	other := seedProfile(t, root, "other", &LocalConfig{Owner: "macbook-pro-2023"})
	baseline := filepath.Join(peer.StoreDir, "baseline.manifest")
	if err := os.WriteFile(baseline, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := RenameOwner(root, "youngs-macbook-pro", "m5x26", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Profiles, []string{"peer", "sync"}) {
		t.Fatalf("renamed %v, want peer and sync", res.Profiles)
	}
	for _, paths := range []*LocalPaths{sync, peer} {
		got := loadPeerStoreConfig(t, paths)
		if got.Owner != "m5x26" || len(got.OwnerAliases) != 1 || NormalizeHostname(got.OwnerAliases[0]) != "youngs-macbook-pro" {
			t.Fatalf("%s: owner %q aliases %v", paths.StoreDir, got.Owner, got.OwnerAliases)
		}
	}
	if got := loadPeerStoreConfig(t, peer); got.OwnerEpoch != 2 || got.Target != "ssh:m3x23:/w" {
		t.Fatalf("rename touched epoch or target: %+v", got)
	}
	if got := loadPeerStoreConfig(t, other); got.Owner != "macbook-pro-2023" || got.OwnerAliases != nil {
		t.Fatalf("a profile owned by another machine changed: %+v", got)
	}
	if got := gitStateFileBytes(t, baseline); string(got) != "keep\n" {
		t.Fatal("baseline changed")
	}

	// A second rename keeps both earlier names; the old name no longer owns.
	if _, err := RenameOwner(root, "m5x26", "m5x27", false); err != nil {
		t.Fatal(err)
	}
	if got := loadPeerStoreConfig(t, peer); got.Owner != "m5x27" || len(got.OwnerAliases) != 2 {
		t.Fatalf("second rename: %+v", got)
	}
	if _, err := RenameOwner(root, "nobody", "x", false); err == nil || !strings.Contains(err.Error(), "no profile") {
		t.Fatalf("rename of an unknown owner: %v", err)
	}
}

func TestOwnerAliasesMatchTheGuardAndThePeer(t *testing.T) {
	names := MachineNames()
	if len(names) == 0 {
		t.Skip("no machine name")
	}
	if err := CheckOwner(&Config{Owner: "renamed-elsewhere", OwnerAliases: []string{names[0]}}); err != nil {
		t.Fatalf("alias naming this machine rejected: %v", err)
	}
	if err := CheckOwner(&Config{Owner: "renamed-elsewhere"}); err == nil {
		t.Fatal("unrelated owner accepted")
	}
	if !sameOwner("m5x26", []string{"youngs-macbook-pro"}, "Youngs-MacBook-Pro", nil) ||
		!sameOwner("youngs-macbook-pro", nil, "m5x26", []string{"youngs-macbook-pro"}) ||
		sameOwner("m5x26", nil, "m3x23", nil) {
		t.Fatal("sameOwner disagrees with the alias rule")
	}
}

func TestSetOwnerClearsAliases(t *testing.T) {
	paths := seedProfile(t, t.TempDir(), PeerProfile, &LocalConfig{Owner: "new", OwnerAliases: []string{"old"}, OwnerEpoch: 1})
	if _, err := SetOwner(OwnerOptions{Config: &Config{Profile: PeerProfile, LocalPaths: paths}, SetTo: "other"}); err != nil {
		t.Fatal(err)
	}
	if got := loadPeerStoreConfig(t, paths); got.OwnerAliases != nil {
		t.Fatalf("aliases survived a deliberate owner change: %v", got.OwnerAliases)
	}
}

// #185 AC: the Mac was renamed and `dot sync owner --rename` ran here, while
// the peer still records the old name (not migrated yet, or unreachable).
// The next sync completes without an adoption run and plans no deletion.
func TestPeerSync_AfterRenameWithUnmigratedPeer(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{owner: "old-mac-name", epoch: 2, dotVersion: "9.9.9 (fake)"}, 2)
	if err := SaveLocalConfig(sb.paths, &LocalConfig{
		Target: "ssh:fake-peer:" + sb.peer, Owner: "old-mac-name", OwnerEpoch: 2,
		Propagation: PropagationPolicy{Create: true, Update: true, Delete: true}, MaxDelete: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameOwner(sb.local, "old-mac-name", sb.owner, false); err != nil {
		t.Fatal(err)
	}
	stored := loadPeerStoreConfig(t, sb.paths)
	sb.cfg.Owner, sb.cfg.OwnerAliases, sb.cfg.OwnerEpoch = stored.Owner, stored.OwnerAliases, stored.OwnerEpoch

	res, err := PeerSync(context.Background(), PeerSyncOptions{
		Config: sb.cfg, Runner: peerScheduleRunner(false), Probe: peerScheduleRunner(false), SkipHome: true,
	})
	if err != nil {
		t.Fatalf("PeerSync after the rename: %v", err)
	}
	if !res.Complete || res.Demoted {
		t.Fatalf("result = %+v, want a complete run without demotion", res)
	}
	if got := loadPeerStoreConfig(t, sb.paths); got.Owner != sb.owner || got.OwnerEpoch != 2 {
		t.Fatalf("store after the sync: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(sb.local, "peer-only.txt")); err != nil {
		t.Fatal("peer file did not arrive")
	}
}

func TestRenamePeerOwnerRunsTheRenameOnThePeer(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1)
	sb.installFakeRemoteDot(t)
	if err := RenamePeerOwner(context.Background(), peerScheduleRunner(false), sb.cfg, "old-mac-name", "new-mac-name"); err != nil {
		t.Fatal(err)
	}
	lines := sb.recordLines(t)
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "dot sync owner --rename --local-only old-mac-name new-mac-name") {
		t.Fatalf("remote commands: %v", lines)
	}
}

// Two different current owners that share an old name are two coordinators;
// the fence must still refuse them at equal epochs.
func TestSameOwner_SharedAliasIsNotTheSameOwner(t *testing.T) {
	if sameOwner("m5x26", []string{"youngs-macbook-pro"}, "m3x23", []string{"youngs-macbook-pro"}) {
		t.Fatal("a shared alias made two owners equal")
	}
	cfg := &Config{Owner: "m5x26", OwnerAliases: []string{"youngs-macbook-pro"}, OwnerEpoch: 3, LocalPath: "/w/", Target: Target{Kind: TargetSSH, Host: "p", Path: "/p"}}
	remote := &remotePeerStatus{OwnerEpoch: 3, DotVersion: "9.9.9"}
	remote.Profile.Owner, remote.Profile.OwnerAliases = "m3x23", []string{"youngs-macbook-pro"}
	remote.Profile.WorkspacePath, remote.Profile.Target.Path = "/p", "/w"
	if _, _, err := peerFence(cfg, remote); err == nil || !strings.Contains(err.Error(), "equal owner epochs") {
		t.Fatalf("fence err = %v, want the equal-epoch refusal", err)
	}
}

// An adopted, initialized or configured different owner drops the aliases
// the old owner carried (PeerAdopt is the demotion and handover path).
func TestOwnerChangesDropAliases(t *testing.T) {
	paths := seedProfile(t, t.TempDir(), PeerProfile, &LocalConfig{Owner: "m5x26", OwnerAliases: []string{"youngs-macbook-pro"}, OwnerEpoch: 3})
	cfg := &Config{Profile: PeerProfile, LocalPaths: paths}
	if _, err := PeerAdopt(cfg, PeerAdoptOptions{Owner: "m3x23", Epoch: 4}); err != nil {
		t.Fatal(err)
	}
	if got := loadPeerStoreConfig(t, paths); got.Owner != "m3x23" || got.OwnerAliases != nil {
		t.Fatalf("after adopting another owner: %+v", got)
	}
	local := &LocalConfig{Owner: "a", OwnerAliases: []string{"old-a"}}
	AssignOwner(local, "A")
	if len(local.OwnerAliases) != 1 {
		t.Fatal("a case-only change dropped the aliases")
	}
}

func TestRenameOwner_GenericNamesAndDryRun(t *testing.T) {
	root := t.TempDir()
	paths := seedProfile(t, root, DefaultProfile, &LocalConfig{Owner: "Mac"})
	if _, err := RenameOwner(root, "mac", "m5x26", true); err != nil {
		t.Fatal(err)
	}
	if got := loadPeerStoreConfig(t, paths); got.Owner != "Mac" {
		t.Fatalf("dry run wrote %+v", got)
	}
	if _, err := RenameOwner(root, "mac", "m5x26", false); err != nil {
		t.Fatal(err)
	}
	if got := loadPeerStoreConfig(t, paths); got.Owner != "m5x26" || got.OwnerAliases != nil {
		t.Fatalf("generic name kept as an alias: %+v", got)
	}
	if _, err := RenameOwner(root, "young's pro", "x", false); err == nil || !strings.Contains(err.Error(), "--set") {
		t.Fatalf("quoted old name: %v", err)
	}
}

func TestPeerMachineNamesReadsTheStatusDocument(t *testing.T) {
	sb := newPeerHandoverSandbox(t, peerStatusFields{epoch: 1, dotVersion: "9.9.9 (fake)"}, 1)
	status := fmt.Sprintf(`{"schemaVersion":%d,"kind":"peer","profile":{"configured":true,"owner":%q,"machineNames":["m3x23","macbook-pro-2023"],"workspacePath":%q,"target":{"path":%q}}}`,
		PeerStatusSchemaVersion, sb.owner, sb.peer, sb.local)
	installFakePeerSSH(t, status)
	names, err := PeerMachineNames(context.Background(), peerScheduleRunner(false), sb.cfg)
	if err != nil || !slices.Equal(names, []string{"m3x23", "macbook-pro-2023"}) {
		t.Fatalf("names = %v, %v", names, err)
	}
}
