package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// AcquirePIDLock creates a directory lock with a lock.pid file inside.
//
// On EEXIST, it probes the recorded PID with signal 0. Dead, missing, or
// malformed PID files are treated as stale and cleaned up before one retry.
func AcquirePIDLock(lockDir string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockDir), 0755); err != nil {
		return nil, fmt.Errorf("preparing lock parent: %w", err)
	}
	if err := os.Mkdir(lockDir, 0755); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating lock: %w", err)
		}
		if !PIDLockIsStale(lockDir) {
			return nil, fmt.Errorf("another sync is running (lock: %s)", lockDir)
		}
		_ = os.RemoveAll(lockDir)
		if err := os.Mkdir(lockDir, 0755); err != nil {
			return nil, fmt.Errorf("recreating lock after stale cleanup: %w", err)
		}
	}
	if err := writeLockPID(lockDir); err != nil {
		_ = os.RemoveAll(lockDir)
		return nil, err
	}
	return func() { _ = os.RemoveAll(lockDir) }, nil
}

// PIDLockIsStale reports whether lockDir's lock.pid is absent, malformed, or
// points at a process that no longer exists.
func PIDLockIsStale(lockDir string) bool {
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	return proc.Signal(syscall.Signal(0)) != nil
}

func writeLockPID(lockDir string) error {
	pidFile := filepath.Join(lockDir, "lock.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("writing lock pid: %w", err)
	}
	return nil
}
