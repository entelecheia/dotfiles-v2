package module

import (
	"context"
	"fmt"
	"path/filepath"

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

	return &ApplyResult{Changed: len(messages) > 0, Messages: messages}, nil
}
