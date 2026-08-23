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

// defaultLockLabel is the noun every refusal carried before locks had
// labels. LockOptions.Label falls back to it so the syncer's
// operator-visible wording does not move.
const defaultLockLabel = "another sync is running"

// ErrLockHeld marks a refusal caused by contention: the lock is held by
// someone else and the caller may reasonably back off. Match it with
// errors.Is. A failure to create the lock parent or to write the pid file
// deliberately does NOT carry it, so a caller that treats contention as a
// quiet no-op cannot swallow a real failure the same way.
var ErrLockHeld = errors.New("lock held")

// LockOptions tunes one AcquirePIDLock call. Both zero values preserve the
// behavior every caller had before this struct existed, which is what lets
// a caller adopt it one field at a time.
type LockOptions struct {
	// Label names the holder in the refusal message, in the repo's
	// labeled-error shape: name first, the observed value in parentheses.
	// The zero value falls back to "another sync is running".
	Label string

	// StaleAfter bounds how long a lock directory with no readable lock.pid
	// is honored before it is treated as abandoned. It governs pid-less
	// locks only: a lock whose pid points at a live process is held no
	// matter how short this is. The zero value falls back to the
	// package-level one-hour horizon.
	StaleAfter time.Duration
}

// lockHeldError is the contention refusal. It unwraps to ErrLockHeld so a
// caller can classify it, while Error() keeps the exact wording the
// operator already knows.
type lockHeldError struct {
	label   string
	lockDir string
}

func (e *lockHeldError) Error() string { return fmt.Sprintf("%s (lock: %s)", e.label, e.lockDir) }
func (e *lockHeldError) Unwrap() error { return ErrLockHeld }

// AcquirePIDLock creates a directory lock with a lock.pid file inside.
//
// The directory create (os.Mkdir) is the atomic gate: the process that
// creates lockDir owns the lock and immediately records its pid. On EEXIST,
// the existing lock is inspected against opts.StaleAfter — a lock whose pid
// points at a dead process (or whose pid file has been missing past that
// window) is reclaimed exactly once.
func AcquirePIDLock(lockDir string, opts LockOptions) (func(), error) {
	label := opts.Label
	if label == "" {
		label = defaultLockLabel
	}
	if err := os.MkdirAll(filepath.Dir(lockDir), 0755); err != nil {
		return nil, fmt.Errorf("preparing lock parent: %w", err)
	}
	if err := os.Mkdir(lockDir, 0755); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating lock: %w", err)
		}
		if !pidLockIsStale(lockDir, opts.StaleAfter) {
			return nil, &lockHeldError{label: label, lockDir: lockDir}
		}
		// Reclaim by renaming the stale dir aside first: rename is atomic, so
		// when several contenders judge the same lock stale only one rename
		// succeeds. A plain RemoveAll here could delete a lock that a faster
		// contender had already recreated and pid-stamped.
		trash := fmt.Sprintf("%s.stale.%d", lockDir, os.Getpid())
		// A leftover trash dir from an earlier run that shared this pid would
		// make the rename fail forever. It is ours by construction, so clear
		// it first rather than letting it wedge the reclaim.
		_ = os.RemoveAll(trash)
		if err := os.Rename(lockDir, trash); err != nil {
			// Only a vanished source is the benign race: a faster contender
			// already reclaimed the stale lock, so fall through and let Mkdir
			// decide. Anything else — no write permission on the parent, a
			// read-only mount — is a real failure and MUST surface. Ignoring
			// it here would make Mkdir report EEXIST, which classifies as
			// ErrLockHeld, which a scheduled run swallows into exit 0. A
			// permission problem would then be an indefinite silent no-op.
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("reclaiming stale lock %s: %w", lockDir, err)
			}
		} else {
			_ = os.RemoveAll(trash)
		}
		if err := os.Mkdir(lockDir, 0755); err != nil {
			if os.IsExist(err) {
				// Another contender won the recreate race — the lock is held.
				return nil, &lockHeldError{label: label, lockDir: lockDir}
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
// a process that no longer exists, against the package-level horizon.
//
// A lock whose pid cannot be read — file missing (mid-acquisition or a legacy
// bare-directory lock) or unreadable (e.g. root-owned after a sudo run) — is
// honored until the lock directory has outlived pidlessStaleAfter, then
// treated as abandoned so it self-heals instead of blocking forever.
//
// Callers that need their own window pass LockOptions.StaleAfter to
// AcquirePIDLock instead; this signature is what syncer.lockIsStale reads
// for its status report, which has no window of its own.
func PIDLockIsStale(lockDir string) bool {
	return pidLockIsStale(lockDir, 0)
}

// pidLockIsStale is PIDLockIsStale against a caller-supplied pid-less
// horizon. A zero staleAfter means the package default.
func pidLockIsStale(lockDir string, staleAfter time.Duration) bool {
	if staleAfter <= 0 {
		staleAfter = pidlessStaleAfter
	}
	data, err := os.ReadFile(filepath.Join(lockDir, "lock.pid"))
	if err != nil {
		return lockDirOlderThan(lockDir, staleAfter)
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
