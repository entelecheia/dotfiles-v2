package gdrivesync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConflictDir_FilesystemSafeTimestamp(t *testing.T) {
	c := NewConflictDir()
	if strings.Contains(c.Timestamp, ":") {
		t.Errorf("timestamp contains colon (not filesystem-safe): %s", c.Timestamp)
	}
	// Sanity: should still parse as RFC3339 once colons are restored.
	restored := strings.Replace(c.Timestamp, "-", ":", 2) // restore HH:MM
	// Above is a rough check; the important thing is the format starts YYYY-MM-DDTHH-MM.
	if len(c.Timestamp) < len("2026-05-01T12-00-00Z") {
		t.Errorf("timestamp shorter than expected RFC3339-with-dashes: %s (restored guess: %s)", c.Timestamp, restored)
	}
}

func TestConflictDir_BackupRels(t *testing.T) {
	c := &ConflictDir{Timestamp: "2026-05-01T12-30-00Z"}

	pull := c.PullBackupRel()
	push := c.PushBackupRel()

	wantPull := filepath.Join(".sync-conflicts", "2026-05-01T12-30-00Z", "from-gdrive")
	wantPush := filepath.Join(".sync-conflicts", "2026-05-01T12-30-00Z", "from-workspace")

	if pull != wantPull {
		t.Errorf("PullBackupRel = %q, want %q", pull, wantPull)
	}
	if push != wantPush {
		t.Errorf("PushBackupRel = %q, want %q", push, wantPush)
	}

	// Both passes share the same <ts> parent.
	if filepath.Dir(pull) != filepath.Dir(push) {
		t.Errorf("pull and push share different timestamp dirs: %s vs %s", pull, push)
	}
}

func TestListConflicts_EmptyTreeReturnsNilNoError(t *testing.T) {
	dir := t.TempDir() // no .sync-conflicts/ inside
	entries, err := ListConflicts(dir)
	if err != nil {
		t.Fatalf("ListConflicts on empty tree returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ListConflicts on empty tree returned %d entries, want 0", len(entries))
	}
}

func TestListConflicts_OldestFirst(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, conflictsDirName)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Three timestamp dirs. Touch them so mtimes are deterministic.
	stamps := []string{
		"2026-05-01T10-00-00Z",
		"2026-05-01T11-00-00Z",
		"2026-05-01T12-00-00Z",
	}
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	for i, s := range stamps {
		p := filepath.Join(root, s)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}

	// Add a regular file inside conflicts root — must be ignored.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("seed stray file: %v", err)
	}

	got, err := ListConflicts(dir)
	if err != nil {
		t.Fatalf("ListConflicts: %v", err)
	}
	if len(got) != len(stamps) {
		t.Fatalf("got %d entries, want %d (regular file should be skipped)", len(got), len(stamps))
	}
	for i, s := range stamps {
		if got[i].Timestamp != s {
			t.Errorf("entry[%d].Timestamp = %q, want %q (sort order broken?)", i, got[i].Timestamp, s)
		}
	}
}
