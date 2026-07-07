package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidWriteGrace bounds how long a freshly created lock directory may exist
// without a readable lock.pid before it is considered abandoned. It covers
// the brief window between os.Mkdir(lockDir) and writeLockPID in a live
// acquirer, closing the TOCTOU race where a second process would otherwise
// treat the pid-less directory as stale and reclaim a lock that is actively
// being taken.
const pidWriteGrace = 5 * time.Second

// AcquirePIDLock creates a directory lock with a lock.pid file inside.
//
// The directory create (os.Mkdir) is the atomic gate: the process that
// creates lockDir owns the lock and immediately records its pid. On EEXIST,
// the existing lock is inspected via PIDLockIsStale — a lock whose pid points
// at a dead process (or whose pid file has been missing past pidWriteGrace)
// is reclaimed exactly once.
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
			if os.IsExist(err) {
				// Another process won the reclaim race between our RemoveAll
				// and Mkdir — treat the lock as held rather than clobbering it.
				return nil, fmt.Errorf("another sync is running (lock: %s)", lockDir)
			}
			return nil, fmt.Errorf("recreating lock after stale cleanup: %w", err)
		}
	}
	if err := writeLockPID(lockDir); err != nil {
		_ = os.RemoveAll(lockDir)
		return nil, err
	}
	return func() { _ = os.RemoveAll(lockDir) }, nil
}

// PIDLockIsStale reports whether lockDir's lock.pid is malformed or points at
// a process that no longer exists.
//
// A missing lock.pid is treated as held while the lock directory is younger
// than pidWriteGrace (a live acquirer may be mid-write); only once the
// directory has outlived the grace period without a pid is it considered
// abandoned. A pid file that cannot be read for other reasons (e.g. a
// permission error) is treated as held — we never reclaim a lock we cannot
// inspect.
func PIDLockIsStale(lockDir string) bool {
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		if os.IsNotExist(err) {
			return lockDirOlderThan(lockDir, pidWriteGrace)
		}
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return true
	}
	return !processAlive(pid)
}

// processAlive reports whether pid names a live process. It distinguishes
// "no such process" (dead) from "exists but not signalable by us" (EPERM,
// e.g. a process owned by another user) so a lock held by a live process is
// never mistaken for stale.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrProcessDone):
		return false
	default:
		// EPERM or any other error: the process exists (or its state is
		// unknown) — err on the side of leaving the lock held.
		return true
	}
}

// lockDirOlderThan reports whether lockDir's mtime is further in the past than
// d. A directory that cannot be stat'd is treated as older (nothing to protect).
func lockDirOlderThan(lockDir string, d time.Duration) bool {
	info, err := os.Stat(lockDir)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > d
}

func writeLockPID(lockDir string) error {
	pidFile := filepath.Join(lockDir, "lock.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return fmt.Errorf("writing lock pid: %w", err)
	}
	return nil
}
