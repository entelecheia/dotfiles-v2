package fileutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
