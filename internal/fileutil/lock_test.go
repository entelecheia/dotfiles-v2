package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAcquirePIDLock_Lifecycle(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	release, err := AcquirePIDLock(lockDir, LockOptions{})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquirePIDLock(lockDir, LockOptions{}); err == nil {
		t.Fatal("second acquire should fail while lock is held")
	}
	release()
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("lock dir still exists after release: %v", err)
	}
}

func TestAcquirePIDLock_ReclaimsDeadPID(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "lock.pid"), []byte("99999999\n"), 0644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	release, err := AcquirePIDLock(lockDir, LockOptions{})
	if err != nil {
		t.Fatalf("acquire stale lock: %v", err)
	}
	defer release()
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock pid was not refreshed: %q", data)
	}
}

func TestPIDLockIsStale_LivePIDHeld(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	// The test process itself is alive and signalable — must be reported held.
	if err := os.WriteFile(filepath.Join(lockDir, "lock.pid"), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if PIDLockIsStale(lockDir) {
		t.Fatal("live pid lock should not be reported stale")
	}
}

func TestPIDLockIsStale_MalformedPID(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "lock.pid"), []byte("not-a-pid"), 0644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if !PIDLockIsStale(lockDir) {
		t.Fatal("malformed pid lock should be reported stale")
	}
}

// A lock directory with no pid file must be treated as held while it is still
// within the write grace window — this is the TOCTOU window between Mkdir and
// writeLockPID in a live acquirer.
func TestPIDLockIsStale_MissingPIDWithinGraceHeld(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	if PIDLockIsStale(lockDir) {
		t.Fatal("freshly created pid-less lock should be held during grace window")
	}
	if _, err := AcquirePIDLock(lockDir, LockOptions{}); err == nil {
		t.Fatal("acquire should fail while a pid-less lock is within its grace window")
	}
}

// Once the pid-less lock directory has outlived the grace window it is
// abandoned and may be reclaimed.
func TestPIDLockIsStale_MissingPIDAfterGraceReclaimed(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	old := time.Now().Add(-2 * pidlessStaleAfter)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatalf("backdate lock dir: %v", err)
	}
	if !PIDLockIsStale(lockDir) {
		t.Fatal("pid-less lock older than grace window should be stale")
	}
	release, err := AcquirePIDLock(lockDir, LockOptions{})
	if err != nil {
		t.Fatalf("acquire abandoned lock: %v", err)
	}
	defer release()
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock pid was not written after reclaim: %q", data)
	}
}

// An unreadable lock.pid (e.g. left root-owned by a sudo run) must be honored
// while fresh but reclaimed once the lock outlives the pid-less horizon —
// otherwise every future sync is blocked until manual cleanup.
func TestPIDLockIsStale_UnreadablePIDAgesOut(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("file modes do not restrict root")
	}
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	pidFile := filepath.Join(lockDir, "lock.pid")
	if err := os.WriteFile(pidFile, []byte("12345"), 0o000); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if PIDLockIsStale(lockDir) {
		t.Fatal("fresh unreadable-pid lock should be held")
	}
	old := time.Now().Add(-2 * pidlessStaleAfter)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatalf("backdate lock dir: %v", err)
	}
	if !PIDLockIsStale(lockDir) {
		t.Fatal("aged unreadable-pid lock should be stale")
	}
}

// TestAcquirePIDLock_RefusalNamesTheCaller is BUG-04's label clause. The
// zero-value Label must reproduce the sync-specific noun byte for byte:
// every syncer refusal the operator already knows goes through it, and this
// change must not move that wording.
func TestAcquirePIDLock_RefusalNamesTheCaller(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  LockOptions
		wantf string
	}{
		{
			name:  "zero label keeps the syncer wording",
			opts:  LockOptions{},
			wantf: "another sync is running (lock: %s)",
		},
		{
			name:  "caller label replaces the sync noun",
			opts:  LockOptions{Label: "another dot write to the claude settings is running"},
			wantf: "another dot write to the claude settings is running (lock: %s)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "sync.lock")
			release, err := AcquirePIDLock(lockDir, tc.opts)
			if err != nil {
				t.Fatalf("first acquire: %v", err)
			}
			defer release()

			_, err = AcquirePIDLock(lockDir, tc.opts)
			if err == nil {
				t.Fatal("second acquire should be refused while the lock is held")
			}
			want := fmt.Sprintf(tc.wantf, lockDir)
			if err.Error() != want {
				t.Fatalf("refusal = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestAcquirePIDLock_ContentionIsDistinguishableFromFailure keeps a caller
// from mistaking a real failure for contention: the scheduled-run path
// swallows contention, so a create failure carrying the sentinel would turn
// a broken lock parent into a silent no-op.
func TestAcquirePIDLock_ContentionIsDistinguishableFromFailure(t *testing.T) {
	held := filepath.Join(t.TempDir(), "sync.lock")
	release, err := AcquirePIDLock(held, LockOptions{})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()
	if _, err := AcquirePIDLock(held, LockOptions{}); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("contention must satisfy errors.Is(err, ErrLockHeld): %v", err)
	}

	// A regular file where the lock parent should be: MkdirAll fails, which
	// is a genuine failure and must NOT look like contention.
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = AcquirePIDLock(filepath.Join(blocker, "sync.lock"), LockOptions{})
	if err == nil {
		t.Fatal("an unwritable lock parent must fail")
	}
	if errors.Is(err, ErrLockHeld) {
		t.Fatalf("a create failure must not carry the contention sentinel: %v", err)
	}
}

// TestAcquirePIDLock_ZeroStaleAfterKeepsTheHourHorizon pins the zero-value
// fallback at the boundary, so "zero means today's behaviour" is measured
// rather than asserted in a doc comment.
func TestAcquirePIDLock_ZeroStaleAfterKeepsTheHourHorizon(t *testing.T) {
	justUnder := backdatedPIDLessLock(t, pidlessStaleAfter-time.Minute)
	if _, err := AcquirePIDLock(justUnder, LockOptions{}); err == nil {
		t.Fatal("a pid-less lock just inside the default horizon must still be honored")
	}

	justOver := backdatedPIDLessLock(t, pidlessStaleAfter+time.Minute)
	release, err := AcquirePIDLock(justOver, LockOptions{})
	if err != nil {
		t.Fatalf("a pid-less lock just past the default horizon must be reclaimed: %v", err)
	}
	release()
}

// TestAcquirePIDLock_ShortStaleAfterReclaims is the clause that stops a
// stale root-owned ~/.claude lock from blocking dot ai hud and dot guard for
// an hour: a settings mutation is a sub-second operation, so its caller
// declares a minutes-scale horizon of its own.
func TestAcquirePIDLock_ShortStaleAfterReclaims(t *testing.T) {
	lockDir := backdatedPIDLessLock(t, 2*time.Minute)
	release, err := AcquirePIDLock(lockDir, LockOptions{StaleAfter: time.Minute})
	if err != nil {
		t.Fatalf("a lock older than the caller's own window must be reclaimed: %v", err)
	}
	defer release()
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock pid was not written after reclaim: %q", data)
	}
}

// TestAcquirePIDLock_LivePIDHeldRegardlessOfWindow: shortening the window
// must not reclaim a lock somebody is actually holding. The window governs
// pid-less locks only.
func TestAcquirePIDLock_LivePIDHeldRegardlessOfWindow(t *testing.T) {
	lockDir := backdatedPIDLessLock(t, time.Hour)
	if err := os.WriteFile(filepath.Join(lockDir, "lock.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if _, err := AcquirePIDLock(lockDir, LockOptions{StaleAfter: time.Nanosecond}); err == nil {
		t.Fatal("a lock held by a live process must survive any window")
	}
}

// backdatedPIDLessLock builds a lock directory with no lock.pid whose mtime
// is age in the past.
func backdatedPIDLessLock(t *testing.T, age time.Duration) string {
	t.Helper()
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	old := time.Now().Add(-age)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatalf("backdate lock dir: %v", err)
	}
	return lockDir
}
