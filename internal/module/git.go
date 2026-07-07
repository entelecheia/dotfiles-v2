package module

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
)

// GitModule manages git configuration files.
type GitModule struct{}

func (m *GitModule) Name() string { return "git" }

func (m *GitModule) files(rc *RunContext) []templatedFile {
	return []templatedFile{
		{
			templatePath: "git/config.tmpl",
			destPath:     filepath.Join(rc.HomeDir, ".config", "git", "config"),
			isTemplate:   true,
			perm:         0644,
		},
		{
			templatePath: "git/ignore",
			destPath:     filepath.Join(rc.HomeDir, ".config", "git", "ignore"),
			isTemplate:   false,
			perm:         0644,
		},
	}
}

func (m *GitModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
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

	fileMessages, err := applyTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}
	messages = append(messages, fileMessages...)

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
