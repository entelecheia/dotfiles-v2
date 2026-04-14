package ws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// InitOptions controls Init behavior.
type InitOptions struct {
	Force bool // re-clone over populated targets
	Yes   bool // skip interactive confirmation for destructive re-clone
}

// targetState classifies the current state of a clone target.
type targetState int

const (
	stateFresh     targetState = iota // missing, empty, or only contains .gdrive symlink
	statePopulated                    // has real content / git repo
)

// Init clones each configured repo into <workspacePath>/<repo.Name> using
// `git clone --recurse-submodules`. Fresh targets (missing, empty, or holding
// only a .gdrive symlink) are cloned without Force. Populated targets are
// skipped unless Force is set, in which case contents are deleted and
// re-cloned (with .gdrive symlink preserved when present).
func Init(ctx context.Context, runner *exec.Runner, workspacePath string,
	repos []config.RepoConfig, opts InitOptions,
) ([]string, error) {
	if workspacePath == "" {
		return nil, fmt.Errorf("workspace path not configured")
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no repos configured (set workspace.repos in user state)")
	}

	if !runner.IsDir(workspacePath) {
		if err := runner.MkdirAll(workspacePath, 0755); err != nil {
			return nil, fmt.Errorf("creating workspace root %s: %w", workspacePath, err)
		}
	}

	var msgs []string
	var errs []error

	for _, repo := range repos {
		if repo.Name == "" || repo.Remote == "" {
			errs = append(errs, fmt.Errorf("invalid repo entry: name=%q remote=%q", repo.Name, repo.Remote))
			continue
		}
		target := filepath.Join(workspacePath, repo.Name)

		state, gdriveTarget, err := classifyTarget(target)
		if err != nil {
			errs = append(errs, fmt.Errorf("classifying %s: %w", target, err))
			continue
		}

		if state == statePopulated && !opts.Force {
			msgs = append(msgs, fmt.Sprintf("skip %s (already populated, use --force to re-clone)", target))
			continue
		}

		if state == statePopulated && opts.Force && !opts.Yes && !runner.DryRun {
			fmt.Printf("Re-clone %s? This will DELETE existing contents.\n", target)
			ok, err := ui.ConfirmBool("Continue?", false, false)
			if err != nil {
				return msgs, err
			}
			if !ok {
				msgs = append(msgs, fmt.Sprintf("skip %s (re-clone declined)", target))
				continue
			}
		}

		// Clear target (preserving .gdrive symlink if present).
		if err := prepareTarget(runner, target); err != nil {
			errs = append(errs, fmt.Errorf("preparing %s: %w", target, err))
			continue
		}

		msgs = append(msgs, fmt.Sprintf("cloning %s -> %s (--recurse-submodules)", repo.Remote, target))
		if _, err := runner.Run(ctx, "git", "clone", "--recurse-submodules", repo.Remote, target); err != nil {
			errs = append(errs, fmt.Errorf("clone %s: %w", repo.Name, err))
			continue
		}

		// Restore .gdrive symlink if we had one.
		if gdriveTarget != "" {
			linkPath := filepath.Join(target, ".gdrive")
			if !runner.DryRun {
				_ = os.Remove(linkPath) // clone may have created a file here (unlikely); ignore errors
			}
			if err := runner.Symlink(gdriveTarget, linkPath); err != nil {
				errs = append(errs, fmt.Errorf("restoring .gdrive symlink in %s: %w", target, err))
				continue
			}
			msgs = append(msgs, fmt.Sprintf("restored %s -> %s", linkPath, gdriveTarget))
		}
	}

	if len(errs) > 0 {
		return msgs, errors.Join(errs...)
	}
	return msgs, nil
}

// classifyTarget inspects the target path and returns its state plus, if
// present, the resolved absolute target of a sole `.gdrive` symlink so it
// can be restored after cloning.
func classifyTarget(path string) (targetState, string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return stateFresh, "", nil
		}
		return stateFresh, "", err
	}
	if !fi.IsDir() {
		// A regular file or symlink at the path itself — treat as populated.
		return statePopulated, "", nil
	}

	// If a .git entry exists, it's a git repo — populated regardless of other contents.
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		return statePopulated, "", nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return stateFresh, "", err
	}
	if len(entries) == 0 {
		return stateFresh, "", nil
	}

	// Only a lone .gdrive symlink → treat as fresh, preserve the link.
	if len(entries) == 1 && entries[0].Name() == ".gdrive" {
		linkPath := filepath.Join(path, ".gdrive")
		lfi, err := os.Lstat(linkPath)
		if err == nil && lfi.Mode()&os.ModeSymlink != 0 {
			t, err := os.Readlink(linkPath)
			if err == nil {
				if !filepath.IsAbs(t) {
					t = filepath.Join(path, t)
				}
				return stateFresh, t, nil
			}
		}
	}

	return statePopulated, "", nil
}

// prepareTarget clears a target directory so `git clone` can populate it.
// `git clone` refuses a non-empty destination, so we remove the directory
// entirely when it exists. Callers preserve any `.gdrive` symlink target
// separately and restore it after clone completes.
func prepareTarget(runner *exec.Runner, target string) error {
	if !runner.FileExists(target) && !runner.IsSymlink(target) {
		return nil
	}
	return runner.RemoveAll(target)
}
