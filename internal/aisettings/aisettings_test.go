package aisettings

import (
	"archive/tar"
	"compress/gzip"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func testEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	return &Engine{
		Runner:   exec.NewRunner(false, slog.Default()),
		HomeDir:  home,
		Root:     root,
		Hostname: "testhost",
		User:     "tester",
	}, home, root
}

func TestBackupRestoreSkipsAuthByDefault(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt\"\n"))
	mustWrite(t, filepath.Join(home, ".codex", "auth.json"), []byte(`{"token":"secret"}`))
	mustWrite(t, filepath.Join(home, ".codex", "skills", "mine", "SKILL.md"), []byte("# mine"))
	mustWrite(t, filepath.Join(home, ".codex", "skills", ".system", "skip", "SKILL.md"), []byte("# skip"))

	snap, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth should be excluded by default, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "skills", ".system")); !os.IsNotExist(err) {
		t.Fatalf(".system skills should be excluded, stat err=%v", err)
	}

	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("mutated\n"))
	if _, err := eng.Restore(RestoreOptions{Version: snap.Version}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "model = \"gpt\"\n" {
		t.Fatalf("restored config = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err != nil {
		t.Fatalf("excluded auth should not be deleted by restore: %v", err)
	}
}

func TestRestoreLatestAlias(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("model = \"gpt\"\n"))

	snap, err := eng.Backup(BackupOptions{})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), []byte("mutated\n"))
	restored, err := eng.Restore(RestoreOptions{Version: "latest"})
	if err != nil {
		t.Fatalf("restore latest alias: %v", err)
	}
	if restored.Version != snap.Version {
		t.Fatalf("restored version = %q, want %q", restored.Version, snap.Version)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "model = \"gpt\"\n" {
		t.Fatalf("restored config = %q", got)
	}
}

func TestIncludeAuthBackup(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".codex", "auth.json"), []byte(`{"token":"secret"}`))
	snap, err := eng.Backup(BackupOptions{IncludeAuth: true})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(snap.Path, "home", ".codex", "auth.json")); err != nil {
		t.Fatalf("auth should be included with IncludeAuth: %v", err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	eng, home, _ := testEngine(t)
	mustWrite(t, filepath.Join(home, ".claude", "skills", "writer", "SKILL.md"), []byte("# writer"))
	archive := filepath.Join(t.TempDir(), "ai.tar.gz")
	if _, err := eng.Export(archive, BackupOptions{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive missing: %v", err)
	}

	newHome := t.TempDir()
	importer := &Engine{Runner: exec.NewRunner(false, slog.Default()), HomeDir: newHome, Hostname: "h", Root: t.TempDir()}
	if _, err := importer.Import(archive, RestoreOptions{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(newHome, ".claude", "skills", "writer", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# writer" {
		t.Fatalf("imported content = %q", got)
	}
}

func TestImportRejectsPathTraversal(t *testing.T) {
	eng, _, _ := testEngine(t)
	archive := filepath.Join(t.TempDir(), "bad.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Import(archive, RestoreOptions{}); err == nil {
		t.Fatal("expected path traversal error")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
