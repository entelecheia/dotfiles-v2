package profilesnap

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func newTestEngine(t *testing.T, home string, root string) *Engine {
	t.Helper()
	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return &Engine{
		Runner:     runner,
		HomeDir:    home,
		Root:       root,
		Hostname:   "testhost",
		User:       "tester",
		StatePath:  filepath.Join(home, ".config", "dotfiles", "config.yaml"),
		SecretsDir: filepath.Join(home, ".ssh"),
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestBackupAndRestoreRoundtrip(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	eng := newTestEngine(t, home, root)

	// seed state + age key
	const stateYAML = "name: Tester\nprofile: full\nmodules:\n  macapps:\n    enabled: true\n    casks: [raycast]\n    casks_extra: [maccy]\n    backup_apps: [raycast]\n"
	writeFile(t, eng.StatePath, stateYAML, 0o644)
	writeFile(t, filepath.Join(eng.SecretsDir, "age_key"), "AGE-SECRET-KEY-TEST", 0o600)
	writeFile(t, filepath.Join(eng.SecretsDir, "age_key.pub"), "age1public", 0o644)

	snap, err := eng.Backup(BackupOptions{Tag: "first", IncludeSecrets: true})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if snap.Tag != "first" {
		t.Errorf("tag: %q", snap.Tag)
	}
	if !snap.WithSecret {
		t.Errorf("WithSecret flag not preserved")
	}

	// artefacts present?
	for _, rel := range []string{"config.yaml", "meta.yaml", "apps/install.yaml", "apps/backup.yaml", "secrets/age_key"} {
		if _, err := os.Stat(filepath.Join(snap.Path, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(eng.LatestPointerPath()); err != nil {
		t.Errorf("latest.txt missing: %v", err)
	}

	// Corrupt live state + age key, then restore and verify.
	writeFile(t, eng.StatePath, "name: MUTATED\n", 0o644)
	writeFile(t, filepath.Join(eng.SecretsDir, "age_key"), "MUTATED", 0o600)

	restored, err := eng.Restore(RestoreOptions{IncludeSecrets: true, IncludeState: true})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Version != snap.Version {
		t.Errorf("restored wrong version: %q vs %q", restored.Version, snap.Version)
	}
	got, _ := os.ReadFile(eng.StatePath)
	if string(got) != stateYAML {
		t.Errorf("state not restored: %q", got)
	}
	keyGot, _ := os.ReadFile(filepath.Join(eng.SecretsDir, "age_key"))
	if string(keyGot) != "AGE-SECRET-KEY-TEST" {
		t.Errorf("age_key not restored: %q", keyGot)
	}
}

func TestListNewestFirstAndLatestMarker(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	eng := newTestEngine(t, home, root)
	writeFile(t, eng.StatePath, "name: x\n", 0o644)

	s1, err := eng.Backup(BackupOptions{Tag: "one"})
	if err != nil {
		t.Fatal(err)
	}
	// Ensure second snapshot has a distinct version id (1s resolution).
	// Force a different directory name by bumping filesystem state—use a new
	// version id manually to keep the test fast.
	v2 := s1.Version + "-2"
	dest := eng.VersionPath(v2)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, "meta.yaml"), "version: "+v2+"\nhostname: testhost\n", 0o644)
	writeFile(t, eng.LatestPointerPath(), v2, 0o644)

	snaps, err := eng.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].Version != v2 {
		t.Errorf("newest first broken: got %q want %q", snaps[0].Version, v2)
	}
	if !snaps[0].IsLatest {
		t.Errorf("latest marker missing on %q", snaps[0].Version)
	}
	if snaps[1].IsLatest {
		t.Errorf("older snapshot should not be latest")
	}
}

func TestRestoreMissingVersion(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	eng := newTestEngine(t, home, root)
	writeFile(t, eng.StatePath, "name: x\n", 0o644)

	if _, err := eng.Restore(RestoreOptions{Version: "nope", IncludeState: true}); err == nil {
		t.Error("expected error for missing version")
	}
}

func TestRestoreDefaultsToLatestPointer(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	eng := newTestEngine(t, home, root)
	writeFile(t, eng.StatePath, "name: original\n", 0o644)

	s1, err := eng.Backup(BackupOptions{Tag: "a"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, eng.StatePath, "name: mutated\n", 0o644)

	restored, err := eng.Restore(RestoreOptions{IncludeState: true})
	if err != nil {
		t.Fatalf("restore latest: %v", err)
	}
	if restored.Version != s1.Version {
		t.Errorf("want %q got %q", s1.Version, restored.Version)
	}
}
