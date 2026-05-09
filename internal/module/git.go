package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
)

// GitModule manages git configuration files.
type GitModule struct{}

func (m *GitModule) Name() string { return "git" }

func (m *GitModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	var changes []Change

	configDest := filepath.Join(rc.HomeDir, ".config", "git", "config")
	ignoreDest := filepath.Join(rc.HomeDir, ".config", "git", "ignore")

	configContent, err := rc.Template.Render("git/config.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering git/config.tmpl: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, configDest, configContent) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", configDest),
			Command:     "render git/config.tmpl -> ~/.config/git/config",
		})
	}

	ignoreContent, err := rc.Template.ReadStatic("git/ignore")
	if err != nil {
		return nil, fmt.Errorf("reading git/ignore: %w", err)
	}
	if fileutil.NeedsUpdate(rc.Runner, ignoreDest, ignoreContent) {
		changes = append(changes, Change{
			Description: fmt.Sprintf("write %s", ignoreDest),
			Command:     "copy git/ignore -> ~/.config/git/ignore",
		})
	}
	if mode := rc.Config.Modules.Git.CoauthorGuard; mode != "" && mode != aisettings.CoauthorGuardOff {
		manager := aisettings.NewCoauthorGuardManager(rc.Runner, rc.HomeDir)
		status, err := manager.Status(mode)
		if err != nil {
			return nil, fmt.Errorf("coauthor guard status: %w", err)
		}
		if status.Conflict != "" {
			return nil, fmt.Errorf("%s", status.Conflict)
		}
		if status.HookDrift != "in-sync" {
			changes = append(changes, Change{
				Description: fmt.Sprintf("write %s", status.HookPath),
				Command:     "dot ai coauthor-guard apply",
			})
		}
		if status.HooksPathDrift != "in-sync" {
			changes = append(changes, Change{
				Description: "enable git core.hooksPath for dotfiles hooks",
				Command:     "dot ai coauthor-guard apply",
			})
		}
		if status.AgentsDrift != "in-sync" {
			changes = append(changes, Change{
				Description: fmt.Sprintf("apply coauthor guard AGENTS instruction (%s)", status.AgentsDrift),
				Command:     "dot ai coauthor-guard apply",
			})
		}
	}

	return &CheckResult{Satisfied: len(changes) == 0, Changes: changes}, nil
}

func (m *GitModule) Apply(ctx context.Context, rc *RunContext) (*ApplyResult, error) {
	var messages []string

	configDest := filepath.Join(rc.HomeDir, ".config", "git", "config")
	ignoreDest := filepath.Join(rc.HomeDir, ".config", "git", "ignore")

	configContent, err := rc.Template.Render("git/config.tmpl", rc.Config.TemplateData())
	if err != nil {
		return nil, fmt.Errorf("rendering git/config.tmpl: %w", err)
	}
	if mode := rc.Config.Modules.Git.CoauthorGuard; mode != "" && mode != aisettings.CoauthorGuardOff {
		manager := aisettings.NewCoauthorGuardManager(rc.Runner, rc.HomeDir)
		status, err := manager.Status(mode)
		if err != nil {
			return nil, fmt.Errorf("coauthor guard status: %w", err)
		}
		if status.Conflict != "" {
			return nil, fmt.Errorf("%s", status.Conflict)
		}
	}
	written, err := fileutil.EnsureFile(rc.Runner, configDest, configContent, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", configDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", configDest))
	}

	ignoreContent, err := rc.Template.ReadStatic("git/ignore")
	if err != nil {
		return nil, fmt.Errorf("reading git/ignore: %w", err)
	}
	written, err = fileutil.EnsureFile(rc.Runner, ignoreDest, ignoreContent, 0644)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", ignoreDest, err)
	}
	if written {
		messages = append(messages, fmt.Sprintf("wrote %s", ignoreDest))
	}
	if mode := rc.Config.Modules.Git.CoauthorGuard; mode != "" && mode != aisettings.CoauthorGuardOff {
		manager := aisettings.NewCoauthorGuardManager(rc.Runner, rc.HomeDir)
		result, err := manager.Apply(aisettings.CoauthorGuardOptions{Mode: mode, DryRun: rc.DryRun})
		if err != nil {
			return nil, fmt.Errorf("applying coauthor guard: %w", err)
		}
		if result.HookChanged {
			messages = append(messages, fmt.Sprintf("wrote %s", result.Status.HookPath))
		}
		if result.ConfigChanged {
			messages = append(messages, fmt.Sprintf("enabled git hooksPath in %s", result.Status.GitConfigPath))
		}
	}

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
