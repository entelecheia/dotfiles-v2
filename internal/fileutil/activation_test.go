package fileutil

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func activationRunner(dryRun bool) *exec.Runner {
	return exec.NewRunner(dryRun, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func writeActivationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestActivateOwnedComponent(t *testing.T) {
	t.Run("replaces only owned entries", func(t *testing.T) {
		root := t.TempDir()
		dest := filepath.Join(root, "component")
		stage := filepath.Join(root, ".component-stage")
		writeActivationFile(t, filepath.Join(dest, "managed"), "old")
		writeActivationFile(t, filepath.Join(dest, "custom", "user.txt"), "user")
		writeActivationFile(t, filepath.Join(stage, "managed"), "new")

		if err := ActivateOwnedComponent(activationRunner(false), ActivationOptions{
			DestinationRoot: dest,
			StagedRoot:      stage,
			OwnedEntries:    []string{"managed"},
			Validate: func(path string) error {
				_, err := os.Stat(filepath.Join(path, "managed"))
				return err
			},
		}); err != nil {
			t.Fatalf("ActivateOwnedComponent: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "managed")); err != nil || string(data) != "new" {
			t.Fatalf("managed entry = %q, %v", data, err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "custom", "user.txt")); err != nil || string(data) != "user" {
			t.Fatalf("unmanaged entry = %q, %v", data, err)
		}
	})

	t.Run("failed promotion restores old entry", func(t *testing.T) {
		root := t.TempDir()
		dest := filepath.Join(root, "component")
		stage := filepath.Join(root, ".component-stage")
		writeActivationFile(t, filepath.Join(dest, "managed"), "old")
		writeActivationFile(t, filepath.Join(stage, "managed"), "new")

		oldRename := activationRename
		t.Cleanup(func() { activationRename = oldRename })
		activationRename = func(oldpath, newpath string) error {
			if oldpath == filepath.Join(stage, "managed") && newpath == filepath.Join(dest, "managed") {
				return errors.New("injected promotion failure")
			}
			return os.Rename(oldpath, newpath)
		}

		err := ActivateOwnedComponent(activationRunner(false), ActivationOptions{
			DestinationRoot: dest,
			StagedRoot:      stage,
			OwnedEntries:    []string{"managed"},
			Validate:        func(string) error { return nil },
		})
		if err == nil {
			t.Fatal("expected promotion failure")
		}
		if data, readErr := os.ReadFile(filepath.Join(dest, "managed")); readErr != nil || string(data) != "old" {
			t.Fatalf("old entry was not restored: %q, %v", data, readErr)
		}
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "rollback") {
				t.Fatalf("rollback directory leaked: %s", entry.Name())
			}
		}
	})

	t.Run("staged validation preserves active component", func(t *testing.T) {
		root := t.TempDir()
		dest := filepath.Join(root, "component")
		stage := filepath.Join(root, ".component-stage")
		writeActivationFile(t, filepath.Join(dest, "managed"), "old")
		writeActivationFile(t, filepath.Join(stage, "managed"), "new")
		err := ActivateOwnedComponent(activationRunner(false), ActivationOptions{
			DestinationRoot: dest,
			StagedRoot:      stage,
			OwnedEntries:    []string{"managed"},
			Validate:        func(string) error { return errors.New("invalid stage") },
		})
		if err == nil {
			t.Fatal("expected validation failure")
		}
		if data, readErr := os.ReadFile(filepath.Join(dest, "managed")); readErr != nil || string(data) != "old" {
			t.Fatalf("active entry changed after validation failure: %q, %v", data, readErr)
		}
	})

	t.Run("dry run does not mutate", func(t *testing.T) {
		root := t.TempDir()
		dest := filepath.Join(root, "component")
		stage := filepath.Join(root, ".component-stage")
		writeActivationFile(t, filepath.Join(dest, "managed"), "old")
		writeActivationFile(t, filepath.Join(stage, "managed"), "new")
		if err := ActivateOwnedComponent(activationRunner(true), ActivationOptions{
			DestinationRoot: dest,
			StagedRoot:      stage,
			OwnedEntries:    []string{"managed"},
			Validate:        func(string) error { return nil },
		}); err != nil {
			t.Fatalf("dry-run ActivateOwnedComponent: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "managed")); err != nil || string(data) != "old" {
			t.Fatalf("dry run changed active entry: %q, %v", data, err)
		}
	})
}

func TestActivateOwnedComponent_Concurrent(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "component")
	stage := filepath.Join(root, ".component-stage")
	writeActivationFile(t, filepath.Join(dest, "managed"), "old")
	writeActivationFile(t, filepath.Join(stage, "managed"), "new")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ActivateOwnedComponent(activationRunner(false), ActivationOptions{
			DestinationRoot: dest,
			StagedRoot:      stage,
			OwnedEntries:    []string{"managed"},
			Validate: func(string) error {
				close(entered)
				<-release
				return nil
			},
		})
	}()
	<-entered
	err := ActivateOwnedComponent(activationRunner(false), ActivationOptions{
		DestinationRoot: dest,
		StagedRoot:      stage,
		OwnedEntries:    []string{"managed"},
		Validate:        func(string) error { return nil },
	})
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("concurrent activation error = %v, want ErrLockHeld", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first activation: %v", err)
	}
}
