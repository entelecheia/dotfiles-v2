package fileutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAcquirePIDLock_Lifecycle(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "sync.lock")
	release, err := AcquirePIDLock(lockDir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquirePIDLock(lockDir); err == nil {
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
	release, err := AcquirePIDLock(lockDir)
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
	if _, err := AcquirePIDLock(lockDir); err == nil {
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
	release, err := AcquirePIDLock(lockDir)
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
