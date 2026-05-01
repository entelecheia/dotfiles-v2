package gdrivesync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectSharedEntry_RealDirIsNotShared(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "owned")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if shared, _ := DetectSharedEntry(dir); shared {
		t.Errorf("plain owned dir flagged as shared")
	}
}

func TestDetectSharedEntry_SymlinkToShortcutTargets(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, ".shortcut-targets-by-id", "abc", "real")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(tmp, "work", "shortcut")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	shared, detail := DetectSharedEntry(link)
	if !shared {
		t.Fatalf("symlink → .shortcut-targets-by-id was not detected")
	}
	if !strings.Contains(detail, ".shortcut-targets-by-id") {
		t.Errorf("detail missing marker: %q", detail)
	}
}

func TestDetectSharedEntry_SymlinkToSharedDrive(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "Shared drives", "team-x", "deep")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(tmp, "work", "team-x")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	shared, detail := DetectSharedEntry(link)
	if !shared {
		t.Fatalf("symlink → Shared drives was not detected")
	}
	if !strings.Contains(detail, "Shared drives") {
		t.Errorf("detail missing marker: %q", detail)
	}
}

func TestDetectSharedEntry_PlainSymlinkNotShared(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "owned-target")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(tmp, "work", "ln")
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if shared, detail := DetectSharedEntry(link); shared {
		t.Errorf("ordinary symlink flagged as shared (%q)", detail)
	}
}

func TestScanShared_DepthLimited(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	target := filepath.Join(tmp, ".shortcut-targets-by-id", "abc")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	// Place the symlink five levels deep — beyond scanMaxDepth (3).
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(deep, "shortcut")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := ScanShared(root, nil)
	if err != nil {
		t.Fatalf("ScanShared: %v", err)
	}
	for _, e := range got {
		if strings.Contains(e.RelPath, "shortcut") {
			t.Errorf("entry beyond scanMaxDepth surfaced: %q", e.RelPath)
		}
	}
}

func TestScanShared_MergesManual(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	if err := os.MkdirAll(filepath.Join(root, "owned-shared-out"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Add an auto-detectable shortcut alongside.
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

	var sawAuto, sawManual bool
	for _, e := range got {
		switch e.RelPath {
		case "from-other":
			if e.Reason != SharedAuto {
				t.Errorf("from-other reason = %v, want auto", e.Reason)
			}
			sawAuto = true
		case "owned-shared-out":
			if e.Reason != SharedManual {
				t.Errorf("owned-shared-out reason = %v, want manual", e.Reason)
			}
			sawManual = true
		}
	}
	if !sawAuto || !sawManual {
		t.Errorf("missing entries: sawAuto=%v sawManual=%v entries=%+v", sawAuto, sawManual, got)
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

func TestScanShared_ManualOverridesAuto(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "work")
	target := filepath.Join(tmp, ".shortcut-targets-by-id", "abc")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "x")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := ScanShared(root, []string{"x"})
	if err != nil {
		t.Fatalf("ScanShared: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d (%+v)", len(got), got)
	}
	if got[0].Reason != SharedManual {
		t.Errorf("when both apply, manual must win — got %v", got[0].Reason)
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

func TestMaterializeSharedExcludesFile(t *testing.T) {
	tmp := t.TempDir()
	entries := []SharedEntry{
		{RelPath: "projects/koica-shared", Reason: SharedManual},
		{RelPath: "external/research-drop", Reason: SharedAuto, Detail: "symlink → .shortcut-targets-by-id"},
	}
	path, err := MaterializeSharedExcludesFile(tmp, entries)
	if err != nil {
		t.Fatalf("MaterializeSharedExcludesFile: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{
		"/projects/koica-shared\n",
		"/projects/koica-shared/\n",
		"/external/research-drop\n",
		"/external/research-drop/\n",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("excludes file missing %q\n--- got ---\n%s", want, body)
		}
	}
}
