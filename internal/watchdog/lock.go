package watchdog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// passLockStaleAfter bounds a lock left behind by a pass that died between
// acquire and release: older than this, the lock is broken and reclaimed. A
// pass runs in seconds, so 15 minutes cannot be a live one.
const passLockStaleAfter = 15 * time.Minute

// AcquireReapLock serializes reap passes (scheduled and manual) so the
// load-modify-save cycle on samples.json cannot interleave and silently
// drop one pass's history. busy=true means another pass holds it; the
// caller skips its run (the next interval retries) and must not error.
func AcquireReapLock(stateDir string) (release func(), busy bool, err error) {
	return acquirePassLock(stateDir, "reap.lock")
}

// AcquireWarpLock serializes warp heal passes (the root daemon and manual
// runs) for the same reason as AcquireReapLock: warp.json is a
// load-modify-save cycle shared by both writers.
func AcquireWarpLock(stateDir string) (release func(), busy bool, err error) {
	return acquirePassLock(stateDir, "warp.lock")
}

// acquirePassLock takes a mkdir lock named name inside stateDir, reclaiming
// it when it is stale. The lock is a directory, atomic on every filesystem
// dot supports.
func acquirePassLock(stateDir, name string) (release func(), busy bool, err error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, false, fmt.Errorf("creating state dir: %w", err)
	}
	lockPath := filepath.Join(stateDir, name)
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Mkdir(lockPath, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, false, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, false, fmt.Errorf("acquiring %s: %w", name, err)
		}
		info, statErr := os.Lstat(lockPath)
		if statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue // released between mkdir and stat
			}
			return nil, false, fmt.Errorf("checking %s: %w", name, statErr)
		}
		if time.Since(info.ModTime()) <= passLockStaleAfter {
			return nil, true, nil
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("reclaiming stale %s: %w", name, err)
		}
	}
	return nil, true, nil
}
