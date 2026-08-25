package fileutil

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func pinBackupClock(t *testing.T, instant time.Time) {
	t.Helper()
	previous := backupNow
	backupNow = func() time.Time { return instant }
	t.Cleanup(func() { backupNow = previous })
}

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

func backupRoot(home string) string {
	return filepath.Join(home, backupDir)
}

func onlyBackup(t *testing.T, home string) string {
	t.Helper()
	names := backupNames(t, home)
	if len(names) != 1 {
		t.Fatalf("backups = %v, want exactly one recovery copy", names)
	}
	return filepath.Join(backupRoot(home), names[0])
}

func assertOwnerOnlyMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
	if got := info.Mode().Perm() & 0o077; got != 0 {
		t.Errorf("%s exposes group/world bits %04o", path, got)
	}
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

func TestEnsureFileAtomic_BackupPermissionsOwnerOnly(t *testing.T) {
	_, home := twoHomes(t)
	path := filepath.Join(home, ".codex", "config.toml")
	const before = "private hud configuration\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	runner, log := runnerWithLog()
	written, err := EnsureFileAtomic(runner, home, path, []byte("updated hud configuration\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("EnsureFileAtomic reported no write")
	}
	if strings.Contains(log.String(), "backup failed") {
		t.Errorf("backup warned: %s", log.String())
	}
	if got, err := os.ReadFile(onlyBackup(t, home)); err != nil || string(got) != before {
		t.Errorf("recovery bytes = %q, err = %v, want %q", got, err, before)
	}
	assertOwnerOnlyMode(t, backupRoot(home), 0o700)
	assertOwnerOnlyMode(t, onlyBackup(t, home), 0o600)
	assertOwnerOnlyMode(t, path, 0o600)
}

func TestWriteHelpers_BackupPermissionsOwnerOnly(t *testing.T) {
	for _, h := range writeHelpers {
		for _, mode := range []os.FileMode{0o600, 0o644, 0o755} {
			t.Run(h.name+"/"+mode.String(), func(t *testing.T) {
				_, home := twoHomes(t)
				path := filepath.Join(home, ".config", "settings.toml")
				const before = "private setting before overwrite\n"
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(before), mode); err != nil {
					t.Fatal(err)
				}

				runner, _ := runnerWithLog()
				written, err := h.fn(runner, home, path, []byte("replacement\n"), mode)
				if err != nil || !written {
					t.Fatalf("written=%t err=%v", written, err)
				}
				copy := onlyBackup(t, home)
				if got, err := os.ReadFile(copy); err != nil || string(got) != before {
					t.Errorf("recovery bytes = %q, err = %v, want %q", got, err, before)
				}
				assertOwnerOnlyMode(t, backupRoot(home), 0o700)
				assertOwnerOnlyMode(t, copy, 0o600)
			})
		}
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

func TestWriteHelpers_EmptyHome(t *testing.T) {
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

			runner, _ := runnerWithLog()
			written, err := h.fn(runner, "", path, []byte("v2\n"), 0o644)
			if err == nil || written {
				t.Fatalf("%s wrote=%t err=%v, want backup refusal", h.name, written, err)
			}
			if got, _ := os.ReadFile(path); string(got) != "v1\n" {
				t.Errorf("content = %q, want unchanged v1", got)
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

func TestWriteHelpers_SameSecondBackups(t *testing.T) {
	pinBackupClock(t, time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC))
	for _, h := range writeHelpers {
		t.Run(h.name, func(t *testing.T) {
			_, home := twoHomes(t)
			path := filepath.Join(home, ".config", "cfg.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
			runner, _ := runnerWithLog()
			if _, err := h.fn(runner, home, path, []byte("b"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := h.fn(runner, home, path, []byte("c"), 0o644); err != nil {
				t.Fatal(err)
			}
			names := backupNames(t, home)
			if len(names) != 2 {
				t.Fatalf("backups = %v, want two same-second recovery copies", names)
			}
			contents := map[string]bool{}
			for _, name := range names {
				data, err := os.ReadFile(filepath.Join(home, backupDir, name))
				if err != nil {
					t.Fatal(err)
				}
				contents[string(data)] = true
			}
			if !contents["a"] || !contents["b"] {
				t.Errorf("recovery contents = %v, want a and b", contents)
			}
		})
	}
}

func TestWriteHelpers_ConcurrentBackupReservations(t *testing.T) {
	pinBackupClock(t, time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC))
	runner, _ := runnerWithLog()
	home := t.TempDir()
	bdir := filepath.Join(home, backupDir)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	paths := make(chan string, workers)
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	for i := range workers {
		go func() {
			payload := []byte("backup-" + string(rune('a'+i)))
			ready.Done()
			<-start
			path, err := writeBackupCopy(runner, bdir, "cfg.toml", payload)
			if err != nil {
				errs <- err
				return
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(payload) {
				errs <- errors.New("backup contents were replaced")
				return
			}
			paths <- path
		}()
	}
	ready.Wait()
	close(start)
	seen := map[string]bool{}
	for range workers {
		select {
		case err := <-errs:
			t.Fatal(err)
		case path := <-paths:
			if seen[path] {
				t.Fatalf("duplicate backup path %s", path)
			}
			seen[path] = true
		}
	}
}

func TestWriteHelpers_BackupFailure(t *testing.T) {
	for _, h := range writeHelpers {
		t.Run(h.name, func(t *testing.T) {
			_, home := twoHomes(t)
			path := filepath.Join(home, ".config", "cfg.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
				t.Fatal(err)
			}
			previous := writeReservedBackup
			writeReservedBackup = func(*os.File, []byte) (int, error) { return 0, errors.New("forced backup write failure") }
			t.Cleanup(func() { writeReservedBackup = previous })

			runner, _ := runnerWithLog()
			written, err := h.fn(runner, home, path, []byte("after"), 0o644)
			if err == nil || written {
				t.Fatalf("written=%t err=%v, want backup failure", written, err)
			}
			if got, _ := os.ReadFile(path); string(got) != "before" {
				t.Errorf("live target = %q, want before", got)
			}
			if names := backupNames(t, home); len(names) != 0 {
				t.Errorf("owned partial backups left behind: %v", names)
			}
		})
	}
}
