package fileutil

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// BUG-15: backup() resolved its root from the process environment, so a run
// pointed at another home deposited that home's overwritten files into the
// invoking user's own tree. These rows pin the root to the home the CALLER
// gave, which is the only home a sandboxed run has any business writing to.
//
// The fixture points the process home at a second temp directory before it
// writes anything. That is load-bearing rather than tidy: a version that left
// the process home alone would deposit its own backups in the operator's home
// while asserting that backups no longer go to the operator's home.

// writeHelper is one of the two entry points that back a file up before
// overwriting it. Both call the same backup(), and a fix applied to one is
// not evidence about the other, so every row below runs twice.
type writeHelper struct {
	name string
	fn   func(runner *exec.Runner, home, path string, content []byte, perm os.FileMode) (bool, error)
}

var writeHelpers = []writeHelper{
	{"EnsureFile", EnsureFile},
	{"EnsureFileAtomic", EnsureFileAtomic},
}

// twoHomes returns an invoking home (what the process is pointed at) and a
// target home (what the caller passes), each distinct, so every row can
// assert presence in one AND absence in the other.
func twoHomes(t *testing.T) (invoking, target string) {
	t.Helper()
	invoking = t.TempDir()
	target = t.TempDir()
	t.Setenv("HOME", invoking)
	return invoking, target
}

// runnerWithLog builds a real (non-dry-run) runner whose warnings are
// capturable, since a failed backup is reported as a warning rather than
// returned to the helper's caller.
func runnerWithLog() (*exec.Runner, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return exec.NewRunner(false, logger), &buf
}

// backupNames lists what sits in home's backup directory. It names the EXACT
// directory rather than searching under home: a root derived from the target
// path's own parent would land the copy beside the file it copied, which is
// still "under the target home" and must fail these rows.
func backupNames(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, backupDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading backup dir under %s: %v", home, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// assertNoBackupDir fails when home received a dotfiles backup tree at all.
func assertNoBackupDir(t *testing.T, label, home string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "dotfiles")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("%s home received %s (stat err: %v)", label, dir, err)
	}
}

// assertOnlyFiles walks root and fails on any file outside want. It catches
// the near-miss the exact-directory assertion is aimed at: a backup joined
// against the written file's parent lands somewhere under the target home
// but not where the layout says.
func assertOnlyFiles(t *testing.T, root string, want ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, w := range want {
		allowed[w] = true
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || allowed[p] {
			return nil
		}
		t.Errorf("unexpected file under %s: %s", root, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

func TestWriteHelpers_BackupLandsUnderTheCallersHome(t *testing.T) {
	const before = "the bytes that were there before the overwrite\n"

	for _, h := range writeHelpers {
		t.Run(h.name, func(t *testing.T) {
			invoking, target := twoHomes(t)
			path := filepath.Join(target, ".claude", "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			runner, log := runnerWithLog()
			written, err := h.fn(runner, target, path, []byte("after the overwrite\n"), 0o644)
			if err != nil {
				t.Fatalf("%s: %v", h.name, err)
			}
			if !written {
				t.Fatalf("%s reported no write", h.name)
			}
			if strings.Contains(log.String(), "backup failed") {
				t.Errorf("backup warned: %s", log.String())
			}

			// Presence, at the exact directory the layout names.
			names := backupNames(t, target)
			if len(names) != 1 || !strings.HasPrefix(names[0], "settings.json.") {
				t.Fatalf("%s/%s holds %v, want exactly one settings.json.<timestamp>", target, backupDir, names)
			}
			copied := filepath.Join(target, backupDir, names[0])
			got, err := os.ReadFile(copied)
			if err != nil {
				t.Fatal(err)
			}
			// The copy must hold the PRE-overwrite bytes, so a backup of the
			// wrong content fails rather than passing on existence alone.
			if string(got) != before {
				t.Errorf("backup content = %q, want %q", got, before)
			}

			// Absence, in the home the process happens to be pointed at.
			assertNoBackupDir(t, "invoking", invoking)
			// And nothing anywhere else under the target either.
			assertOnlyFiles(t, target, path, copied)
		})
	}
}

func TestWriteHelpers_NoBackupWhenNothingIsOverwritten(t *testing.T) {
	for _, h := range writeHelpers {
		t.Run(h.name, func(t *testing.T) {
			t.Run("new file", func(t *testing.T) {
				invoking, target := twoHomes(t)
				path := filepath.Join(target, ".config", "fresh.toml")

				runner, _ := runnerWithLog()
				if _, err := h.fn(runner, target, path, []byte("v1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if names := backupNames(t, target); len(names) != 0 {
					t.Errorf("a first write made backups %v", names)
				}
				assertNoBackupDir(t, "invoking", invoking)
				assertNoBackupDir(t, "target", target)
			})

			t.Run("identical content", func(t *testing.T) {
				invoking, target := twoHomes(t)
				path := filepath.Join(target, ".config", "same.toml")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
					t.Fatal(err)
				}

				runner, _ := runnerWithLog()
				written, err := h.fn(runner, target, path, []byte("v1\n"), 0o644)
				if err != nil {
					t.Fatal(err)
				}
				if written {
					t.Error("a hash-identical write reported a write")
				}
				assertNoBackupDir(t, "invoking", invoking)
				assertNoBackupDir(t, "target", target)
			})
		})
	}
}

func TestWriteHelpers_EmptyHomeRefusesTheBackupAndStillWrites(t *testing.T) {
	// An empty home would join the backup path relative to the process
	// working directory, writing into whatever tree the operator was
	// standing in. Refusing it names the caller's mistake instead.
	for _, h := range writeHelpers {
		t.Run(h.name, func(t *testing.T) {
			invoking, target := twoHomes(t)
			path := filepath.Join(target, ".config", "cfg.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			runner, log := runnerWithLog()
			written, err := h.fn(runner, "", path, []byte("v2\n"), 0o644)
			// A failed backup has always been a warning rather than a stop.
			if err != nil {
				t.Fatalf("%s must still write: %v", h.name, err)
			}
			if !written {
				t.Fatalf("%s reported no write", h.name)
			}
			if got, _ := os.ReadFile(path); string(got) != "v2\n" {
				t.Errorf("content = %q, want v2", got)
			}

			if !strings.Contains(log.String(), "backup failed") {
				t.Errorf("no backup warning logged; log = %q", log.String())
			}
			if !strings.Contains(log.String(), "no home directory") {
				t.Errorf("warning does not name the empty home; log = %q", log.String())
			}

			assertNoBackupDir(t, "invoking", invoking)
			assertNoBackupDir(t, "target", target)
			// Nor into the process working directory, which is where a
			// relative join would have landed.
			if _, err := os.Stat(filepath.Join(".local", "share", "dotfiles")); !os.IsNotExist(err) {
				t.Errorf("backup landed relative to the working directory (stat err: %v)", err)
			}
		})
	}
}
