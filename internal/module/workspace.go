package module

import (
	"context"
	"fmt"
	"path/filepath"

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

func (m *WorkspaceModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	cfg := rc.Config.Modules.Workspace

	if !cfg.Enabled || cfg.Path == "" {
		return &CheckResult{Satisfied: true}, nil
	}

	workspacePath := cfg.Path

	// workspace symlink: only if explicit symlink target is configured
	if cfg.Symlink != "" && !m.pathUsable(rc, workspacePath) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("symlink %s -> %s", workspacePath, cfg.Symlink),
			Command:     fmt.Sprintf("ln -sfn %s %s", cfg.Symlink, workspacePath),
		})
	}

	// Google Drive symlink: gdrive_symlink → gdrive
	if cfg.GdriveSymlink != "" && cfg.Gdrive != "" && !m.pathUsable(rc, cfg.GdriveSymlink) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("symlink %s -> %s", cfg.GdriveSymlink, cfg.Gdrive),
			Command:     fmt.Sprintf("ln -sfn %q %s", cfg.Gdrive, cfg.GdriveSymlink),
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
				Command:     fmt.Sprintf("ln -sfn %s %s", vaultPath, vaultXref),
			})
		}

		// workspace/work/.gdrive → gdrive_symlink/work
		if cfg.GdriveSymlink != "" {
			gdriveWork := filepath.Join(cfg.GdriveSymlink, "work")
			gdriveXref := filepath.Join(workDir, ".gdrive")
			if m.targetReachable(rc, gdriveWork) && !m.pathUsable(rc, gdriveXref) {
				changes = append(changes, Change{
					Description: fmt.Sprintf("symlink %s -> %s", gdriveXref, gdriveWork),
					Command:     fmt.Sprintf("ln -sfn %s %s", gdriveWork, gdriveXref),
				})
			}
		}

		// inbox symlinks: work/inbox/{downloads,incoming} → gdrive/work/inbox/{downloads,incoming}
		if cfg.GdriveSymlink != "" {
			inboxDir := filepath.Join(workDir, "inbox")
			if rc.Runner.IsDir(inboxDir) {
				for _, sub := range []string{"downloads", "incoming"} {
					linkPath := filepath.Join(inboxDir, sub)
					gdriveTarget := filepath.Join(cfg.GdriveSymlink, "work", "inbox", sub)
					if m.targetReachable(rc, gdriveTarget) && !m.pathUsable(rc, linkPath) {
						changes = append(changes, Change{
							Description: fmt.Sprintf("symlink %s -> %s", linkPath, gdriveTarget),
							Command:     fmt.Sprintf("ln -sfn %s %s", gdriveTarget, linkPath),
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
				Command:     fmt.Sprintf("ln -sfn %s %s", workDir, workXref),
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

	workspacePath := cfg.Path

	// workspace symlink: only create if explicit target is set and path doesn't exist
	if cfg.Symlink != "" {
		if !m.pathUsable(rc, workspacePath) {
			if !m.targetReachable(rc, cfg.Symlink) {
				return nil, fmt.Errorf("symlink target does not exist: %s", cfg.Symlink)
			}
			if err := rc.Runner.Symlink(cfg.Symlink, workspacePath); err != nil {
				return nil, fmt.Errorf("symlinking workspace: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", workspacePath, cfg.Symlink))
		}
	}

	// Google Drive symlink
	if cfg.GdriveSymlink != "" && cfg.Gdrive != "" {
		if !m.pathUsable(rc, cfg.GdriveSymlink) {
			if !m.targetReachable(rc, cfg.Gdrive) {
				return nil, fmt.Errorf("gdrive target does not exist: %s", cfg.Gdrive)
			}
			if err := rc.Runner.Symlink(cfg.Gdrive, cfg.GdriveSymlink); err != nil {
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
		if cfg.GdriveSymlink != "" {
			gdriveWork := filepath.Join(cfg.GdriveSymlink, "work")
			gdriveXref := filepath.Join(workDir, ".gdrive")
			if m.targetReachable(rc, gdriveWork) && !m.pathUsable(rc, gdriveXref) {
				if err := rc.Runner.Symlink(gdriveWork, gdriveXref); err != nil {
					return nil, fmt.Errorf("symlinking work/.gdrive: %w", err)
				}
				messages = append(messages, fmt.Sprintf("symlinked %s -> %s", gdriveXref, gdriveWork))
			}
		}

		// inbox symlinks
		if cfg.GdriveSymlink != "" {
			inboxDir := filepath.Join(workDir, "inbox")
			if rc.Runner.IsDir(inboxDir) {
				for _, sub := range []string{"downloads", "incoming"} {
					linkPath := filepath.Join(inboxDir, sub)
					gdriveTarget := filepath.Join(cfg.GdriveSymlink, "work", "inbox", sub)
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
