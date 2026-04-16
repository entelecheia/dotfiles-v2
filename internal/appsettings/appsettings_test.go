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
		Runner:    runner,
		HomeDir:   home,
		Root: backup,
		Hostname:  "testhost",
		Manifest:  mf,
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
