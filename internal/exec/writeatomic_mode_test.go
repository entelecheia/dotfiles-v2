package exec

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomic_PreservesExistingMode pins the CREATE-mode contract.
//
// os.WriteFile applies perm only when it creates the file, so the non-atomic
// WriteFile leaves an existing file's mode alone. WriteFileAtomic renames a
// temp file over the target, which REPLACES the inode — without an explicit
// carry-over it would reset the mode to whatever the caller passed. That is
// how swapping EnsureFile for EnsureFileAtomic silently turned a
// user-chmod'd 0600 ~/.claude/settings.json into a world-readable 0644 file.
//
// Deleting the os.Stat carry-over in WriteFileAtomic must turn this red.
func TestWriteFileAtomic_PreservesExistingMode(t *testing.T) {
	r := NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	path := filepath.Join(t.TempDir(), "settings.json")

	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFileAtomic(path, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after atomic write = %04o, want 0600 preserved (perm is the CREATE mode, not a reset)", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"a":2}` {
		t.Errorf("content = %q, want the new payload — preserving mode must not skip the write", content)
	}
}

// TestWriteFileAtomic_UsesPermWhenCreating is the other half: with no existing
// file there is nothing to preserve, so the caller's perm is authoritative.
// Without this row a "preserve" implementation that ignored perm entirely
// would pass the test above.
func TestWriteFileAtomic_UsesPermWhenCreating(t *testing.T) {
	r := NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	path := filepath.Join(t.TempDir(), "fresh.json")

	if err := r.WriteFileAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode on create = %04o, want 0600 from the caller's perm", got)
	}
}
