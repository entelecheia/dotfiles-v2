package module

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
			templatePath: "git/gitignore.global",
			destPath:     filepath.Join(rc.HomeDir, ".config", "git", "gitignore.global"),
			isTemplate:   false,
			perm:         0644,
		},
	}
}

// legacyGitIgnore is the static content dot deployed to ~/.config/git/ignore
// before gitignore.global became the managed excludesFile (#142). The file is
// removed on apply only when it still matches this content byte for byte; a
// locally edited copy is left alone.
const legacyGitIgnore = `.DS_Store
Thumbs.db
*.swp
*.swo
*~
.env
.env.local
.venv/
__pycache__/
node_modules/
.idea/
.vscode/
*.pyc

**/.claude/settings.local.json
`

func legacyIgnorePath(rc *RunContext) string {
	return filepath.Join(rc.HomeDir, ".config", "git", "ignore")
}

// legacyIgnoreRemovable reports whether ~/.config/git/ignore exists and is
// safe to delete: present and byte-identical to what dot deployed. A missing
// file and a diverged file both report false.
func legacyIgnoreRemovable(rc *RunContext) (present bool, removable bool, err error) {
	content, err := os.ReadFile(legacyIgnorePath(rc))
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, string(content) == legacyGitIgnore, nil
}

func (m *GitModule) Check(ctx context.Context, rc *RunContext) (*CheckResult, error) {
	changes, err := checkTemplatedFiles(rc, m.files(rc))
	if err != nil {
		return nil, err
	}

	_, removable, err := legacyIgnoreRemovable(rc)
	if err != nil {
		return nil, fmt.Errorf("reading legacy git ignore: %w", err)
	}
	if removable {
		changes = append(changes, Change{
			Description: fmt.Sprintf("remove legacy %s (superseded by gitignore.global)", legacyIgnorePath(rc)),
			Command:     "dot apply --module git",
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

	present, removable, err := legacyIgnoreRemovable(rc)
	if err != nil {
		return nil, fmt.Errorf("reading legacy git ignore: %w", err)
	}
	switch {
	case present && removable:
		if !rc.DryRun {
			if err := os.Remove(legacyIgnorePath(rc)); err != nil {
				return nil, fmt.Errorf("removing legacy git ignore: %w", err)
			}
		}
		messages = append(messages, fmt.Sprintf("removed legacy %s (superseded by gitignore.global)", legacyIgnorePath(rc)))
	case present:
		fmt.Fprintf(rc.out(), "  ⚠ git: kept %s (local edits; no longer managed, remove by hand)\n", legacyIgnorePath(rc))
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
