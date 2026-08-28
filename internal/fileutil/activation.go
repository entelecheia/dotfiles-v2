package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// ActivationOptions describes one same-filesystem replacement of a declared
// component. Only OwnedEntries may be renamed or removed from DestinationRoot.
type ActivationOptions struct {
	DestinationRoot string
	StagedRoot      string
	OwnedEntries    []string
	Validate        func(string) error
}

// activationRename is a narrow test seam for promotion and rollback failures.
// Production always uses os.Rename.
var activationRename = os.Rename

type activationMove struct {
	entry       string
	hadPrevious bool
	promoted    bool
}

// ActivateOwnedComponent validates staged content and then atomically promotes
// only declared component entries. It preserves every other destination child,
// and restores the prior owned entries if any cutover or final validation fails.
func ActivateOwnedComponent(runner *exec.Runner, opts ActivationOptions) error {
	if runner == nil {
		return errors.New("activation runner is required")
	}
	if err := validateActivationOptions(opts); err != nil {
		return err
	}

	lockDir := filepath.Join(filepath.Dir(opts.DestinationRoot), "."+filepath.Base(opts.DestinationRoot)+".activation.lock")
	unlock, err := AcquirePIDLock(lockDir, LockOptions{Label: "component activation is running"})
	if err != nil {
		return err
	}
	defer unlock()

	if err := opts.Validate(opts.StagedRoot); err != nil {
		return fmt.Errorf("validating staged component: %w", err)
	}
	for _, entry := range opts.OwnedEntries {
		if _, err := os.Lstat(filepath.Join(opts.StagedRoot, entry)); err != nil {
			return fmt.Errorf("staged owned entry %q: %w", entry, err)
		}
	}
	if runner.DryRun {
		runner.Logger.Info("dry-run: activate owned component", "destination", opts.DestinationRoot, "stage", opts.StagedRoot, "entries", len(opts.OwnedEntries))
		return nil
	}

	if err := os.MkdirAll(opts.DestinationRoot, 0755); err != nil {
		return fmt.Errorf("preparing component destination: %w", err)
	}
	rollbackRoot, err := os.MkdirTemp(filepath.Dir(opts.DestinationRoot), "."+filepath.Base(opts.DestinationRoot)+".rollback-")
	if err != nil {
		return fmt.Errorf("creating rollback directory: %w", err)
	}
	defer os.RemoveAll(rollbackRoot)

	moves := make([]activationMove, 0, len(opts.OwnedEntries))
	for _, entry := range opts.OwnedEntries {
		move := activationMove{entry: entry}
		destination := filepath.Join(opts.DestinationRoot, entry)
		rollback := filepath.Join(rollbackRoot, entry)
		stage := filepath.Join(opts.StagedRoot, entry)

		if _, err := os.Lstat(destination); err == nil {
			if err := os.MkdirAll(filepath.Dir(rollback), 0755); err != nil {
				return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("preparing rollback parent for %q: %w", entry, err))
			}
			if err := activationRename(destination, rollback); err != nil {
				return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("moving active entry %q to rollback: %w", entry, err))
			}
			move.hadPrevious = true
		} else if !os.IsNotExist(err) {
			return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("checking active entry %q: %w", entry, err))
		}

		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			moves = append(moves, move)
			return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("preparing destination parent for %q: %w", entry, err))
		}
		if err := activationRename(stage, destination); err != nil {
			moves = append(moves, move)
			return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("promoting staged entry %q: %w", entry, err))
		}
		move.promoted = true
		moves = append(moves, move)
	}

	if err := opts.Validate(opts.DestinationRoot); err != nil {
		return rollbackActivation(opts, rollbackRoot, moves, fmt.Errorf("validating promoted component: %w", err))
	}
	if err := os.RemoveAll(rollbackRoot); err != nil {
		return fmt.Errorf("removing rollback directory: %w", err)
	}
	return nil
}

func validateActivationOptions(opts ActivationOptions) error {
	if !filepath.IsAbs(opts.DestinationRoot) || !filepath.IsAbs(opts.StagedRoot) {
		return errors.New("activation destination and stage must be absolute")
	}
	if filepath.Clean(opts.DestinationRoot) == filepath.Clean(opts.StagedRoot) || filepath.Dir(opts.DestinationRoot) != filepath.Dir(opts.StagedRoot) {
		return errors.New("activation stage must be a same-parent sibling of destination")
	}
	if opts.Validate == nil {
		return errors.New("activation validation is required")
	}
	info, err := os.Stat(opts.StagedRoot)
	if err != nil {
		return fmt.Errorf("checking activation stage: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("activation stage %q is not a directory", opts.StagedRoot)
	}
	if len(opts.OwnedEntries) == 0 {
		return errors.New("activation requires at least one owned entry")
	}
	for i, entry := range opts.OwnedEntries {
		if entry == "." || entry == "" || !filepath.IsLocal(entry) {
			return fmt.Errorf("unsafe owned entry %q", entry)
		}
		for j := 0; j < i; j++ {
			other := opts.OwnedEntries[j]
			if entry == other || strings.HasPrefix(entry, other+string(filepath.Separator)) || strings.HasPrefix(other, entry+string(filepath.Separator)) {
				return fmt.Errorf("overlapping owned entries %q and %q", other, entry)
			}
		}
	}
	return nil
}

func rollbackActivation(opts ActivationOptions, rollbackRoot string, moves []activationMove, primary error) error {
	var rollbackErr error
	for i := len(moves) - 1; i >= 0; i-- {
		move := moves[i]
		destination := filepath.Join(opts.DestinationRoot, move.entry)
		if move.promoted {
			if err := os.RemoveAll(destination); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("removing promoted entry %q: %w", move.entry, err))
				continue
			}
		}
		if move.hadPrevious {
			rollback := filepath.Join(rollbackRoot, move.entry)
			if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("preparing restore parent for %q: %w", move.entry, err))
				continue
			}
			if err := activationRename(rollback, destination); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restoring active entry %q: %w", move.entry, err))
			}
		}
	}
	return errors.Join(primary, rollbackErr)
}
