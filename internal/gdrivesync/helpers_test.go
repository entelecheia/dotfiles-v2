package gdrivesync

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireLock_FailsWhenLiveLockHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "live.lock")

	release1, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer release1()

	// Second call must refuse.
	release2, err := AcquireLock(dir)
	if err == nil {
		release2()
		t.Fatal("AcquireLock did not refuse a held lock")
	}
	if !strings.Contains(err.Error(), "another sync") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAcquireLock_BreaksStaleLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stale.lock")

	// Pre-seed a "lock" with a guaranteed-dead PID. PID 0 is reserved
	// (init/swapper) and signal-0 to it returns EPERM not ESRCH on most
	// systems, so use a high unlikely-to-exist PID instead.
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	pidFile := filepath.Join(dir, "lock.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(99999999)), 0644); err != nil {
		t.Fatalf("seed pid file: %v", err)
	}

	release, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock did not break stale lock: %v", err)
	}
	defer release()

	// Lock should now belong to the current process.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading new pid file: %v", err)
	}
	gotPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parsing pid: %v", err)
	}
	if gotPID != os.Getpid() {
		t.Errorf("new lock PID = %d, want %d (current process)", gotPID, os.Getpid())
	}
}

func TestAcquireLock_BreaksLockWithMissingPIDFile(t *testing.T) {
	// A lock dir with no lock.pid is treated as stale (corrupted state).
	dir := filepath.Join(t.TempDir(), "noPID.lock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	release, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock should treat missing PID as stale: %v", err)
	}
	release()
}

func TestAcquireLock_ReleaseRemovesLockDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "release.lock")

	release, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("lock dir should exist after acquire: %v", err)
	}
	release()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("lock dir should be gone after release; stat err = %v", err)
	}
}

func TestNewTimestamp_FilesystemSafe(t *testing.T) {
	ts := newTimestamp()
	if strings.Contains(ts, ":") {
		t.Errorf("timestamp must not contain colons: %q", ts)
	}
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("timestamp should be UTC (suffix Z): %q", ts)
	}
}

func TestRotateLog_TrimsExcess(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "x.log")

	// Write 100 lines, rotate keeping last 10 when over 50.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	if err := os.WriteFile(logFile, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	RotateLog(logFile, 50, 10)

	got, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	keptLines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(keptLines) != 10 {
		t.Errorf("RotateLog kept %d lines, want 10", len(keptLines))
	}
	if keptLines[0] != "line 90" {
		t.Errorf("RotateLog kept wrong tail: first=%q want %q", keptLines[0], "line 90")
	}
}

func TestRotateLog_NoOpUnderThreshold(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "x.log")

	original := "a\nb\nc\n"
	if err := os.WriteFile(logFile, []byte(original), 0644); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	RotateLog(logFile, 50, 10)
	got, _ := os.ReadFile(logFile)
	if string(got) != original {
		t.Errorf("RotateLog modified file under threshold: got %q want %q", got, original)
	}
}
