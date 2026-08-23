// Package claudecfg is the single owner of the ~/.claude/settings.json
// path, its lock, and its write. Every package that reads or writes that
// file goes through SettingsPath, Read, or Mutate; no other package joins
// a home directory with .claude and settings.json.
package claudecfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// ErrNoChange short-circuits Mutate without writing. An edit closure
// returns it when the desired state already holds or when it declines to
// write (e.g. a foreign-owned block it must not overwrite).
var ErrNoChange = errors.New("claudecfg: no change")

// ErrInvalidJSON wraps every parse failure Read reports, so a read-only
// caller (a status or drift report) can translate the condition into a
// drift result instead of aborting, while writers still fail hard.
var ErrInvalidJSON = errors.New("claudecfg: settings json invalid")

// SettingsPath returns ~/.claude/settings.json for the given home.
func SettingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

// LockDir returns the PID lock directory serializing Mutate calls. It
// lives inside Claude Code's tree, so docs/BOUNDARIES.md names it as a
// dotfiles-v2 write root.
func LockDir(home string) string {
	return filepath.Join(home, ".claude", ".dot-lock")
}

// Read parses settings.json into a generic map. A missing or empty file
// yields an empty map; invalid JSON is a hard error so dot never clobbers
// a file it cannot faithfully rewrite.
//
// Read deliberately takes no lock: a hook or status read that blocked on a
// stale or root-owned lock would be worse than the race a read lock would
// close. Only writers serialize.
func Read(home string) (map[string]any, error) {
	path := SettingsPath(home)
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w: %v (fix the JSON manually; dot will not overwrite it)", path, ErrInvalidJSON, err)
	}
	return settings, nil
}

// Mutate is the write path for settings.json. It acquires the PID lock at
// LockDir, reads the file through the same code path Read uses, applies fn
// to the parsed map, and persists via fileutil.EnsureFile (hash-skip when
// unchanged, backup-before-overwrite, dry-run aware through the runner) at
// mode 0644 with two-space indentation and a trailing newline. It returns
// the changed flag EnsureFile reports.
//
// The lock is released on every exit path: the release closure is deferred
// on the statement following a successful acquire, so a closure error, a
// marshal failure, and a write failure all drop it. A closure returning
// ErrNoChange short-circuits to (false, nil) before any write; any other
// closure error is returned with nothing written.
func Mutate(runner *exec.Runner, home string, fn func(map[string]any) error) (bool, error) {
	release, err := fileutil.AcquirePIDLock(LockDir(home))
	if err != nil {
		return false, err
	}
	defer release()

	settings, err := Read(home)
	if err != nil {
		return false, err
	}
	if err := fn(settings); err != nil {
		if errors.Is(err, ErrNoChange) {
			return false, nil
		}
		return false, err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	return fileutil.EnsureFile(runner, SettingsPath(home), append(data, '\n'), 0o644)
}
