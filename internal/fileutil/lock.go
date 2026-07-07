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

// pidlessStaleAfter bounds how long a lock directory without a readable
// lock.pid is honored before it is considered abandoned. It covers three
// cases with one conservative horizon:
//   - the brief window between os.Mkdir(lockDir) and writeLockPID in a live
//     acquirer (TOCTOU during acquisition),
//   - bare-directory locks created by pre-pid dot versions, which may belong
//     to a still-running legacy sync (must not be reclaimed after seconds),
//   - lock.pid files this user cannot read (e.g. left root-owned by a sudo
//     run), which previously blocked every future sync forever.
//
// ponytail: syncs longer than this horizon can still be raced by a reclaim;
// shrink only with a handshake that upgrades legacy locks in place.
const pidlessStaleAfter = time.Hour

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
		// Reclaim by renaming the stale dir aside first: rename is atomic, so
		// when several contenders judge the same lock stale only one rename
		// succeeds. A plain RemoveAll here could delete a lock that a faster
		// contender had already recreated and pid-stamped.
		trash := fmt.Sprintf("%s.stale.%d", lockDir, os.Getpid())
		if err := os.Rename(lockDir, trash); err == nil {
			_ = os.RemoveAll(trash)
		}
		if err := os.Mkdir(lockDir, 0755); err != nil {
			if os.IsExist(err) {
				// Another contender won the recreate race — the lock is held.
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
// A lock whose pid cannot be read — file missing (mid-acquisition or a legacy
// bare-directory lock) or unreadable (e.g. root-owned after a sudo run) — is
// honored until the lock directory has outlived pidlessStaleAfter, then
// treated as abandoned so it self-heals instead of blocking forever.
func PIDLockIsStale(lockDir string) bool {
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		return lockDirOlderThan(lockDir, pidlessStaleAfter)
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
