package ws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ghauth"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// InitOptions controls Init behavior.
type InitOptions struct {
	Force bool // re-clone over populated targets
	Yes   bool // skip interactive confirmations (destructive re-clone, gh auth login)
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
// skipped unless Force is set, in which case contents are deleted and the repo
// is re-cloned. A pre-existing .gdrive symlink is always preserved across
// re-clones (captured before prepareTarget, restored after clone completes).
//
// Empty repos argument is a no-op (returns nil) — callers can invoke this
// unconditionally without checking.
func Init(ctx context.Context, runner *exec.Runner, workspacePath string,
	repos []config.RepoConfig, opts InitOptions,
) ([]string, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	if workspacePath == "" {
		return nil, fmt.Errorf("workspace path not configured")
	}

	// Detect whether any target needs cloning so we only prompt for auth when needed.
	needsClone := false
	for _, repo := range repos {
		if repo.Name == "" || repo.Remote == "" {
			continue
		}
		target := filepath.Join(workspacePath, repo.Name)
		state, _, err := classifyTarget(target)
		if err != nil {
			continue
		}
		if state == stateFresh || (state == statePopulated && opts.Force) {
			needsClone = true
			break
		}
	}

	// Ensure gh is authenticated if we will clone (private repos need auth).
	if needsClone && runner.CommandExists("gh") && !ghauth.Authenticated(runner) {
		fmt.Println("  GitHub authentication required for private repos.")
		if opts.Yes {
			fmt.Println("  ⚠ Skipping gh auth in --yes mode (run 'gh auth login' manually if clone fails)")
		} else if !runner.DryRun {
			if err := ghauth.Login(ctx, runner); err != nil {
				fmt.Printf("  ⚠ gh auth login failed: %v (clone may fail for private repos)\n", err)
			}
		}
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

		// Clear target (gdrive symlink has been captured separately in classifyTarget).
		if err := prepareTarget(runner, target); err != nil {
			errs = append(errs, fmt.Errorf("preparing %s: %w", target, err))
			continue
		}

		prefix := "cloning"
		if runner.DryRun {
			prefix = "would clone"
		}
		msgs = append(msgs, fmt.Sprintf("%s %s -> %s (--recurse-submodules)", prefix, repo.Remote, target))
		if _, err := runner.Run(ctx, "git", "clone", "--recurse-submodules", repo.Remote, target); err != nil {
			errs = append(errs, fmt.Errorf("clone %s: %w", repo.Name, err))
			continue
		}

		// Restore .gdrive symlink if we captured one (works for both fresh and populated re-clones).
		if gdriveTarget != "" {
			linkPath := filepath.Join(target, ".gdrive")
			if !runner.DryRun {
				_ = os.Remove(linkPath) // clone may have created a file here; ignore errors
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
// present, the resolved absolute target of a `.gdrive` symlink inside the dir
// (captured regardless of populated/fresh so it can be restored after a
// destructive re-clone).
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

	gdriveTarget := readGdriveSymlink(path)

	// If a .git entry exists, it's a git repo — populated regardless of other contents.
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		return statePopulated, gdriveTarget, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return stateFresh, gdriveTarget, err
	}
	if len(entries) == 0 {
		return stateFresh, gdriveTarget, nil
	}

	// Only a lone .gdrive symlink → fresh.
	if len(entries) == 1 && entries[0].Name() == ".gdrive" && gdriveTarget != "" {
		return stateFresh, gdriveTarget, nil
	}

	return statePopulated, gdriveTarget, nil
}

// readGdriveSymlink returns the resolved absolute target of a .gdrive symlink
// inside dir, or "" if no such symlink exists.
func readGdriveSymlink(dir string) string {
	linkPath := filepath.Join(dir, ".gdrive")
	fi, err := os.Lstat(linkPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	t, err := os.Readlink(linkPath)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(dir, t)
	}
	return t
}

// prepareTarget clears a target directory so `git clone` can populate it.
// `git clone` refuses a non-empty destination, so we remove the directory
// entirely when it exists. A captured .gdrive symlink target is restored
// by the caller after cloning completes.
func prepareTarget(runner *exec.Runner, target string) error {
	if !runner.FileExists(target) && !runner.IsSymlink(target) {
		return nil
	}
	return runner.RemoveAll(target)
}
