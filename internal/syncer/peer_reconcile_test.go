package syncer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	goexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestAppendPeerConflictAuditRecordsDecision(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		ConfigDir: dir,
		Target:    Target{Kind: TargetSSH, Host: "peer", Path: "/work"},
	}
	plan := &PeerPlan{Conflicts: []PeerConflict{{
		RelPath: "shared/문서.txt",
		Reason:  "simultaneous edit/edit; coordinator copy pushed; peer payload quarantined",
	}}}
	if err := AppendPeerConflictAudit(cfg, plan); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "peer-conflicts.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{"shared/문서.txt", "coordinator copy pushed", "ssh:peer:/work"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit missing %q: %s", want, got)
		}
	}
}

func TestValidatePeerBaselineLocalTypesRejectsSymlinkTransition(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "doc.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Profile: PeerProfile, LocalPath: root + "/", FilterMode: FilterModeExclude}
	baseline := map[string]Fingerprint{"linked/doc.txt": peerTestFP(7, "base")}
	if err := ValidatePeerBaselineLocalTypes(cfg, baseline); err == nil {
		t.Fatal("baseline path through symlink was accepted as a regular payload")
	}
	if err := ValidatePeerBaselineLocalTypes(cfg, map[string]Fingerprint{"missing.txt": peerTestFP(1, "base")}); err != nil {
		t.Fatalf("missing baseline path should remain a valid deletion: %v", err)
	}
	volatile := filepath.Join(root, "scratchpad", "temp")
	if err := os.MkdirAll(volatile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(volatile, "runtime.sock")); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePeerBaselineLocalTypes(cfg, map[string]Fingerprint{"scratchpad/temp/runtime.sock": peerTestFP(1, "base")}); err != nil {
		t.Fatalf("excluded volatile baseline path should retire without blocking: %v", err)
	}
}

func peerTestFP(size int64, sha string) Fingerprint {
	return Fingerprint{Size: size, Sha: sha, Mtime: time.Unix(100, 0).UTC()}
}

func peerTestFile(size int64, sha string) PeerFile {
	return PeerFile{Present: true, FP: peerTestFP(size, sha)}
}

func TestPlanPeerReconcile_RemoteOnlyUpdateDeleteAndCreate(t *testing.T) {
	base := map[string]Fingerprint{
		"update.txt": peerTestFP(2, "base-update"),
		"gone.txt":   peerTestFP(2, "base-gone"),
	}
	local := PeerSnapshot{
		"update.txt": peerTestFile(2, "base-update"),
		"gone.txt":   peerTestFile(2, "base-gone"),
	}
	remote := PeerSnapshot{
		"update.txt": peerTestFile(3, "remote-update"),
		"new.txt":    peerTestFile(3, "remote-new"),
	}

	plan, err := PlanPeerReconcile(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Pull, []string{"new.txt", "update.txt"}) {
		t.Errorf("Pull = %v", plan.Pull)
	}
	if !slices.Equal(plan.DeleteLocal, []string{"gone.txt"}) {
		t.Errorf("DeleteLocal = %v", plan.DeleteLocal)
	}
	if len(plan.Push) != 0 || len(plan.DeleteRemote) != 0 || plan.HasConflicts() {
		t.Errorf("unexpected coordinator actions: %+v", plan)
	}
	if got := plan.NextBaseline["update.txt"].FP.Sha; got != "remote-update" {
		t.Errorf("next baseline update = %q", got)
	}
	if _, ok := plan.NextBaseline["gone.txt"]; ok {
		t.Error("remote-only deletion remained in next baseline")
	}
}

func TestValidatePeerPlanSafetyRefusesEmptyOrMassDeletingPeer(t *testing.T) {
	cfg := &Config{MaxDelete: 2}
	emptyReset := &PeerPlan{
		BaselineCount: 3,
		LocalCount:    3,
		RemoteCount:   0,
		DeleteLocal:   []string{"a", "b", "c"},
	}
	if err := ValidatePeerPlanSafety(cfg, emptyReset); err == nil || !strings.Contains(err.Error(), "remote inventory is empty") {
		t.Fatalf("empty reset safety error = %v", err)
	}
	massInbound := &PeerPlan{BaselineCount: 4, LocalCount: 4, RemoteCount: 1, DeleteLocal: []string{"a", "b", "c"}}
	if err := ValidatePeerPlanSafety(cfg, massInbound); err == nil || !strings.Contains(err.Error(), "inbound deletion") {
		t.Fatalf("inbound cap error = %v", err)
	}
	massOutbound := &PeerPlan{BaselineCount: 4, LocalCount: 1, RemoteCount: 4, DeleteRemote: []string{"a", "b", "c"}}
	if err := ValidatePeerPlanSafety(cfg, massOutbound); err == nil || !strings.Contains(err.Error(), "outbound deletion") {
		t.Fatalf("outbound cap error = %v", err)
	}
	smallReset := &PeerPlan{BaselineCount: 80, LocalCount: 80, RemoteCount: 1, DeleteLocal: make([]string, 79)}
	if err := ValidatePeerPlanSafety(&Config{MaxDelete: 100}, smallReset); err == nil || !strings.Contains(err.Error(), "probable reset") {
		t.Fatalf("small reset safety error = %v", err)
	}
	localReset := &PeerPlan{BaselineCount: 80, LocalCount: 1, RemoteCount: 80, DeleteRemote: make([]string, 79)}
	if err := ValidatePeerPlanSafety(&Config{MaxDelete: 100}, localReset); err == nil || !strings.Contains(err.Error(), "probable reset") {
		t.Fatalf("local reset safety error = %v", err)
	}
}

func TestPlanPeerReconcileCountsOnlyObservableBaselinePayloads(t *testing.T) {
	base := map[string]Fingerprint{
		"visible.txt": peerTestFP(1, "visible"),
		"retired.txt": peerTestFP(1, "retired"),
	}
	local := PeerSnapshot{"visible.txt": peerTestFile(1, "visible")}
	plan, err := PlanPeerReconcile(base, local, PeerSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaselineCount != 1 {
		t.Fatalf("BaselineCount = %d, want only the observable key", plan.BaselineCount)
	}
}

func TestValidatePeerPushRemoteStableRefusesLateUnquarantinedEdit(t *testing.T) {
	plan := &PeerPlan{
		Push:             []string{"ordinary.txt", "conflict.txt", "new.txt"},
		RemoteBefore:     PeerSnapshot{"ordinary.txt": peerTestFile(1, "old"), "conflict.txt": peerTestFile(1, "old-conflict")},
		QuarantineRemote: []string{"conflict.txt"},
	}
	stable := PeerSnapshot{"ordinary.txt": peerTestFile(1, "old"), "conflict.txt": peerTestFile(1, "new-conflict")}
	if err := ValidatePeerPushRemoteStable(plan, stable); err != nil {
		t.Fatalf("stable ordinary path rejected (quarantined drift is safe): %v", err)
	}
	lateEdit := clonePeerSnapshot(stable)
	lateEdit["ordinary.txt"] = peerTestFile(2, "late")
	if err := ValidatePeerPushRemoteStable(plan, lateEdit); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("late ordinary edit error = %v", err)
	}
	lateCreate := clonePeerSnapshot(stable)
	lateCreate["new.txt"] = peerTestFile(1, "late-create")
	if err := ValidatePeerPushRemoteStable(plan, lateCreate); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("late remote create error = %v", err)
	}
}

func TestCommitPeerBaselinePersistsProvenSnapshotNotLiveDrift(t *testing.T) {
	root := t.TempDir()
	paths := ResolveLocalPathsForProfile(root, PeerProfile)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Profile:    PeerProfile,
		LocalPath:  root + "/",
		LocalPaths: paths,
		Target:     Target{Kind: TargetSSH, Host: "peer", Path: root},
	}
	proven := peerTestFP(3, "planned")
	if err := os.WriteFile(filepath.Join(root, "doc.txt"), []byte("late local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitPeerBaseline(cfg, PeerSnapshot{"doc.txt": {Present: true, FP: proven}}); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadBaselineManifest(paths.BaselineFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := baseline["doc.txt"]; got.Sha != proven.Sha || got.Size != proven.Size {
		t.Fatalf("baseline = %+v, want proven plan fingerprint %+v", got, proven)
	}
}

func TestPlanPeerReconcile_CoordinatorWinsAndQuarantinesRemoteOnce(t *testing.T) {
	base := map[string]Fingerprint{"shared.txt": peerTestFP(4, "baseline")}
	local := PeerSnapshot{"shared.txt": peerTestFile(4, "coordinator")}
	remote := PeerSnapshot{"shared.txt": peerTestFile(4, "remote")}

	plan, err := PlanPeerReconcile(base, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Push, []string{"shared.txt"}) {
		t.Errorf("Push = %v", plan.Push)
	}
	if !slices.Equal(plan.QuarantineRemote, []string{"shared.txt"}) {
		t.Errorf("QuarantineRemote = %v", plan.QuarantineRemote)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0].RelPath != "shared.txt" {
		t.Errorf("Conflicts = %+v", plan.Conflicts)
	}
	if len(plan.Pull) != 0 {
		t.Errorf("conflict must not pull remote payload: %v", plan.Pull)
	}
	if got := plan.NextBaseline["shared.txt"].FP.Sha; got != "coordinator" {
		t.Errorf("next baseline = %q, want coordinator", got)
	}
}

func TestPlanPeerReconcile_LocalOnlyDeleteUsesRemoteDeletePass(t *testing.T) {
	base := map[string]Fingerprint{"gone.txt": peerTestFP(2, "baseline")}
	remote := PeerSnapshot{"gone.txt": peerTestFile(2, "baseline")}

	plan, err := PlanPeerReconcile(base, PeerSnapshot{}, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.DeleteRemote, []string{"gone.txt"}) {
		t.Errorf("DeleteRemote = %v", plan.DeleteRemote)
	}
	if len(plan.QuarantineRemote) != 0 {
		t.Errorf("unchanged remote deletion target should not be a conflict: %v", plan.QuarantineRemote)
	}
}

func TestPeerNormalArgs_NoRoutineBackupsAndVolatileLayer(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Profile = PeerProfile
	cfg.Target = Target{Kind: TargetSSH, Host: "peer", Path: "/work"}
	cfg.AllowPatterns = []string{".cache/**"}
	conflict := &ConflictDir{Timestamp: "ts"}

	for name, args := range map[string][]string{
		"pull": pullArgs(cfg, conflict, runtimeFilters{}, false),
		"push": pushArgs(cfg, conflict, runtimeFilters{}, false),
	} {
		for _, arg := range args {
			if arg == "--backup" || strings.HasPrefix(arg, "--backup-dir=") {
				t.Errorf("%s routine peer args contain backup: %v", name, args)
			}
		}
		for _, want := range []string{
			"--exclude=.cache",
			"--exclude=.cache/",
			"--exclude=/.maru/cache",
			"--exclude=/.maru/cache/",
			"--exclude=/.maru/desk-pipeline/logs",
			"--exclude=/.maru/desk-pipeline/logs/",
			"--exclude=/.maru/desk-pipeline/*.out",
			"--exclude=/.maru/runs",
			"--exclude=/.maru/runs/",
			"--exclude=test-results",
			"--exclude=test-results/",
			"--exclude=playwright-report",
			"--exclude=playwright-report/",
			"--exclude=.astro",
			"--exclude=.astro/",
			"--exclude=*.tsbuildinfo",
			"--exclude=/scratchpad/temp",
			"--exclude=/scratchpad/temp/",
			"--exclude=.metadata_never_index",
		} {
			if !slices.Contains(args, want) {
				t.Errorf("%s missing volatile deny %q: %v", name, want, args)
			}
		}
		for _, arg := range args {
			if strings.Contains(arg, "graphify-out") {
				t.Errorf("%s unexpectedly denies graphify-out: %v", name, args)
			}
		}
		denyAt := slices.Index(args, "--exclude=.cache/")
		allowAt := slices.Index(args, "--include=.cache/**")
		if allowAt >= 0 && denyAt > allowAt {
			t.Errorf("%s places volatile deny after editable allow: %v", name, args)
		}
	}
}

func TestPushPeerPlanPreflightsRemoteQuarantineBeforeRsync(t *testing.T) {
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "ssh"), []byte("#!/bin/sh\nexit 41\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "rsync-ran")
	rsyncScript := "#!/bin/sh\ntouch " + sentinel + "\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "rsync"), []byte(rsyncScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := newTestConfig(t)
	cfg.Profile = PeerProfile
	cfg.Target = Target{Kind: TargetSSH, Host: "peer", Path: "/work"}
	plan := &PeerPlan{
		Push:             []string{"shared.txt"},
		QuarantineRemote: []string{"shared.txt"},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := PushPeerPlan(context.Background(), goexec.NewRunner(false, logger), cfg, plan, &ConflictDir{Timestamp: "TS"}, false)
	if err == nil || !strings.Contains(err.Error(), "preflight peer quarantine") {
		t.Fatalf("preflight error = %v", err)
	}
	if _, statErr := os.Stat(sentinel); !os.IsNotExist(statErr) {
		t.Fatalf("rsync ran before quarantine preflight completed: %v", statErr)
	}
}

func TestPeerScopedArgs_UseNulPlanListWithoutBackup(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Profile = PeerProfile
	cfg.Target = Target{Kind: TargetSSH, Host: "peer", Path: "/work"}
	args, err := peerScopedArgs(cfg, runtimeFilters{}, []string{"a file.txt"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "--from0") {
		t.Errorf("scoped peer args missing --from0: %v", args)
	}
	if !slices.Contains(args, "--files-from="+filepath.Join(cfg.ConfigDir, "peer-plan-files.dyn")) {
		t.Errorf("scoped peer args missing plan list: %v", args)
	}
	for _, arg := range args {
		if arg == "--backup" || strings.HasPrefix(arg, "--backup-dir=") {
			t.Errorf("ordinary scoped peer args unexpectedly backup: %v", args)
		}
	}
	body, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "peer-plan-files.dyn"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "a file.txt\x00" {
		t.Errorf("plan list = %q, want NUL-delimited path", body)
	}
}

func TestPreparePeerPlanFilters_MaterializesIncludeRuntimeFile(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Profile = PeerProfile
	cfg.FilterMode = FilterModeInclude
	if err := PreparePeerPlanFilters(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.LocalPaths.TrackedDynFile); err != nil {
		t.Fatalf("tracked include runtime file was not materialized: %v", err)
	}
}

func TestInventoryPeer_DeniesVolatileButKeepsGraphifyOut(t *testing.T) {
	root := t.TempDir()
	paths := ResolveLocalPathsForProfile(root, PeerProfile)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatal(err)
	}
	for rel := range map[string]bool{
		".cache/a.bin":                        true,
		".maru/cache/a.bin":                   true,
		".maru/desk-pipeline/logs/run.log":    true,
		".maru/desk-pipeline/run.out":         true,
		".maru/desk-pipeline/candidates.json": true,
		".maru/runs/skills/run/events.jsonl":  true,
		"test-results/result.txt":             true,
		"playwright-report/index.html":        true,
		".astro/types.d.ts":                   true,
		"build/app.tsbuildinfo":               true,
		"scratchpad/temp/session.log":         true,
		"docs/.metadata_never_index":          true,
		"analysis/graphify-out/graph.json":    true,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &Config{
		Profile:      PeerProfile,
		LocalPath:    root + "/",
		MirrorPath:   root + "/mirror/",
		FilterMode:   FilterModeExclude,
		ExcludesFile: paths.ExcludeFile,
		IgnoreFile:   paths.IgnoreFile,
		AllowFile:    paths.AllowFile,
		LocalPaths:   paths,
		ConfigDir:    paths.StoreDir,
	}
	got, err := InventoryPeer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["analysis/graphify-out/graph.json"]; !ok {
		t.Errorf("graphify-out was denied: %v", got)
	}
	if _, ok := got[".maru/desk-pipeline/candidates.json"]; !ok {
		t.Errorf("desk-pipeline candidates were denied: %v", got)
	}
	for rel := range got {
		if IsPeerVolatile(rel) {
			t.Errorf("volatile path escaped inventory: %q", rel)
		}
	}
}

func TestDeletePeerLocal_RefusesQuarantineSymlink(t *testing.T) {
	root := t.TempDir()
	paths := ResolveLocalPathsForProfile(root, PeerProfile)
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(payload, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, conflictsDirName)); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Profile:    PeerProfile,
		LocalPath:  root + "/",
		LocalPaths: paths,
	}
	err := DeletePeerLocal(cfg, &ConflictDir{Timestamp: "ts"}, []string{"gone.txt"}, false)
	if err == nil {
		t.Fatal("DeletePeerLocal followed a quarantine symlink")
	}
	if _, statErr := os.Stat(payload); statErr != nil {
		t.Fatalf("source payload was moved after refused quarantine: %v", statErr)
	}
	if entries, readErr := os.ReadDir(outside); readErr != nil {
		t.Fatal(readErr)
	} else if len(entries) != 0 {
		t.Fatalf("quarantine symlink target was modified: %v", entries)
	}
}
