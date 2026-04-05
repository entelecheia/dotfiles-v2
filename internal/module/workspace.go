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
//   - The gdrive field is for shell environment context only; it does not
//     trigger automatic symlink creation.
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

	// ~/.brain → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	brainLink := filepath.Join(rc.HomeDir, ".brain")
	if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, brainLink) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("symlink %s -> %s", brainLink, vaultPath),
			Command:     fmt.Sprintf("ln -sfn %s %s", vaultPath, brainLink),
		})
	}

	// workspace/work/.vault → workspace/vault
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, vaultXref) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", vaultXref, vaultPath),
				Command:     fmt.Sprintf("ln -sfn %s %s", vaultPath, vaultXref),
			})
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

	// ~/.brain → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	brainLink := filepath.Join(rc.HomeDir, ".brain")
	if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, brainLink) {
		if err := rc.Runner.Symlink(vaultPath, brainLink); err != nil {
			return nil, fmt.Errorf("symlinking .brain: %w", err)
		}
		messages = append(messages, fmt.Sprintf("symlinked %s -> %s", brainLink, vaultPath))
	}

	// workspace/work/.vault → workspace/vault
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if m.targetReachable(rc, vaultPath) && !m.pathUsable(rc, vaultXref) {
			if err := rc.Runner.Symlink(vaultPath, vaultXref); err != nil {
				return nil, fmt.Errorf("symlinking work/.vault: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", vaultXref, vaultPath))
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
