package gdrivesync

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// migrationFixture sets up a temp dir mirroring a workspace's structure
// at the moment migrate would touch it: 3 symlinks plus a few real
// files that must be untouched.
type migrationFixture struct {
	root        string
	mirrorRoot  string
	gdriveLink  string
	dlLink      string
	incLink     string
	keepFile    string
	preexisting string
}

func newMigrationFixture(t *testing.T) *migrationFixture {
	t.Helper()
	root := t.TempDir()
	mirror := t.TempDir()

	// Build mirror with the directories the symlinks pointed to so the
	// links resolve. The migration doesn't dereference; this just makes
	// the fixture realistic.
	if err := os.MkdirAll(filepath.Join(mirror, "inbox", "downloads"), 0755); err != nil {
		t.Fatalf("seed mirror downloads: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(mirror, "inbox", "incoming"), 0755); err != nil {
		t.Fatalf("seed mirror incoming: %v", err)
	}

	// Workspace skeleton: real inbox dir (sibling of the symlinks).
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0755); err != nil {
		t.Fatalf("seed workspace inbox: %v", err)
	}
	gdriveLink := filepath.Join(root, ".gdrive")
	dlLink := filepath.Join(root, "inbox", "downloads")
	incLink := filepath.Join(root, "inbox", "incoming")

	if err := os.Symlink(mirror, gdriveLink); err != nil {
		t.Fatalf("seed .gdrive symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(mirror, "inbox", "downloads"), dlLink); err != nil {
		t.Fatalf("seed downloads symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(mirror, "inbox", "incoming"), incLink); err != nil {
		t.Fatalf("seed incoming symlink: %v", err)
	}

	// Real file that must survive the migration untouched.
	keepFile := filepath.Join(root, "README.md")
	if err := os.WriteFile(keepFile, []byte("real content"), 0644); err != nil {
		t.Fatalf("seed README: %v", err)
	}

	return &migrationFixture{
		root:        root,
		mirrorRoot:  mirror,
		gdriveLink:  gdriveLink,
		dlLink:      dlLink,
		incLink:     incLink,
		keepFile:    keepFile,
		preexisting: filepath.Join(root, "preexisting.txt"),
	}
}

func (f *migrationFixture) runner() *exec.Runner {
	return exec.NewRunner(false, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSymlinkStates_DetectsAllShapes(t *testing.T) {
	f := newMigrationFixture(t)
	states := symlinkStates(f.root)

	if len(states) != 3 {
		t.Fatalf("got %d states, want 3", len(states))
	}
	for _, st := range states {
		if !st.IsSymlink {
			t.Errorf("%s should be detected as symlink, got: %+v", st.Rel, st)
		}
	}
}

func TestSymlinkStates_HandlesMissingAndRealDir(t *testing.T) {
	root := t.TempDir()
	// Create only inbox/downloads as a real dir; leave .gdrive and inbox/incoming missing.
	if err := os.MkdirAll(filepath.Join(root, "inbox", "downloads"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got := symlinkStates(root)
	byRel := make(map[string]SymlinkState, len(got))
	for _, s := range got {
		byRel[s.Rel] = s
	}

	if !byRel[".gdrive"].Missing {
		t.Errorf(".gdrive should be Missing, got %+v", byRel[".gdrive"])
	}
	if !byRel["inbox/downloads"].IsDir {
		t.Errorf("inbox/downloads should be IsDir, got %+v", byRel["inbox/downloads"])
	}
	if !byRel["inbox/incoming"].Missing {
		t.Errorf("inbox/incoming should be Missing, got %+v", byRel["inbox/incoming"])
	}
}

func TestConvertSymlinks_ReplacesAllSymlinks(t *testing.T) {
	f := newMigrationFixture(t)

	if err := ConvertSymlinks(f.runner(), f.root); err != nil {
		t.Fatalf("ConvertSymlinks: %v", err)
	}

	// .gdrive: removed entirely (no replacement dir).
	if _, err := os.Lstat(f.gdriveLink); !os.IsNotExist(err) {
		t.Errorf(".gdrive should be removed; lstat err = %v", err)
	}

	// inbox/downloads: real dir now (not symlink).
	fi, err := os.Lstat(f.dlLink)
	if err != nil {
		t.Fatalf("inbox/downloads should exist: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("inbox/downloads still a symlink")
	}
	if !fi.IsDir() {
		t.Errorf("inbox/downloads should be a directory")
	}

	// inbox/incoming: same — real dir.
	fi, err = os.Lstat(f.incLink)
	if err != nil {
		t.Fatalf("inbox/incoming should exist: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("inbox/incoming still a symlink")
	}
	if !fi.IsDir() {
		t.Errorf("inbox/incoming should be a directory")
	}

	// Untouched real file must still be there with its content.
	body, err := os.ReadFile(f.keepFile)
	if err != nil {
		t.Errorf("README missing after convert: %v", err)
	}
	if string(body) != "real content" {
		t.Errorf("README content corrupted: %q", body)
	}
}

func TestConvertSymlinks_Idempotent(t *testing.T) {
	f := newMigrationFixture(t)

	if err := ConvertSymlinks(f.runner(), f.root); err != nil {
		t.Fatalf("first ConvertSymlinks: %v", err)
	}

	// Place a marker file inside the now-real downloads dir; second run
	// must not blow it away.
	marker := filepath.Join(f.dlLink, "marker.txt")
	if err := os.WriteFile(marker, []byte("post-migrate"), 0644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	// Second invocation: must be a no-op.
	if err := ConvertSymlinks(f.runner(), f.root); err != nil {
		t.Fatalf("second ConvertSymlinks: %v", err)
	}

	body, err := os.ReadFile(marker)
	if err != nil {
		t.Errorf("marker missing after idempotent re-run: %v", err)
	}
	if string(body) != "post-migrate" {
		t.Errorf("marker content corrupted: %q", body)
	}
}

func TestConvertSymlinks_RefusesUnknownNonSymlink(t *testing.T) {
	root := t.TempDir()
	// Put a regular file where .gdrive should be — must refuse rather
	// than silently deleting the user's data.
	gdrivePath := filepath.Join(root, ".gdrive")
	if err := os.WriteFile(gdrivePath, []byte("user data"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := ConvertSymlinks(runner, root)
	if err == nil {
		t.Fatal("ConvertSymlinks accepted a regular file at .gdrive — would have destroyed user data")
	}

	// Original file must still exist with its content.
	body, _ := os.ReadFile(gdrivePath)
	if string(body) != "user data" {
		t.Errorf("user data lost: %q", body)
	}
}

func TestMigrationLinks_CompletePlanFromSourceSpec(t *testing.T) {
	// Lock in the 3 paths source plan §B.2 specified. Anyone changing
	// this list should also update the source plan and migrate-runbook.
	wantRels := map[string]string{
		".gdrive":         "remove",
		"inbox/downloads": "convert",
		"inbox/incoming":  "convert",
	}

	if len(migrationLinks) != len(wantRels) {
		t.Errorf("migrationLinks count = %d, want %d", len(migrationLinks), len(wantRels))
	}
	for _, link := range migrationLinks {
		want, ok := wantRels[link.Rel]
		if !ok {
			t.Errorf("unexpected migration link %q", link.Rel)
			continue
		}
		if link.Action != want {
			t.Errorf("link %q action = %q, want %q", link.Rel, link.Action, want)
		}
	}
}

func TestPreflight_ReportsBasicShape(t *testing.T) {
	f := newMigrationFixture(t)

	cfg := &Config{
		LocalPath:  f.root + "/",
		MirrorPath: f.mirrorRoot + "/",
		MaxDelete:  1000,
	}

	info, err := Preflight(cfg)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !info.LocalExists || !info.MirrorExists {
		t.Errorf("Preflight should detect both trees: %+v", info)
	}
	if len(info.Symlinks) != 3 {
		t.Errorf("Preflight should detect all 3 symlinks; got %d", len(info.Symlinks))
	}
	if info.FreeOnLocalPart <= 0 {
		t.Errorf("FreeOnLocalPart should be > 0; got %d", info.FreeOnLocalPart)
	}
}
