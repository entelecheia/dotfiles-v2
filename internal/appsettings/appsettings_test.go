package appsettings

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func TestLoadManifestHasExpectedTokens(t *testing.T) {
	mf, err := LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	expected := []string{
		"1password", "raycast", "obsidian", "fantastical", "arc",
		"cmux", "cursor", "chatgpt", "claude", "slack",
		"bartender", "one-switch", "popclip", "shottr", "wispr-flow",
		"forklift", "yoink", "hazel", "termius", "keyboard-maestro", "moom",
	}
	set := make(map[string]bool)
	for _, a := range mf.Apps {
		set[a.Token] = true
		if len(a.Paths) == 0 {
			t.Errorf("token %q: no paths", a.Token)
		}
	}
	for _, want := range expected {
		if !set[want] {
			t.Errorf("manifest missing token %q", want)
		}
	}
	if len(mf.Tokens()) != len(mf.Apps) {
		t.Errorf("Tokens() length mismatch: got %d want %d", len(mf.Tokens()), len(mf.Apps))
	}
}

func TestIsExcluded(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"Preferences/com.foo.plist", false},
		{"Application Support/Foo/Cache/blob", true},
		{"Application Support/Foo/GPUCache/state", true},
		{"Application Support/Foo/Singleton.lock", true},
		{"Application Support/Foo/log.log", true},
		{"Application Support/Foo/.DS_Store", true},
		{"Application Support/Foo/Code Cache/file", true},
		{"Containers/a.b.c/.com.apple.containermanagerd.metadata.plist", false},
	}
	for _, c := range cases {
		got := isExcluded(c.path)
		if got != c.want {
			t.Errorf("isExcluded(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestEngineBackupAndRestoreRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only paths")
	}
	home := t.TempDir()
	// Build fake Library layout with one plist and one support dir w/ caches.
	lib := filepath.Join(home, "Library")
	prefDir := filepath.Join(lib, "Preferences")
	if err := os.MkdirAll(prefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(prefDir, "com.test.moom.plist")
	if err := os.WriteFile(plistPath, []byte("moom-plist"), 0o644); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(home, "backup")

	// Manifest with a single known entry matching the test file.
	mf := &Manifest{Apps: []AppEntry{{
		Token: "testmoom",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.test.moom.plist"}},
	}}}

	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	eng := &Engine{
		Runner:   runner,
		HomeDir:  home,
		Root:     backup,
		Hostname: "testhost",
		Manifest: mf,
	}

	sum, err := eng.Backup(context.Background(), []string{"testmoom"})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if sum.Files != 1 {
		t.Errorf("backup files: got %d want 1", sum.Files)
	}
	archivePath := filepath.Join(backup, "app-settings", "testhost", "testmoom", "Preferences", "com.test.moom.plist")
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("archive path missing: %v", err)
	}

	// Mutate live file, then restore and confirm original contents returned.
	if err := os.WriteFile(plistPath, []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Restore(context.Background(), []string{"testmoom"}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "moom-plist" {
		t.Errorf("restore content: got %q want %q", got, "moom-plist")
	}
}

func TestBackupSkipsExcludedSubtrees(t *testing.T) {
	home := t.TempDir()
	lib := filepath.Join(home, "Library", "Application Support", "Foo")
	if err := os.MkdirAll(filepath.Join(lib, "Caches"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "Caches", "big.blob"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	mf := &Manifest{Apps: []AppEntry{{
		Token: "foo",
		Paths: []PathEntry{{Type: "support", Path: "Application Support/Foo"}},
	}}}
	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	eng := &Engine{Runner: runner, HomeDir: home, Root: filepath.Join(home, "bk"), Hostname: "h", Manifest: mf}

	if _, err := eng.Backup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	archiveRoot := filepath.Join(home, "bk", "app-settings", "h", "foo", "Application Support", "Foo")
	if _, err := os.Stat(filepath.Join(archiveRoot, "settings.json")); err != nil {
		t.Errorf("settings.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveRoot, "Caches")); err == nil {
		t.Errorf("Caches/ should have been excluded")
	}
}

func TestDryRunProducesNoFiles(t *testing.T) {
	home := t.TempDir()
	prefDir := filepath.Join(home, "Library", "Preferences")
	if err := os.MkdirAll(prefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(prefDir, "com.test.dry.plist")
	if err := os.WriteFile(plistPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	mf := &Manifest{Apps: []AppEntry{{
		Token: "dry",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.test.dry.plist"}},
	}}}
	backup := filepath.Join(home, "bk")
	runner := exec.NewRunner(true, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	eng := &Engine{Runner: runner, HomeDir: home, Root: backup, Hostname: "h", Manifest: mf}

	if _, err := eng.Backup(context.Background(), nil); err != nil {
		t.Fatalf("dry-run backup: %v", err)
	}
	// The host root dir itself is skipped in dry-run (MkdirAll), so no files.
	var foundFiles bool
	if _, err := os.Stat(backup); err == nil {
		_ = filepath.WalkDir(backup, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				foundFiles = true
			}
			return nil
		})
	}
	if foundFiles {
		t.Error("dry-run produced regular files")
	}
}

func newRoundtripEngine(t *testing.T, home string, mf *Manifest) *Engine {
	t.Helper()
	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return &Engine{
		Runner:   runner,
		HomeDir:  home,
		Root:     filepath.Join(home, "bk"),
		Hostname: "h",
		Manifest: mf,
	}
}

func TestRestoreSnapshotsExistingFilesFirst(t *testing.T) {
	home := t.TempDir()
	plistPath := filepath.Join(home, "Library", "Preferences", "com.test.pre.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := &Manifest{Apps: []AppEntry{{
		Token: "pre",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.test.pre.plist"}},
	}}}
	eng := newRoundtripEngine(t, home, mf)

	if _, err := eng.Backup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("live-edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	sum, err := eng.Restore(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.PreBackupPath == "" {
		t.Fatal("PreBackupPath not set despite overwriting a live file")
	}
	pre, err := os.ReadFile(filepath.Join(sum.PreBackupPath, "pre", "Preferences", "com.test.pre.plist"))
	if err != nil || string(pre) != "live-edit" {
		t.Errorf("pre-restore copy wrong: %q err=%v", pre, err)
	}
	got, _ := os.ReadFile(plistPath)
	if string(got) != "original" {
		t.Errorf("restore content: %q", got)
	}
}

func TestBackupFailureKeepsPreviousArchive(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based failure injection is ineffective as root")
	}
	home := t.TempDir()
	support := filepath.Join(home, "Library", "Application Support", "Foo")
	if err := os.MkdirAll(support, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(support, "settings.json")
	if err := os.WriteFile(cfg, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := &Manifest{Apps: []AppEntry{{
		Token: "foo",
		Paths: []PathEntry{{Type: "support", Path: "Application Support/Foo"}},
	}}}
	eng := newRoundtripEngine(t, home, mf)

	if _, err := eng.Backup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(eng.HostRoot(), "foo", "Application Support", "Foo", "settings.json")
	if _, err := os.Stat(archived); err != nil {
		t.Fatal(err)
	}

	// Make the live copy unreadable so the next backup fails mid-token.
	if err := os.WriteFile(cfg, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o644) })

	sum, err := eng.Backup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed == 0 {
		t.Fatal("expected Failed > 0 for unreadable source")
	}
	got, err := os.ReadFile(archived)
	if err != nil || string(got) != "v1" {
		t.Errorf("previous archive corrupted: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(eng.HostRoot(), ".staging")); !os.IsNotExist(err) {
		// recoverStaging on next run also clears it; immediate cleanup expected.
		entries, _ := os.ReadDir(filepath.Join(eng.HostRoot(), ".staging"))
		if len(entries) > 0 {
			t.Errorf("staging leftovers remain: %v", entries)
		}
	}
}

func TestBackupSeedsArchivedCopyWhenLiveMissing(t *testing.T) {
	home := t.TempDir()
	plistPath := filepath.Join(home, "Library", "Preferences", "com.test.gone.plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("only-copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := &Manifest{Apps: []AppEntry{{
		Token: "gone",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.test.gone.plist"}},
	}}}
	eng := newRoundtripEngine(t, home, mf)

	if _, err := eng.Backup(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	// App uninstalled: live file gone. Re-backup must not wipe the archive.
	if err := os.Remove(plistPath); err != nil {
		t.Fatal(err)
	}
	sum, err := eng.Backup(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("unexpected failures: %+v", sum)
	}
	archived := filepath.Join(eng.HostRoot(), "gone", "Preferences", "com.test.gone.plist")
	got, err := os.ReadFile(archived)
	if err != nil || string(got) != "only-copy" {
		t.Errorf("archived only-copy lost: %q err=%v", got, err)
	}
}

func TestRecoverStagingRestoresOrphanPrev(t *testing.T) {
	home := t.TempDir()
	mf := &Manifest{Apps: []AppEntry{{
		Token: "x",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.x.plist"}},
	}}}
	eng := newRoundtripEngine(t, home, mf)

	// Simulate a crash between "final -> prev" and "staging -> final".
	prev := filepath.Join(eng.HostRoot(), "x.prev")
	if err := os.MkdirAll(filepath.Join(prev, "Preferences"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "Preferences", "com.x.plist"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng.recoverStaging()
	got, err := os.ReadFile(filepath.Join(eng.HostRoot(), "x", "Preferences", "com.x.plist"))
	if err != nil || string(got) != "old" {
		t.Errorf("orphan .prev not recovered: %q err=%v", got, err)
	}
}

func TestAdoptArchivedAppsSynthesizesEntries(t *testing.T) {
	home := t.TempDir()
	mf := &Manifest{Apps: []AppEntry{{
		Token: "known",
		Paths: []PathEntry{{Type: "pref", Path: "Preferences/com.known.plist"}},
	}}}
	eng := newRoundtripEngine(t, home, mf)

	// Archive contains a discovered app that isn't in the manifest.
	tokenDir := filepath.Join(eng.HostRoot(), "Moom Classic")
	for _, p := range []string{
		filepath.Join(tokenDir, "Preferences", "com.manytricks.Moom.plist"),
		filepath.Join(tokenDir, "Application Support", "Moom", "windows.dat"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Noise that must be ignored.
	if err := os.MkdirAll(filepath.Join(eng.HostRoot(), ".staging", "x.123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(eng.HostRoot(), "dead.prev"), 0o755); err != nil {
		t.Fatal(err)
	}

	adopted := eng.AdoptArchivedApps()
	if len(adopted) != 1 || adopted[0] != "Moom Classic" {
		t.Fatalf("adopted = %v", adopted)
	}
	entry := eng.Manifest.App("Moom Classic")
	if entry == nil {
		t.Fatal("synthesized entry not appended to manifest")
	}
	wantPaths := map[string]bool{
		filepath.Join("Preferences", "com.manytricks.Moom.plist"): true,
		filepath.Join("Application Support", "Moom"):              true,
	}
	for _, p := range entry.Paths {
		if !wantPaths[p.Path] {
			t.Errorf("unexpected synthesized path %q", p.Path)
		}
		delete(wantPaths, p.Path)
	}
	for p := range wantPaths {
		t.Errorf("missing synthesized path %q", p)
	}

	// Restore must now bring the archived settings back.
	sum, err := eng.Restore(context.Background(), []string{"Moom Classic"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 0 {
		t.Fatalf("restore failed: %+v", sum)
	}
	live := filepath.Join(home, "Library", "Preferences", "com.manytricks.Moom.plist")
	if _, err := os.Stat(live); err != nil {
		t.Errorf("adopted app not restored: %v", err)
	}
}

func TestLastBackupStampRoundtrip(t *testing.T) {
	home := t.TempDir()
	eng := newRoundtripEngine(t, home, &Manifest{})
	if err := os.MkdirAll(eng.HostRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := eng.ReadLastBackupStamp(); err != nil || got != nil {
		t.Fatalf("absent stamp should be (nil, nil): %v %v", got, err)
	}
	stamp := BackupStamp{Tag: "onestop-x", Tokens: []string{"a", "b"}, Files: 3}
	if err := eng.WriteLastBackupStamp(stamp); err != nil {
		t.Fatal(err)
	}
	got, err := eng.ReadLastBackupStamp()
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Tag != "onestop-x" || got.Files != 3 || len(got.Tokens) != 2 {
		t.Errorf("stamp roundtrip wrong: %+v", got)
	}
}

func TestListHosts(t *testing.T) {
	root := t.TempDir()
	if hosts, err := ListHosts(root); err != nil || hosts != nil {
		t.Fatalf("missing tree should be (nil, nil): %v %v", hosts, err)
	}
	for _, h := range []string{"mac2", "mac1"} {
		if err := os.MkdirAll(filepath.Join(root, "app-settings", h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hosts, err := ListHosts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0] != "mac1" || hosts[1] != "mac2" {
		t.Errorf("hosts = %v", hosts)
	}
}
