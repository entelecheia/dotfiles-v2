package claudecfg

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func testRunner() *exec.Runner {
	return exec.NewRunner(false, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMutate_AppliesEditAndWrites(t *testing.T) {
	home := t.TempDir()
	writeFile(t, SettingsPath(home), "{\n  \"existing\": true\n}\n")

	changed, err := Mutate(testRunner(), home, func(settings map[string]any) error {
		settings["added"] = "value"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("inserting a key should report changed")
	}
	settings, err := Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if settings["added"] != "value" {
		t.Fatalf("added key missing: %#v", settings)
	}
	if settings["existing"] != true {
		t.Fatalf("pre-existing key lost: %#v", settings)
	}
}

func TestMutate_ReleasesLockOnClosureError(t *testing.T) {
	home := t.TempDir()
	writeFile(t, SettingsPath(home), "{}\n")
	closureErr := errors.New("edit failed")

	changed, err := Mutate(testRunner(), home, func(map[string]any) error {
		return closureErr
	})
	if !errors.Is(err, closureErr) {
		t.Fatalf("err = %v, want closure error", err)
	}
	if changed {
		t.Fatal("a failing closure must not report changed")
	}
	data, readErr := os.ReadFile(SettingsPath(home))
	if readErr != nil || string(data) != "{}\n" {
		t.Fatalf("file must be untouched on closure error: %q, %v", data, readErr)
	}
	if _, statErr := os.Stat(LockDir(home)); !os.IsNotExist(statErr) {
		t.Fatalf("lock directory must be released on closure error: %v", statErr)
	}
	// A second Mutate on the same home succeeds immediately.
	if _, err := Mutate(testRunner(), home, func(map[string]any) error {
		return ErrNoChange
	}); err != nil {
		t.Fatalf("second Mutate should re-acquire cleanly: %v", err)
	}
}

func TestMutate_ErrNoChangeSkipsWrite(t *testing.T) {
	home := t.TempDir()
	path := SettingsPath(home)
	writeFile(t, path, "{\n  \"a\": 1\n}\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := Mutate(testRunner(), home, func(settings map[string]any) error {
		settings["touched"] = true // mutation is discarded: no write happens
		return ErrNoChange
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("ErrNoChange must report unchanged")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("file changed despite ErrNoChange.\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestRead_TakesNoLock(t *testing.T) {
	home := t.TempDir()
	writeFile(t, SettingsPath(home), "{\n  \"k\": \"v\"\n}\n")

	settings, err := Read(home)
	if err != nil {
		t.Fatal(err)
	}
	if settings["k"] != "v" {
		t.Fatalf("Read = %#v", settings)
	}
	if _, statErr := os.Stat(LockDir(home)); !os.IsNotExist(statErr) {
		t.Fatalf("Read must create no lock directory: %v", statErr)
	}

	// Missing file: empty non-nil map, nil error.
	settings, err = Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if settings == nil || len(settings) != 0 {
		t.Fatalf("missing file: Read = %#v, want empty map", settings)
	}

	// Whitespace-only file: same contract as missing.
	emptyHome := t.TempDir()
	writeFile(t, SettingsPath(emptyHome), "  \n")
	settings, err = Read(emptyHome)
	if err != nil {
		t.Fatal(err)
	}
	if settings == nil || len(settings) != 0 {
		t.Fatalf("empty file: Read = %#v, want empty map", settings)
	}
}

func TestSettingsPath_IsTheOnlyResolver(t *testing.T) {
	home := t.TempDir()
	settings := SettingsPath(home)
	lock := LockDir(home)

	if settings != filepath.Join(home, ".claude", "settings.json") {
		t.Fatalf("SettingsPath = %q, not rooted at the given home", settings)
	}
	if filepath.Dir(lock) != filepath.Join(home, ".claude") {
		t.Fatalf("LockDir = %q, not rooted at the given home", lock)
	}
	// The lock root is dot's own hidden directory, not a path any other tool
	// writes: it shares no name with a Claude Code artifact.
	if base := filepath.Base(lock); base != ".dot-lock" {
		t.Fatalf("LockDir base = %q, want .dot-lock", base)
	}
}
