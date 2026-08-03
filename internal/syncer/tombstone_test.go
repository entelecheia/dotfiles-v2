package syncer

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	goexec "github.com/entelecheia/dotfiles-v2/internal/exec"
)

// tombstoneFixture builds a peer-shaped config whose local tree is a real
// temp dir, seeds the baseline with the given relpaths, and creates the
// subset that still exists on disk.
func tombstoneFixture(t *testing.T, baselineRels, onDiskRels []string) *Config {
	t.Helper()
	local := t.TempDir()
	cfg := newTestConfig(t)
	cfg.LocalPath = local + "/"
	cfg.Target = Target{Kind: TargetSSH, Host: "user@peer", Path: "/remote/work"}
	cfg.Propagation = PropagationPolicy{Create: true, Update: true, Delete: true}
	cfg.FilterMode = FilterModeExclude

	for _, rel := range onDiskRels {
		abs := filepath.Join(local, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("payload"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	baseline := map[string]Fingerprint{}
	for _, rel := range baselineRels {
		baseline[rel] = Fingerprint{Size: 7, Mtime: time.Now().UTC()}
	}
	if err := SaveBaselineManifest(cfg.LocalPaths.BaselineFile, baseline); err != nil {
		t.Fatalf("SaveBaselineManifest: %v", err)
	}
	return cfg
}

func TestComputeTombstones_InBaselineAndAbsentLocally(t *testing.T) {
	cfg := tombstoneFixture(t,
		[]string{"notes/keep.md", "inbox/drop/gone.csv"},
		[]string{"notes/keep.md"},
	)

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	want := []string{"inbox/drop/gone.csv"}
	if !slices.Equal(got, want) {
		t.Errorf("tombstones = %v, want %v", got, want)
	}
}

// A path the peer created is absent locally but was never in the baseline.
// Deleting it would destroy the peer's new work.
func TestComputeTombstones_NotInBaselineIsNotADeletion(t *testing.T) {
	cfg := tombstoneFixture(t, []string{"notes/keep.md"}, []string{"notes/keep.md"})

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tombstones = %v, want none — peer-only paths are not deletions", got)
	}
}

func TestComputeTombstones_RecreatedLocallyIsNotADeletion(t *testing.T) {
	cfg := tombstoneFixture(t,
		[]string{"notes/back.md"},
		[]string{"notes/back.md"},
	)

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tombstones = %v, want none — the path exists locally again", got)
	}
}

func TestComputeTombstones_SkipsFilteredPaths(t *testing.T) {
	cfg := tombstoneFixture(t,
		[]string{".maru/secrets/token.env", "notes/keep.md"},
		[]string{"notes/keep.md"},
	)

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	for _, rel := range got {
		if strings.HasPrefix(rel, ".maru/secrets/") {
			t.Errorf("tombstones leaked a filtered secret path: %v", got)
		}
	}
}

// Delete propagation is peer-only. For a mirror target the baseline records
// the mirror tree, so baseline-minus-local means something else entirely.
func TestComputeTombstones_RefusesNonSSHTarget(t *testing.T) {
	cfg := tombstoneFixture(t, []string{"gone.md"}, nil)
	cfg.Target = Target{Kind: TargetLocal, Path: "/tmp/mirror"}

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tombstones = %v, want none for a non-SSH target", got)
	}
}

func TestComputeTombstones_RefusesWhenDeleteOff(t *testing.T) {
	cfg := tombstoneFixture(t, []string{"gone.md"}, nil)
	cfg.Propagation = PropagationPolicy{Create: true, Update: true, Delete: false}

	got, err := ComputeTombstones(cfg)
	if err != nil {
		t.Fatalf("ComputeTombstones: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tombstones = %v, want none when propagation.delete is off", got)
	}
}

func TestDeletePassArgs_TargetsOnlyTheListedPaths(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Target = Target{Kind: TargetSSH, Host: "user@peer", Path: "/remote/work"}
	conflict := &ConflictDir{Timestamp: "ts"}

	args := deletePassArgs(cfg, conflict, "/tmp/list", false)

	for _, want := range []string{
		"--files-from=/tmp/list",
		"--from0",
		"--delete-missing-args",
		"--backup",
		"--backup-dir=" + conflict.PushBackupRel(),
	} {
		if !slices.Contains(args, want) {
			t.Errorf("deletePassArgs missing %q; got %v", want, args)
		}
	}
	// --delete would remove every peer path absent locally, not just the
	// listed ones. That is the failure this pass exists to avoid.
	for _, forbidden := range []string{"--delete", "--delete-after", "--delete-during", "--delete-excluded"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("deletePassArgs leaked %q — only listed paths may be removed; got %v", forbidden, args)
		}
	}
}

func TestDeletePassArgs_DryRunPropagates(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Target = Target{Kind: TargetSSH, Host: "user@peer", Path: "/remote/work"}

	args := deletePassArgs(cfg, &ConflictDir{Timestamp: "ts"}, "/tmp/list", true)

	if !slices.Contains(args, "--dry-run") {
		t.Errorf("dry-run deletePassArgs missing --dry-run; got %v", args)
	}
}

func TestWriteTombstoneList_IsNulDelimited(t *testing.T) {
	dir := t.TempDir()
	rels := []string{"inbox/drop/왕솬 chat.csv", "notes/a b.md"}

	path, err := writeTombstoneList(dir, rels)
	if err != nil {
		t.Fatalf("writeTombstoneList: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	if strings.Contains(string(raw), "\n") {
		t.Error("tombstone list contains a newline — names with spaces need NUL delimiting")
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if !slices.Equal(got, rels) {
		t.Errorf("list = %q, want %q", got, rels)
	}
}

func TestMaterializeTombstoneExcludes_AnchorsEachPath(t *testing.T) {
	dir := t.TempDir()

	path, err := MaterializeTombstoneExcludesFile(dir, []string{"inbox/drop/gone.csv"})
	if err != nil {
		t.Fatalf("MaterializeTombstoneExcludesFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read excludes: %v", err)
	}
	// Anchored at the transfer root, otherwise the pattern would also match a
	// same-named file elsewhere in the tree.
	if !strings.Contains(string(raw), "/inbox/drop/gone.csv\n") {
		t.Errorf("excludes file missing anchored path; got %q", raw)
	}
}

func TestPullArgs_ExcludesTombstonedPaths(t *testing.T) {
	cfg := newTestConfig(t)
	conflict := &ConflictDir{Timestamp: "ts"}
	rf := runtimeFilters{TombstonesDyn: "/tmp/tombstones.excl"}

	args := pullArgs(cfg, conflict, rf, false)

	if !slices.Contains(args, "--exclude-from=/tmp/tombstones.excl") {
		t.Errorf("pullArgs missing the tombstone exclude layer; got %v", args)
	}
	// Without this the pull restores the file before the delete pass can run,
	// which is the whole bug.
	idx := slices.Index(args, "--exclude-from=/tmp/tombstones.excl")
	trackedIdx := slices.Index(args, "--include-from="+rf.TrackedDyn)
	if trackedIdx != -1 && idx > trackedIdx {
		t.Error("tombstone excludes must precede the include layer — rsync is first-match-wins")
	}
}

func TestPullArgs_StillNeverDeletes(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Propagation = PropagationPolicy{Create: true, Update: true, Delete: true}

	args := pullArgs(cfg, &ConflictDir{Timestamp: "ts"}, runtimeFilters{}, false)

	for _, forbidden := range []string{"--delete", "--delete-after", "--delete-during", "--delete-missing-args"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("pullArgs leaked %q — the pull must never delete locally; got %v", forbidden, args)
		}
	}
}

// End-to-end against real rsync, with a local directory standing in for the
// peer. Proves the property the whole feature rests on: the listed path is
// removed and quarantined, and a path the peer created is left alone.
func TestPropagateDeletes_RemovesListedPathAndSparesPeerOnlyFile(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	root := t.TempDir()
	local := filepath.Join(root, "local")
	peer := filepath.Join(root, "peer")
	for _, d := range []string{local, filepath.Join(peer, "inbox", "drop")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Deleted locally, still on the peer.
	if err := os.WriteFile(filepath.Join(peer, "inbox", "drop", "gone.csv"), []byte("removed"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Created on the peer, never seen here. Must survive.
	if err := os.WriteFile(filepath.Join(peer, "inbox", "drop", "peer-new.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := newTestConfig(t)
	cfg.LocalPath = local + "/"
	cfg.MirrorPath = peer + "/"
	cfg.Target = Target{Kind: TargetLocal, Path: peer + "/"}
	cfg.LogFile = filepath.Join(root, "sync.log")
	cfg.RsyncPath = "rsync"

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := goexec.NewRunner(false, logger)
	conflict := &ConflictDir{Timestamp: "TS"}

	if err := PropagateDeletes(context.Background(), runner, cfg, conflict,
		[]string{"inbox/drop/gone.csv"}, false); err != nil {
		t.Fatalf("PropagateDeletes: %v", err)
	}

	if _, err := os.Stat(filepath.Join(peer, "inbox", "drop", "gone.csv")); !os.IsNotExist(err) {
		t.Error("tombstoned path still on the peer")
	}
	if _, err := os.Stat(filepath.Join(peer, "inbox", "drop", "peer-new.md")); err != nil {
		t.Errorf("peer-only file was destroyed: %v", err)
	}
	quarantined := filepath.Join(peer, conflictsDirName, "TS", "from-workspace", "inbox", "drop", "gone.csv")
	body, err := os.ReadFile(quarantined)
	if err != nil {
		t.Fatalf("expected quarantine copy at %s: %v", quarantined, err)
	}
	if string(body) != "removed" {
		t.Errorf("quarantined content = %q, want %q", body, "removed")
	}
}

func TestPropagateDeletes_RefusesOverCap(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.MaxDelete = 1
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := PropagateDeletes(context.Background(), goexec.NewRunner(false, logger), cfg,
		&ConflictDir{Timestamp: "TS"}, []string{"a", "b"}, false)

	if err == nil {
		t.Fatal("over-cap delete pass must refuse before touching the peer")
	}
}

func TestCheckTombstoneCap(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.MaxDelete = 2

	if err := checkTombstoneCap(cfg, []string{"a", "b"}); err != nil {
		t.Errorf("at the cap should pass, got %v", err)
	}
	err := checkTombstoneCap(cfg, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("over the cap must refuse — a broken filter can present the whole tree as deleted")
	}
	if !strings.Contains(err.Error(), "max_delete") {
		t.Errorf("error should name the knob to raise; got %v", err)
	}
}
