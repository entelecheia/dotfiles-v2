package gsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanShared_ManualOnly(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	if err := os.MkdirAll(filepath.Join(root, "owned-shared-out"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(tmp, ".shortcut-targets-by-id", "abc")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "from-other")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := ScanShared(root, []string{"owned-shared-out"})
	if err != nil {
		t.Fatalf("ScanShared: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %+v, want only one manual entry", got)
	}
	if got[0].RelPath != "owned-shared-out" || got[0].Reason != SharedManual {
		t.Fatalf("entry = %+v, want manual owned-shared-out", got[0])
	}
}

func TestScanShared_MissingManualEntry(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := ScanShared(root, []string{"never-existed"})
	if err != nil {
		t.Fatalf("ScanShared: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Detail != "(missing)" {
		t.Errorf("detail = %q, want (missing)", got[0].Detail)
	}
}

func TestScanShared_SortsAndSkipsBlankManualEntries(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := ScanShared(root, []string{"z-last", "", "a-first"})
	if err != nil {
		t.Fatalf("ScanShared: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %+v, want 2", got)
	}
	if got[0].RelPath != "a-first" || got[1].RelPath != "z-last" {
		t.Fatalf("entries not sorted: %+v", got)
	}
}

func TestIsSharedDriveMount(t *testing.T) {
	tmp := t.TempDir()
	owned := filepath.Join(tmp, "My Drive", "work")
	shared := filepath.Join(tmp, "Shared drives", "team-x")
	if err := os.MkdirAll(owned, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(shared, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if IsSharedDriveMount(owned) {
		t.Errorf("My Drive path flagged as shared-drive mount")
	}
	if !IsSharedDriveMount(shared) {
		t.Errorf("Shared drives path NOT flagged as shared-drive mount")
	}
}

func TestRefuseSharedDriveMirror_BlocksSharedDriveRoot(t *testing.T) {
	tmp := t.TempDir()
	mirror := filepath.Join(tmp, "Shared drives", "team-x", "work")
	if err := os.MkdirAll(mirror, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := &Config{MirrorPath: mirror + "/"}
	if err := refuseSharedDriveMirror(cfg); err == nil {
		t.Error("refuseSharedDriveMirror should reject Shared drives mount; got nil")
	}
}

func TestRefuseSharedDriveMirror_AllowsMyDrive(t *testing.T) {
	tmp := t.TempDir()
	mirror := filepath.Join(tmp, "My Drive", "work")
	if err := os.MkdirAll(mirror, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := &Config{MirrorPath: mirror + "/"}
	if err := refuseSharedDriveMirror(cfg); err != nil {
		t.Errorf("refuseSharedDriveMirror should accept My Drive mount; got %v", err)
	}
}

func TestMaterializeRuntimeExcludesFile_IncludesSharedAndGitTrackedRelpaths(t *testing.T) {
	tmp := t.TempDir()
	path, err := MaterializeRuntimeExcludesFile(tmp, []SharedEntry{
		{RelPath: "projects/koica-shared", Reason: SharedManual},
	}, []string{"tracked.pdf", "nested/source.go"})
	if err != nil {
		t.Fatalf("MaterializeRuntimeExcludesFile: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{
		"/projects/koica-shared\n",
		"/projects/koica-shared/\n",
		"/tracked.pdf\n",
		"/nested/source.go\n",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("runtime excludes file missing %q\n--- got ---\n%s", want, body)
		}
	}
}
