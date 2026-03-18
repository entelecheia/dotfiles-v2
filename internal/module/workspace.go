package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// WorkspaceModule manages workspace symlink federation and shell config.
type WorkspaceModule struct{}

func (m *WorkspaceModule) Name() string { return "workspace" }

func (m *WorkspaceModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change
	cfg := rc.Config.Modules.Workspace

	if !cfg.Enabled || cfg.Path == "" {
		return &CheckResult{Satisfied: true}, nil
	}

	workspacePath := cfg.Path

	// gdrive → workspace symlink
	if cfg.Gdrive != "" {
		gdriveWork := filepath.Join(cfg.Gdrive, "work")
		gdriveTarget := gdriveWork
		// if gdrive/work doesn't exist, fall back to gdrive itself
		if !rc.Runner.IsDir(gdriveWork) {
			gdriveTarget = cfg.Gdrive
		}
		if !m.symlinkOK(rc, workspacePath, gdriveTarget) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", workspacePath, gdriveTarget),
				Command:     fmt.Sprintf("ln -sfn %s %s", gdriveTarget, workspacePath),
			})
		}
	}

	// ~/.brain → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	brainLink := filepath.Join(rc.HomeDir, ".brain")
	if rc.Runner.IsDir(vaultPath) || rc.Runner.IsSymlink(vaultPath) {
		if !m.symlinkOK(rc, brainLink, vaultPath) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", brainLink, vaultPath),
				Command:     fmt.Sprintf("ln -sfn %s %s", vaultPath, brainLink),
			})
		}
	}

	// workspace/work/.vault → workspace/vault
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if !m.symlinkOK(rc, vaultXref, vaultPath) {
			changes = append(changes, Change{
				Description: fmt.Sprintf("symlink %s -> %s", vaultXref, vaultPath),
				Command:     fmt.Sprintf("ln -sfn %s %s", vaultPath, vaultXref),
			})
		}
	}

	// workspace/vault/.work → workspace/work
	if rc.Runner.IsDir(vaultPath) || rc.Runner.IsSymlink(vaultPath) {
		workXref := filepath.Join(vaultPath, ".work")
		if !m.symlinkOK(rc, workXref, workDir) {
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

	// gdrive → workspace symlink
	if cfg.Gdrive != "" {
		gdriveWork := filepath.Join(cfg.Gdrive, "work")
		gdriveTarget := gdriveWork
		if !rc.Runner.IsDir(gdriveWork) {
			gdriveTarget = cfg.Gdrive
		}
		if !m.symlinkOK(rc, workspacePath, gdriveTarget) {
			if err := m.ensureSymlink(rc, workspacePath, gdriveTarget); err != nil {
				return nil, fmt.Errorf("symlinking workspace: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", workspacePath, gdriveTarget))
		}
	}

	// ~/.brain → workspace/vault
	vaultPath := filepath.Join(workspacePath, "vault")
	brainLink := filepath.Join(rc.HomeDir, ".brain")
	if rc.Runner.IsDir(vaultPath) || rc.Runner.IsSymlink(vaultPath) {
		if !m.symlinkOK(rc, brainLink, vaultPath) {
			if err := m.ensureSymlink(rc, brainLink, vaultPath); err != nil {
				return nil, fmt.Errorf("symlinking .brain: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", brainLink, vaultPath))
		}
	}

	// workspace/work/.vault → workspace/vault
	workDir := filepath.Join(workspacePath, "work")
	if rc.Runner.IsDir(workDir) {
		vaultXref := filepath.Join(workDir, ".vault")
		if !m.symlinkOK(rc, vaultXref, vaultPath) {
			if err := m.ensureSymlink(rc, vaultXref, vaultPath); err != nil {
				return nil, fmt.Errorf("symlinking work/.vault: %w", err)
			}
			messages = append(messages, fmt.Sprintf("symlinked %s -> %s", vaultXref, vaultPath))
		}
	}

	// workspace/vault/.work → workspace/work
	if rc.Runner.IsDir(vaultPath) || rc.Runner.IsSymlink(vaultPath) {
		workXref := filepath.Join(vaultPath, ".work")
		if !m.symlinkOK(rc, workXref, workDir) {
			if err := m.ensureSymlink(rc, workXref, workDir); err != nil {
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

// symlinkOK returns true if link exists, is a symlink, and points to target.
func (m *WorkspaceModule) symlinkOK(rc *RunContext, link, target string) bool {
	if !rc.Runner.IsSymlink(link) {
		return false
	}
	actual, err := rc.Runner.Readlink(link)
	if err != nil {
		return false
	}
	return actual == target
}

// ensureSymlink removes any existing file/symlink at link and creates a new symlink.
func (m *WorkspaceModule) ensureSymlink(rc *RunContext, link, target string) error {
	if rc.Runner.FileExists(link) || rc.Runner.IsSymlink(link) {
		if err := rc.Runner.Remove(link); err != nil {
			return fmt.Errorf("removing %s: %w", link, err)
		}
	}
	return rc.Runner.Symlink(target, link)
}
