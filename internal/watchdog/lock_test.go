package watchdog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireReapLock_SerializesAndReleases(t *testing.T) {
	dir := t.TempDir()

	release, busy, err := AcquireReapLock(dir)
	if err != nil || busy {
		t.Fatalf("first acquire = busy %v, err %v", busy, err)
	}
	if _, busy, err := AcquireReapLock(dir); err != nil || !busy {
		t.Fatalf("second acquire while held = busy %v, err %v; want busy", busy, err)
	}
	release()
	if _, busy, err := AcquireReapLock(dir); err != nil || busy {
		t.Fatalf("acquire after release = busy %v, err %v", busy, err)
	}
}

func TestAcquireReapLock_ReclaimsStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "reap.lock")
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-reapLockStaleAfter - time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	release, busy, err := AcquireReapLock(dir)
	if err != nil || busy {
		t.Fatalf("stale lock must be reclaimed: busy %v, err %v", busy, err)
	}
	release()
}
