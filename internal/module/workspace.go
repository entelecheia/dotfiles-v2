package module

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// WorkspaceModule manages workspace symlink federation and shell config.
//
// Symlink safety rules:
//   - Only create symlinks when the link path does NOT exist at all.
//   - Never delete, overwrite, or modify existing symlinks or directories.
//   - Broken symlinks are repaired only if an explicit target is configured.
//
// Dual-workspace architecture:
//   - Path (e.g. ~/workspace) is the git workspace (text/md only).
//   - GdriveSymlink (e.g. ~/gdrive-workspace) → Gdrive (Google Drive physical path).
//   - inbox/downloads and inbox/incoming are symlinked to Drive for binary access.
type WorkspaceModule struct{}

func (m *WorkspaceModule) Name() string { return "workspace" }

// expandHome replaces a leading ~/ with the user's home directory.
func (m *WorkspaceModule) expandHome(rc *RunContext, path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(rc.HomeDir, path[2:])
	}
	return path
}

func (m *WorkspaceModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	cfg := rc.Config.Modules.Workspace

	if !cfg.Enabled || cfg.Path == "" {
		return &CheckResult{Satisfied: true}, nil
	}

	// Expand ~ in all config paths for filesystem operations
	workspacePath := m.expandHome(rc, cfg.Path)
	symlink := m.expandHome(rc, cfg.Symlink)
	gdrive := m.expandHome(rc, cfg.Gdrive)
	gdriveSymlink := m.expandHome(rc, cfg.GdriveSymlink)

	// Git repo cloning: check if configured repos need cloning
	for _, repo := range cfg.Repos {
		repoPath := filepath.Join(workspacePath, repo.Name)
		if !rc.Runner.IsDir(repoPath) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("clone %s into %s/%s", repo.Remote, cfg.Path, repo.Name),
				Command:     fmt.Sprintf("git clone %s %s", repo.Remote, repoPath),
			})
		}
	}

	// workspace symlink: only if explicit symlink target is configured
	if symlink != "" && !m.pathUsable(rc, workspacePath) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("symlink %s -> %s", cfg.Path, cfg.Symlink),
			Command:     fmt.Sprintf("ln -sfn %q %q", symlink, workspacePath),
		})
	}

	// Google Drive symlink: gdrive_symlink → gdrive
	if gdriveSymlink != "" && gdrive != "" && !m.pathUsable(rc, gdriveSymlink) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("symlink %s -> %s", cfg.GdriveSymlink, cfg.Gdrive),
			Command:     fmt.Sprintf("ln -sfn %q %q", gdrive, gdriveSymlink),
		})
	}

	// workspace/work/.vault → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, vaultXref) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", vaultXref, vaultPath),
				Command:     fmt.Sprintf("ln -sfn %q %q", vaultPath, vaultXref),
			})
		}

		// workspace/work/.gdrive → gdrive_symlink/work
		if gdriveSymlink != "" {
			gdriveWork := filepath.Join(gdriveSymlink, "work")
			gdriveXref := filepath.Join(workDir, ".gdrive")
			if m.targetReachable(rc, gdriveWork) && !m.pathUsable(rc, gdriveXref) {
				changes = append(changes, Change{
					Description: fmt.Sprintf("symlink %s -> %s", gdriveXref, gdriveWork),
					Command:     fmt.Sprintf("ln -sfn %q %q", gdriveWork, gdriveXref),
				})
			}
		}

		// inbox symlinks: work/inbox/{downloads,incoming} → gdrive/work/inbox/{downloads,incoming}
		if gdriveSymlink != "" {
			inboxDir := filepath.Join(workDir, "inbox")
			if rc.Runner.IsDir(inboxDir) {
				for _, sub := range []string{"downloads", "incoming"} {
					linkPath := filepath.Join(inboxDir, sub)
					gdriveTarget := filepath.Join(gdriveSymlink, "work", "inbox", sub)
					if m.targetReachable(rc, gdriveTarget) && !m.pathUsable(rc, linkPath) {
						changes = append(changes, Change{
							Description: fmt.Sprintf("symlink %s -> %s", linkPath, gdriveTarget),
							Command:     fmt.Sprintf("ln -sfn %q %q", gdriveTarget, linkPath),
						})
					}
				}
			}
		}
	}

	// workspace/vault/.work → workspace/work
	if m.targetReachable(rc, vaultPath) {
		workXref := filepath.Join(vaultPath, ".work")
		if rc.Runner.IsDir(workDir) && !m.pathUsable(rc, workXref) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", workXref, workDir),
				Command:     fmt.Sprintf("ln -sfn %q %q", workDir, workXref),
			})
		}
	}

	// shell config file
	shellDest := filepath.Join(rc.HomeDir, ".config", "shell", "40-workspace.sh")
	content, err := rc.Template.Render("shell/40-workspace.sh.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering shell/40-workspace.sh.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, shellDest, content) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", shellDest),
			Command:     "render shell/40-workspace.sh.tmpl -> ~/.config/shell/40-workspace.sh",
		})
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *WorkspaceModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string
	cfg := rc.Config.Modules.Workspace

	if !cfg.Enabled || cfg.Path == "" {
		return &ApplyResult{Changed: false, Messages: []string{"workspace path not configured"}}, nil
	}

	// Expand ~ in all config paths for filesystem operations
	workspacePath := m.expandHome(rc, cfg.Path)
	symlink := m.expandHome(rc, cfg.Symlink)
	gdrive := m.expandHome(rc, cfg.Gdrive)
	gdriveSymlink := m.expandHome(rc, cfg.GdriveSymlink)

	// Git repo cloning (before symlinks — dirs must exist first)
	var toClone []config.RepoConfig
	for _, repo := range cfg.Repos {
		repoPath := filepath.Join(workspacePath, repo.Name)
		if !rc.Runner.IsDir(repoPath) {
			toClone = append(toClone, repo)
		}
	}
	if len(toClone) > 0 {
		// Ensure gh is authenticated before cloning (private repos need auth)
		if rc.Runner.CommandExists("gh") && !ghAuthenticated(rc) {
			fmt.Println("  GitHub authentication required for private repos.")
			if rc.Yes {
				fmt.Println("  ⚠ Skipping gh auth in --yes mode (run 'gh auth login' manually)")
			} else if !rc.DryRun {
				if err := ghLogin(ctx, rc); err != nil {
					fmt.Printf("  ⚠ gh auth login failed: %v (clone may fail for private repos)\n", err)
				}
			}
		}
		// Ensure workspace root exists
		if !rc.Runner.IsDir(workspacePath) {
			if err := rc.Runner.MkdirAll(workspacePath, 0755); err != nil {
				fmt.Printf("  ⚠ workspace: cannot create %s: %v\n", workspacePath, err)
			}
		}
		// Clone each repo
		for _, repo := range toClone {
			repoPath := filepath.Join(workspacePath, repo.Name)
			result, err := rc.Runner.Run(ctx, "git", "clone", repo.Remote, repoPath)
			if err != nil {
				fmt.Printf("  ⚠ workspace: clone %s failed: %v (continuing)\n", repo.Name, err)
				if result != nil && result.Stderr != "" {
					fmt.Printf("    %s\n", result.Stderr)
				}
				continue
			}
			messages = append(messages, fmt.Sprintf("cloned %s into %s", repo.Remote, repoPath))
		}
	}

	// workspace symlink: only create if explicit target is set and path doesn't exist
	if symlink != "" {
		if !m.pathUsable(rc, workspacePath) {
			if !m.targetReachable(rc, symlink) {
				return nil, fmt.Errorf("symlink target does not exist: %s", cfg.Symlink)
			}
			if err := rc.Runner.Symlink(symlink, workspacePath); err != nil {
				return nil, fmt.Errorf("symlinking workspace: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", cfg.Path, cfg.Symlink))
		}
	}

	// Google Drive symlink
	if gdriveSymlink != "" && gdrive != "" {
		if !m.pathUsable(rc, gdriveSymlink) {
			if !m.targetReachable(rc, gdrive) {
				return nil, fmt.Errorf("gdrive target does not exist: %s", cfg.Gdrive)
			}
			if err := rc.Runner.Symlink(gdrive, gdriveSymlink); err != nil {
				return nil, fmt.Errorf("symlinking gdrive: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", cfg.GdriveSymlink, cfg.Gdrive))
		}
	}

	// workspace/work/.vault → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, vaultXref) {
			if err := rc.Runner.Symlink(vaultPath, vaultXref); err != nil {
				return nil, fmt.Errorf("symlinking work/.vault: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", vaultXref, vaultPath))
		}

		// workspace/work/.gdrive → gdrive_symlink/work
		if gdriveSymlink != "" {
			gdriveWork := filepath.Join(gdriveSymlink, "work")
			gdriveXref := filepath.Join(workDir, ".gdrive")
			if m.targetReachable(rc, gdriveWork) && !m.pathUsable(rc, gdriveXref) {
				if err := rc.Runner.Symlink(gdriveWork, gdriveXref); err != nil {
					return nil, fmt.Errorf("symlinking work/.gdrive: %w", err)
				}
				messages = append(messages, fmt.Sprintf("symlinked %s -> %s", gdriveXref, gdriveWork))
			}
		}

		// inbox symlinks
		if gdriveSymlink != "" {
			inboxDir := filepath.Join(workDir, "inbox")
			if rc.Runner.IsDir(inboxDir) {
				for _, sub := range []string{"downloads", "incoming"} {
					linkPath := filepath.Join(inboxDir, sub)
					gdriveTarget := filepath.Join(gdriveSymlink, "work", "inbox", sub)
					if m.targetReachable(rc, gdriveTarget) && !m.pathUsable(rc, linkPath) {
						if err := rc.Runner.Symlink(gdriveTarget, linkPath); err != nil {
							return nil, fmt.Errorf("symlinking inbox/%s: %w", sub, err)
						}
						messages = append(messages, fmt.Sprintf("symlinked %s -> %s", linkPath, gdriveTarget))
					}
				}
			}
		}
	}

	// workspace/vault/.work → workspace/work
	if m.targetReachable(rc, vaultPath) {
		workXref := filepath.Join(vaultPath, ".work")
		if rc.Runner.IsDir(workDir) && !m.pathUsable(rc, workXref) {
			if err := rc.Runner.Symlink(workDir, workXref); err != nil {
				return nil, fmt.Errorf("symlinking vault/.work: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", workXref, workDir))
		}
	}

	// shell config
	shellDest := filepath.Join(rc.HomeDir, ".config", "shell", "40-workspace.sh")
	content, err := rc.Template.Render("shell/40-workspace.sh.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering shell/40-workspace.sh.tmpl: %w", err)
	}
	written, err := fileutil.EnsureFile(rc.Runner, shellDest, content, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", shellDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", shellDest))
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}

// pathUsable returns true if path exists and is functional.
// A valid symlink (pointing to an accessible target), regular directory, or file.
// Returns false for nonexistent paths or broken symlinks.
func (m *WorkspaceModule) pathUsable(rc *RunContext, path string) bool {
	if rc.Runner.IsSymlink(path) {
		target, err := rc.Runner.Readlink(path)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return rc.Runner.FileExists(target) || rc.Runner.IsDir(target)
	}
	return rc.Runner.FileExists(path) || rc.Runner.IsDir(path)
}

// targetReachable returns true if path exists and is accessible (follows symlinks).
func (m *WorkspaceModule) targetReachable(rc *RunContext, path string) bool {
	return rc.Runner.FileExists(path) || rc.Runner.IsDir(path)
}
