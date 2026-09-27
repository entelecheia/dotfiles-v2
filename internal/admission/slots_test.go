package admission

import (
	"context"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	runner := exec.NewRunner(false, slog.Default())
	return NewStore(filepath.Join(t.TempDir(), "admission"), runner)
}

func selfLease(t *testing.T, store *Store) Lease {
	t.Helper()
	started := ""
	if res, err := store.Runner.RunQuery(context.Background(), "ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid())); err == nil {
		started = strings.Join(strings.Fields(res.Stdout), " ")
	}
	if started == "" {
		t.Skip("ps lstart unavailable; cannot test liveness-guarded slot behavior")
	}
	return Lease{Owner: "test@host", PID: os.Getpid(), PIDStart: started, PGID: os.Getpid(), CWD: "/tmp"}
}

func TestAcquireContentionAndRelease(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	lease := selfLease(t, store)

	slot, holder, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, lease)
	if err != nil || slot == nil || holder != nil {
		t.Fatalf("first acquire = %v, %v, %v", slot, holder, err)
	}
	// Same repo, second contender: deferred with the owner visible.
	got, holder, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, Lease{Owner: "other@host", PID: 4242})
	if err != nil || got != nil {
		t.Fatalf("contending acquire = %v, %v", got, err)
	}
	if holder == nil || holder.Owner != "test@host" || holder.PID != os.Getpid() {
		t.Errorf("holder = %+v, want the first owner visible", holder)
	}
	if err := slot.Release(); err != nil {
		t.Fatal(err)
	}
	got, holder, err = store.Acquire(ctx, "/repo/a/.git", ClassHeavy, Lease{Owner: "other@host", PID: 4242})
	if err != nil || got == nil {
		t.Fatalf("acquire after release = %v, %v, %v", got, holder, err)
	}
}

func TestCrossRepoParallelAndMaintenanceSerial(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	lease := selfLease(t, store)

	slotA, _, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, lease)
	if err != nil || slotA == nil {
		t.Fatalf("repo A acquire = %v, %v", slotA, err)
	}
	slotB, holderB, err := store.Acquire(ctx, "/repo/b/.git", ClassHeavy, lease)
	if err != nil || slotB == nil || holderB != nil {
		t.Fatalf("repo B must admit in parallel: %v, %v, %v", slotB, holderB, err)
	}
	m1, _, err := store.Acquire(ctx, MaintenanceScope, ClassMaintenance, lease)
	if err != nil || m1 == nil {
		t.Fatalf("maintenance acquire = %v, %v", m1, err)
	}
	m2, holderM, err := store.Acquire(ctx, MaintenanceScope, ClassMaintenance, lease)
	if err != nil || m2 != nil || holderM == nil {
		t.Errorf("maintenance slot must serialize across repos: %v, %v, %v", m2, holderM, err)
	}
}

// deadPIDLease returns a lease whose pid is guaranteed gone (a process that
// already exited) with an already-expired deadline.
func deadPIDLease(t *testing.T, store *Store) Lease {
	t.Helper()
	cmd := osexec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot spawn a probe process")
	}
	past := store.now().Add(-2 * store.HeartbeatStaleAfter())
	return Lease{
		Owner: "dead@host", PID: cmd.Process.Pid, PIDStart: "Sun Sep 27 10:00:00 2026",
		AcquiredAt: past, HeartbeatAt: past, Deadline: past,
	}
}

func TestStaleOwnerCrashRecovery(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	dead := deadPIDLease(t, store)

	// Occupy the slot as the dead owner.
	dir := store.slotDir("/repo/a/.git", ClassHeavy)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLease(dir, dead); err != nil {
		t.Fatal(err)
	}
	// A live contender reclaims it: owner crashed, heartbeat expired.
	lease := selfLease(t, store)
	slot, holder, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, lease)
	if err != nil || slot == nil {
		t.Fatalf("reclaim acquire = %v, %v, %v", slot, holder, err)
	}
	if got := slot.Lease(); got.PID != lease.PID {
		t.Errorf("reclaimed lease pid = %d, want %d", got.PID, lease.PID)
	}
}

// TestPIDReuseNotForceFreed pins the conservative branch: a fresh heartbeat
// is never reclaimed, whatever the pid situation looks like.
func TestPIDReuseNotForceFreed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	dead := deadPIDLease(t, store)
	dead.Deadline = store.now().Add(time.Minute) // fresh heartbeat
	dead.HeartbeatAt = store.now()

	dir := store.slotDir("/repo/a/.git", ClassHeavy)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLease(dir, dead); err != nil {
		t.Fatal(err)
	}
	got, holder, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, Lease{Owner: "new@host", PID: 7})
	if err != nil || got != nil {
		t.Fatalf("fresh-heartbeat acquire = %v, %v", got, err)
	}
	if holder == nil || holder.Owner != "dead@host" {
		t.Errorf("holder = %+v, want the original owner reported", holder)
	}
}

// TestLiveOwnerStalledHeartbeatNotReclaimed: the heartbeat expired but the
// owning incarnation is verifiably alive — freeing it would admit a second
// heavy job next to a live one.
func TestLiveOwnerStalledHeartbeatNotReclaimed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	lease := selfLease(t, store) // own live pid + real lstart
	past := store.now().Add(-2 * store.HeartbeatStaleAfter())
	lease.HeartbeatAt = past
	lease.Deadline = past
	lease.AcquiredAt = past

	dir := store.slotDir("/repo/a/.git", ClassHeavy)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLease(dir, lease); err != nil {
		t.Fatal(err)
	}
	got, holder, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, Lease{Owner: "new@host", PID: 7})
	if err != nil || got != nil || holder == nil {
		t.Errorf("stalled-but-alive owner = %v, %v, %v; want busy", got, holder, err)
	}
}

func TestHeartbeatAndReleaseOwnershipGuard(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	lease := selfLease(t, store)
	slot, _, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, lease)
	if err != nil || slot == nil {
		t.Fatalf("acquire = %v, %v", slot, err)
	}
	if err := slot.Heartbeat(); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	current, err := readLease(store.slotDir("/repo/a/.git", ClassHeavy))
	if err != nil {
		t.Fatal(err)
	}
	if !current.Deadline.After(current.HeartbeatAt) {
		t.Errorf("deadline %v not after heartbeat %v", current.Deadline, current.HeartbeatAt)
	}

	// Simulate a reclaim + re-acquire by someone else: both Heartbeat and
	// Release must refuse to touch it.
	foreign := lease
	foreign.PID = lease.PID + 1000
	foreign.AcquiredAt = store.now().Add(time.Second)
	if err := writeLease(store.slotDir("/repo/a/.git", ClassHeavy), foreign); err != nil {
		t.Fatal(err)
	}
	if err := slot.Heartbeat(); err == nil {
		t.Error("heartbeat after slot changed hands must fail")
	}
	if err := slot.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.slotDir("/repo/a/.git", ClassHeavy)); err != nil {
		t.Error("release after slot changed hands must not remove the new owner's slot")
	}
}

func TestCorruptLeaseReclaimBound(t *testing.T) {
	store := testStore(t)
	dir := store.slotDir("/repo/a/.git", ClassHeavy)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lease.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Fresh directory: corrupt lease cannot prove ownership, but the slot is
	// not yet old enough to reclaim.
	got, _, err := store.Acquire(context.Background(), "/repo/a/.git", ClassHeavy, Lease{Owner: "new@host", PID: 7})
	if err != nil || got != nil {
		t.Errorf("fresh corrupt slot = %v, %v; want busy", got, err)
	}
	// Aged past the stale bound: reclaimable.
	past := store.now().Add(-2 * store.HeartbeatStaleAfter())
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
	got, _, err = store.Acquire(context.Background(), "/repo/a/.git", ClassHeavy, Lease{Owner: "new@host", PID: 7})
	if err != nil || got == nil {
		t.Errorf("stale corrupt slot = %v, %v; want reclaimed", got, err)
	}
}

func TestNestedScope(t *testing.T) {
	env := NestedEnvValue("/repo/a/.git", ClassHeavy)
	if !NestedScope(env, "/repo/a/.git", ClassHeavy) {
		t.Error("same scope+class not detected as nested")
	}
	if NestedScope(env, "/repo/b/.git", ClassHeavy) {
		t.Error("different scope detected as nested")
	}
	if NestedScope(env, "/repo/a/.git", ClassMaintenance) {
		t.Error("different class detected as nested")
	}
	if NestedScope("{garbage", "/repo/a/.git", ClassHeavy) || NestedScope("", "/repo/a/.git", ClassHeavy) {
		t.Error("garbage/empty env detected as nested")
	}
}

func TestHistoryRoundTripAndGate(t *testing.T) {
	store := testStore(t)
	if _, err := LoadHistory(store.HistoryPath()); err != nil {
		t.Fatalf("missing history must be empty, got %v", err)
	}
	fixed := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	monitor := &Monitor{
		GOOS: "darwin",
		Now:  func() time.Time { return fixed },
		SnapshotFunc: func(context.Context, *Monitor) PressureSnapshot {
			return PressureSnapshot{
				Platform: "darwin", MemoryLevel: MemoryWarn, MemoryAvailable: true,
				ThermalCPULimit: 100, ThermalAvailable: true,
				Load1: 1, NumCPU: 10, LoadAvailable: true,
				IdlePct: 90, IdleAvailable: true, WSScanOK: true,
			}
		},
	}
	d, err := store.Gate(context.Background(), monitor, DefaultThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if d.Admit {
		t.Error("gate admitted under memory warn")
	}
	hist, err := LoadHistory(store.HistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	if !hist.DeferActive || !hist.DeferSince.Equal(fixed) {
		t.Errorf("persisted history = %+v, want episode opened at %v", hist, fixed)
	}
}

func TestNotifyDedup(t *testing.T) {
	store := testStore(t)
	episode := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if !store.ShouldNotify("/repo/a/.git", ClassHeavy, episode) {
		t.Error("first defer of the episode must notify")
	}
	if err := store.MarkNotified("/repo/a/.git", ClassHeavy, episode); err != nil {
		t.Fatal(err)
	}
	if store.ShouldNotify("/repo/a/.git", ClassHeavy, episode) {
		t.Error("second defer of the same episode must not notify")
	}
	if !store.ShouldNotify("/repo/a/.git", ClassHeavy, episode.Add(time.Hour)) {
		t.Error("a new episode must notify again")
	}
}

func TestListLeases(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	lease := selfLease(t, store)
	if _, _, err := store.Acquire(ctx, "/repo/a/.git", ClassHeavy, lease); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Acquire(ctx, MaintenanceScope, ClassMaintenance, lease); err != nil {
		t.Fatal(err)
	}
	owners, err := store.ListLeases()
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 {
		t.Errorf("owners = %d, want 2", len(owners))
	}
	classes := map[string]bool{}
	for _, o := range owners {
		classes[o.Class] = true
	}
	if !classes[ClassHeavy] || !classes[ClassMaintenance] {
		t.Errorf("classes = %v, want heavy and maintenance", classes)
	}
}
